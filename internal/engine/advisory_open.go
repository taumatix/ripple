package engine

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/model"
)

const (
	advisoryOpenPrefix = "osv-open:"
	// maxOpenAdvisories bounds the list kept per repo, and with it the cursor value and
	// the dashboard section.
	maxOpenAdvisories = 100
)

// openAdvisory is an advisory that affects a repo's dependency and that ripple cannot
// propose a fix for: no release fixes it, or only a pseudo-version does and the repo did
// not opt in. It is listed on the repo's dependency dashboard, the one place a report
// reaches the repo's owners without a PR.
type openAdvisory struct {
	ID         string `json:"id"`
	Datasource string `json:"ds"`
	Package    string `json:"pkg"`
	Version    string `json:"version"`
	Why        string `json:"why"`
}

func (o openAdvisory) same(id, ds, pkg string) bool {
	return o.ID == id && o.Datasource == ds && o.Package == pkg
}

func (e *Engine) loadOpenAdvisories(ctx context.Context, repo string) ([]openAdvisory, error) {
	raw, err := e.Store.GetCursor(ctx, advisoryOpenPrefix+repo)
	if err != nil || raw == "" {
		return nil, err
	}
	var out []openAdvisory
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil, nil // unreadable: start again rather than fail a poll
	}
	return out, nil
}

func (e *Engine) saveOpenAdvisories(ctx context.Context, repo string, list []openAdvisory) error {
	val := ""
	if len(list) > 0 {
		b, err := json.Marshal(list)
		if err != nil {
			return err
		}
		val = string(b)
	}
	return e.Store.SetCursor(ctx, advisoryOpenPrefix+repo, val)
}

// recordOpenAdvisory remembers o for repo, if the repo has a dependency dashboard to show
// it on. It takes no repo lock (Scan holds it, the poller does not); advMu serialises the
// read-modify-write instead.
func (e *Engine) recordOpenAdvisory(ctx context.Context, rs *repoState, d model.Dependency, o openAdvisory) error {
	if !rs.cfg.DependencyDashboard {
		return nil
	}
	e.advMu.Lock()
	defer e.advMu.Unlock()
	list, err := e.loadOpenAdvisories(ctx, d.Repo)
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].same(o.ID, o.Datasource, o.Package) {
			if list[i] == o {
				return nil
			}
			list[i] = o
			e.markDashboard(d.Repo)
			return e.saveOpenAdvisories(ctx, d.Repo, list)
		}
	}
	if len(list) >= maxOpenAdvisories {
		return nil
	}
	e.markDashboard(d.Repo)
	return e.saveOpenAdvisories(ctx, d.Repo, append(list, o))
}

// clearOpenAdvisory drops an entry once a fix exists (or the record is withdrawn).
func (e *Engine) clearOpenAdvisory(ctx context.Context, repo, id, ds, pkg string) error {
	e.advMu.Lock()
	defer e.advMu.Unlock()
	list, err := e.loadOpenAdvisories(ctx, repo)
	if err != nil || len(list) == 0 {
		return err
	}
	kept := list[:0:0]
	for _, o := range list {
		if !o.same(id, ds, pkg) {
			kept = append(kept, o)
		}
	}
	if len(kept) == len(list) {
		return nil
	}
	e.markDashboard(repo)
	return e.saveOpenAdvisories(ctx, repo, kept)
}

// forgetOpenAdvisories clears a withdrawn record from every repo that depends on what it
// names. Withdrawals are rare, so one cursor read per dependent is fine.
func (e *Engine) forgetOpenAdvisories(ctx context.Context, v *advisory.Vuln) error {
	clear := func(ds, pkg string) error {
		deps, err := e.Store.ListDependents(ctx, ds, pkg)
		if err != nil {
			return err
		}
		for _, d := range deps {
			if err := e.clearOpenAdvisory(ctx, d.Repo, v.ID, ds, d.Package); err != nil {
				return err
			}
		}
		return nil
	}
	for _, m := range v.Modules() {
		if err := clear(model.DatasourceGo, m); err != nil {
			return err
		}
	}
	if v.HasToolchain() {
		return clear(model.DatasourceGoToolchain, model.PackageGoToolchain)
	}
	return nil
}

// currentOpenAdvisories is repo's list without the entries whose dependency has since
// moved to another version (or gone): the entry described a version that is no longer used.
func (e *Engine) currentOpenAdvisories(ctx context.Context, repo string) ([]openAdvisory, error) {
	list, err := e.loadOpenAdvisories(ctx, repo)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	deps, err := e.Store.ListRepoDependencies(ctx, repo)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, d := range deps {
		have[d.Datasource+"|"+d.Package+"|"+d.Version] = true
	}
	var out []openAdvisory
	for _, o := range list {
		if have[o.Datasource+"|"+o.Package+"|"+o.Version] {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}

// listOpenAdvisory is recordOpenAdvisory plus the index that lets a corrected record find
// the repos it listed on (see advisory_correct.go). The index is written without advMu
// held: indexAdd takes it.
func (e *Engine) listOpenAdvisory(ctx context.Context, rs *repoState, d model.Dependency, o openAdvisory) error {
	if err := e.recordOpenAdvisory(ctx, rs, d, o); err != nil {
		return err
	}
	if !rs.cfg.DependencyDashboard {
		return nil
	}
	return e.indexAdd(ctx, advisoryOpenIxPrefix+o.ID, d.Repo)
}
