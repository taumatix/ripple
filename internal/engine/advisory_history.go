package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
)

const (
	advisoryCheckedPrefix = "osv-checked:"
	// maxHistoryQueries bounds the (dependency, version) pairs one scan sends to OSV;
	// the rest are checked by the next scan.
	maxHistoryQueries = 500
	// maxHistoryFetch bounds the record requests of one scan, for the same reason.
	maxHistoryFetch = 100
	// historyCacheTTL is how long a fetched record is reused by the scans of other repos,
	// so registering many repos at once does not fetch the same record again for each.
	historyCacheTTL = 10 * time.Minute
)

// AdvisoryQuerier is the OSV surface the history check reads; *advisory.Client implements it.
type AdvisoryQuerier interface {
	QueryBatch(ctx context.Context, qs []advisory.Query) ([][]string, error)
	Vuln(ctx context.Context, id string) (*advisory.Vuln, error)
}

type cachedVuln struct {
	v  *advisory.Vuln // nil: OSV does not know the id
	at time.Time
}

func pairKey(d model.Dependency) string {
	h := sha256.Sum256([]byte(d.Datasource + "|" + d.Package + "|" + d.Version))
	return hex.EncodeToString(h[:6])
}

// checkAdvisoryHistory gives a repo the security jobs the advisory poller would have
// opened had the repo been registered when each advisory was published. The poller only
// ever sees records modified after its cursor, so a repo that registers later, or a
// dependency bumped to a version an older advisory covers, would otherwise wait for the
// advisory to be edited. One POST /v1/querybatch asks OSV which advisories affect each
// (module, version) the repo uses; each such record is fetched and applied to this repo
// only.
//
// A pair is remembered once checked (cursor osv-checked:<repo>), so a rescan of an
// unchanged repo makes no request. Nothing is remembered when OSV fails: the next scan
// retries. The caller holds the repo lock and has already stored the repo and its
// config; deps are the repo's dependencies as just scanned.
func (e *Engine) checkAdvisoryHistory(ctx context.Context, repo string, cfg *config.Repo, deps []model.Dependency) error {
	_, err := e.checkAdvisoryHistoryMore(ctx, repo, cfg, deps)
	return err
}

// checkAdvisoryHistoryMore is one round of the check. It reports whether pairs are still
// unchecked because the round's query or fetch budget ran out (the sweep runs more rounds).
func (e *Engine) checkAdvisoryHistoryMore(ctx context.Context, repo string, cfg *config.Repo, deps []model.Dependency) (more bool, err error) {
	q := e.AdvisoryHistory
	if q == nil {
		return false, nil
	}
	maxQueries, maxFetch := e.historyCaps()
	var cands []model.Dependency
	seen := map[string]bool{}
	for _, d := range deps {
		ok := d.Datasource == model.DatasourceGo ||
			(d.Datasource == model.DatasourceGoToolchain && d.Package == model.PackageGoToolchain)
		if !ok || !semver.IsValid(d.Version) {
			continue
		}
		if _, covered := cfg.UpdateFor(d.Manager, d.File); !covered {
			continue
		}
		if k := pairKey(d); !seen[k] {
			seen[k] = true
			cands = append(cands, d)
		}
	}
	raw, err := e.Store.GetCursor(ctx, advisoryCheckedPrefix+repo)
	if err != nil {
		return false, err
	}
	var prev []string
	_ = json.Unmarshal([]byte(raw), &prev) // unreadable: everything is checked again
	checked := map[string]bool{}
	for _, k := range prev {
		checked[k] = true
	}
	var fresh []model.Dependency
	for _, d := range cands {
		if !checked[pairKey(d)] {
			fresh = append(fresh, d)
		}
	}
	if len(fresh) == 0 {
		return false, e.saveChecked(ctx, repo, cands, checked, nil) // drops pairs no longer used
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].Package != fresh[j].Package {
			return fresh[i].Package < fresh[j].Package
		}
		return fresh[i].Version < fresh[j].Version
	})
	total := len(fresh)
	if len(fresh) > maxQueries {
		fresh = fresh[:maxQueries]
	}
	var qs []advisory.Query
	owner := []int{} // query index -> fresh index
	for i, d := range fresh {
		names := []string{d.Package}
		if d.Datasource == model.DatasourceGoToolchain {
			names = []string{"stdlib", "toolchain"}
		}
		for _, n := range names {
			qs = append(qs, advisory.Query{Name: n, Version: d.Version})
			owner = append(owner, i)
		}
	}
	answers, err := q.QueryBatch(ctx, qs)
	if err != nil {
		return false, fmt.Errorf("osv querybatch: %w", err)
	}
	perDep := make([][]string, len(fresh))
	for qi, ids := range answers {
		perDep[owner[qi]] = append(perDep[owner[qi]], ids...)
	}
	// Take pairs while their records fit this scan's fetch budget; the rest wait.
	unique := map[string]bool{}
	take := 0
	for i := range fresh {
		added := 0
		for _, id := range perDep[i] {
			if !unique[id] {
				added++
			}
		}
		if take > 0 && len(unique)+added > maxFetch {
			break
		}
		for _, id := range perDep[i] {
			unique[id] = true
		}
		take++
	}
	fresh = fresh[:take]
	more = take < total
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	repos := map[string]*repoState{}
	for _, id := range ids {
		v, err := e.historyRecord(ctx, q, id)
		if err != nil {
			return false, fmt.Errorf("osv record %s: %w", id, err)
		}
		if v == nil || v.Withdrawn() {
			continue
		}
		if _, err := e.applyAdvisory(ctx, v, newNote(v), fresh, repos); err != nil {
			return false, err
		}
	}
	return more, e.saveChecked(ctx, repo, cands, checked, fresh)
}

// historyCaps are the per-round bounds: the Engine fields, else the defaults.
func (e *Engine) historyCaps() (queries, fetch int) {
	queries, fetch = e.HistoryQueries, e.HistoryFetch
	if queries <= 0 {
		queries = maxHistoryQueries
	}
	if fetch <= 0 {
		fetch = maxHistoryFetch
	}
	return queries, fetch
}

// saveChecked remembers the pairs checked before (still in use) plus the ones just done.
func (e *Engine) saveChecked(ctx context.Context, repo string, cands []model.Dependency, checked map[string]bool, done []model.Dependency) error {
	keep := map[string]bool{}
	for _, d := range cands {
		if k := pairKey(d); checked[k] {
			keep[k] = true
		}
	}
	for _, d := range done {
		keep[pairKey(d)] = true
	}
	if len(keep) == len(checked) && len(done) == 0 {
		return nil
	}
	list := make([]string, 0, len(keep))
	for k := range keep {
		list = append(list, k)
	}
	sort.Strings(list)
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return e.Store.SetCursor(ctx, advisoryCheckedPrefix+repo, string(b))
}

// historyRecord fetches one record through a short-lived cache. An id OSV does not know
// is cached too, as nil.
func (e *Engine) historyRecord(ctx context.Context, q AdvisoryQuerier, id string) (*advisory.Vuln, error) {
	e.advMu.Lock()
	c, ok := e.histCache[id]
	e.advMu.Unlock()
	if ok && e.now().Sub(c.at) < historyCacheTTL {
		return c.v, nil
	}
	v, err := q.Vuln(ctx, id)
	if errors.Is(err, advisory.ErrNotFound) {
		v, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.advMu.Lock()
	if e.histCache == nil || len(e.histCache) > 4*maxHistoryFetch {
		e.histCache = map[string]cachedVuln{}
	}
	e.histCache[id] = cachedVuln{v: v, at: e.now()}
	e.advMu.Unlock()
	return v, nil
}
