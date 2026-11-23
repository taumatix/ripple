package engine

import (
	"context"
	"encoding/json"
	"sort"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
)

const (
	actionSkipPrefix = "actions-shape:"
	// maxShapeSkips bounds the list kept per repo, and with it the cursor value and the
	// dashboard section.
	maxShapeSkips = 100
)

// shapeSkip is a github-tags dependency that has a newer tag upstream, none of which has
// its shape: `v4` while only `v5.0.0` exists. ripple leaves it alone (moving it would
// change how it floats) and says so on the dependency dashboard, since otherwise the
// action is silently never updated.
type shapeSkip struct {
	Package string `json:"pkg"`
	Version string `json:"version"`
	Newest  string `json:"newest"`
}

// newestOtherShape is the newest tag, of any shape, that is newer than cur, or "".
func newestOtherShape(cur string, tags []string) string {
	curSV, _, ok := tagShape(cur)
	if !ok {
		return ""
	}
	best, bestSV := "", curSV
	for _, t := range tags {
		if sv, _, ok := tagShape(t); ok && semver.Compare(sv, bestSV) > 0 {
			best, bestSV = t, sv
		}
	}
	return best
}

func (e *Engine) loadShapeSkips(ctx context.Context, repo string) ([]shapeSkip, error) {
	raw, err := e.Store.GetCursor(ctx, actionSkipPrefix+repo)
	if err != nil || raw == "" {
		return nil, err
	}
	var out []shapeSkip
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil, nil // unreadable: rebuilt by the next poll
	}
	return out, nil
}

// noteShapeSkip records (newest != "") or clears (newest == "") the skip of d. Only a repo
// with a dependency dashboard keeps one: nowhere else shows it. advMu serialises the
// read-modify-write; a lost update is rebuilt by the next poll.
func (e *Engine) noteShapeSkip(ctx context.Context, rs *repoState, d model.Dependency, newest string) error {
	if !rs.cfg.DependencyDashboard {
		return nil
	}
	e.advMu.Lock()
	defer e.advMu.Unlock()
	list, err := e.loadShapeSkips(ctx, d.Repo)
	if err != nil {
		return err
	}
	want := shapeSkip{Package: d.Package, Version: d.Version, Newest: newest}
	out := make([]shapeSkip, 0, len(list)+1)
	had := false
	for _, s := range list {
		if s.Package == d.Package && s.Version == d.Version {
			had = true
			if s == want {
				return nil
			}
			continue // replaced below, or cleared
		}
		out = append(out, s)
	}
	switch {
	case newest != "" && len(out) < maxShapeSkips:
		out = append(out, want)
	case newest == "" && !had:
		return nil
	}
	val := ""
	if len(out) > 0 {
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		val = string(b)
	}
	if err := e.Store.SetCursor(ctx, actionSkipPrefix+d.Repo, val); err != nil {
		return err
	}
	e.markDashboard(d.Repo)
	return nil
}

// currentShapeSkips is repo's list without the entries whose dependency has since moved
// to another version (or gone).
func (e *Engine) currentShapeSkips(ctx context.Context, repo string) ([]shapeSkip, error) {
	list, err := e.loadShapeSkips(ctx, repo)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	deps, err := e.Store.ListRepoDependencies(ctx, repo)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, d := range deps {
		if d.Datasource == model.DatasourceGitHubTags {
			have[d.Package+"|"+d.Version] = true
		}
	}
	var out []shapeSkip
	for _, s := range list {
		if have[s.Package+"|"+s.Version] {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}
