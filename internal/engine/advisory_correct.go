package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// Corrected records (entry 82). OSV amends a record after publishing it: a range
// narrowed, an entry withdrawn. A job or PR ripple made from the earlier text then asks
// for a change nothing needs. When a record comes through the poller again, what ripple
// made from it is reconciled with what it now says:
//
//   - a pending job whose dependency the record no longer affects is finished as skipped;
//   - an open PR ripple opened for such a job is closed with a comment, unless a person
//     touched it (the host decides that, as for superseded PRs) or the repo turned
//     close-superseded-pull-requests off;
//   - a dashboard entry for a version the record no longer affects is dropped.
//
// "No longer affected" is judged on the version in use: if the dependency is already at
// or past the job's version, nothing was corrected, the work was simply done. A fix that
// was raised is the supersede flow's business: its new job replaces the old PR.
//
// Two indexes make this a lookup instead of a scan, written when a record makes a job or
// a dashboard entry: osv-jobs:<id> (job keys) and osv-open-ix:<id> (repos). A job made
// before they existed is indexed once by the backfill (advisory_backfill.go); a done one is not found.
const (
	advisoryJobsPrefix   = "osv-jobs:"
	advisoryOpenIxPrefix = "osv-open-ix:"
	// advisoryRetryCursor lists the record ids whose correction found a repo busy; the
	// watcher applies them again on its next poll (retryBusy).
	advisoryRetryCursor = "osv-reconcile-retry"
	// maxIndexed bounds each index, and with it the work one corrected record causes.
	maxIndexed = 100
	// correctWait is how long a corrected record waits for a repo another run holds.
	correctWait = 2 * time.Second
)

// indexAdd appends v to the string list at the cursor, once, up to maxIndexed.
func (e *Engine) indexAdd(ctx context.Context, cursor, v string) error {
	e.advMu.Lock()
	defer e.advMu.Unlock()
	list, err := e.indexRead(ctx, cursor)
	if err != nil {
		return err
	}
	for _, x := range list {
		if x == v {
			return nil
		}
	}
	if len(list) >= maxIndexed {
		return nil
	}
	b, err := json.Marshal(append(list, v))
	if err != nil {
		return err
	}
	return e.Store.SetCursor(ctx, cursor, string(b))
}

func (e *Engine) indexRead(ctx context.Context, cursor string) ([]string, error) {
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil || raw == "" {
		return nil, err
	}
	var list []string
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil, nil
	}
	return list, nil
}

// reconcileCorrected applies a record that came through the poller to what ripple made
// from it before. Errors are logged: the record's fan-out has already succeeded.
func (e *Engine) reconcileCorrected(ctx context.Context, v *advisory.Vuln) (busy bool) {
	keys, err := e.indexRead(ctx, advisoryJobsPrefix+v.ID)
	if err != nil {
		e.log().Warn("osv: read job index", "id", v.ID, "err", err)
		return false
	}
	byRepo := map[string][]model.Job{}
	var order []string
	for _, k := range keys {
		j, ok, err := e.Store.GetJob(ctx, k)
		if err != nil || !ok || (j.State != model.JobPending && j.State != model.JobDone) {
			continue
		}
		if n := e.advisoryNoteFor(ctx, j); n == nil || n.ID != v.ID {
			continue // the job belongs to another record of the same advisory
		}
		if len(byRepo[j.Repo]) == 0 {
			order = append(order, j.Repo)
		}
		byRepo[j.Repo] = append(byRepo[j.Repo], j)
	}
	for _, repo := range order {
		if e.reconcileRepo(ctx, v, repo, byRepo[repo]) {
			busy = true
		}
	}
	e.reconcileDashboards(ctx, v)
	return busy
}

// retryBusy applies again the records whose correction found a repo busy: it fetches each
// and reconciles it as the poller would have, and keeps the ones that find a repo busy
// still. A record OSV no longer serves is dropped.
func (w *AdvisoryWatcher) retryBusy(ctx context.Context) {
	e := w.Engine
	ids, err := e.indexRead(ctx, advisoryRetryCursor)
	if err != nil || len(ids) == 0 {
		return
	}
	resolved := map[string]bool{}
	for _, id := range ids {
		v, err := w.Source.Vuln(ctx, id)
		switch {
		case errors.Is(err, advisory.ErrNotFound):
			resolved[id] = true
		case err != nil:
			// OSV is unwell: the next poll tries again.
		case !e.reconcileCorrected(ctx, v):
			resolved[id] = true
		}
	}
	if len(resolved) == 0 {
		return
	}
	e.advMu.Lock()
	defer e.advMu.Unlock()
	list, err := e.indexRead(ctx, advisoryRetryCursor)
	if err != nil {
		return
	}
	var keep []string
	for _, id := range list {
		if !resolved[id] {
			keep = append(keep, id)
		}
	}
	val := ""
	if len(keep) > 0 {
		b, _ := json.Marshal(keep)
		val = string(b)
	}
	if err := e.Store.SetCursor(ctx, advisoryRetryCursor, val); err != nil {
		e.log().Warn("osv: store the corrections to retry", "err", err)
	}
}

func (e *Engine) reconcileRepo(ctx context.Context, v *advisory.Vuln, repoName string, jobs []model.Job) (busy bool) {
	deps, err := e.Store.ListRepoDependencies(ctx, repoName)
	if err != nil {
		e.log().Warn("osv: list dependencies", "repo", repoName, "err", err)
		return false
	}
	var stale []model.Job
	for _, j := range jobs {
		if e.corrected(v, j, deps) {
			stale = append(stale, j)
		}
	}
	if len(stale) == 0 {
		return false
	}
	hold, release := e.tryRepo(ctx, repoName)
	if release == nil {
		e.log().Warn("osv: repo busy; the correction is retried on a later poll", "repo", repoName, "id", v.ID)
		if err := e.indexAdd(ctx, advisoryRetryCursor, v.ID); err != nil {
			e.log().Warn("osv: remember the correction to retry", "id", v.ID, "err", err)
		}
		return true
	}
	defer release()
	repo, found, err := e.Store.GetRepo(ctx, repoName)
	if err != nil || !found {
		return false
	}
	var open []platform.PR
	opened := false
	for _, j := range stale {
		// Read again under the lease: a run may have finished the job meanwhile.
		cur, ok, err := e.Store.GetJob(ctx, j.Key)
		if err != nil || !ok {
			continue
		}
		reason := fmt.Sprintf("advisory %s was corrected: it no longer affects %s at the version in use", v.ID, j.Package)
		switch cur.State {
		case model.JobPending:
			cur.State, cur.Reason = model.JobSkipped, reason
			if err := e.updateJob(ctx, hold, cur); err != nil {
				e.log().Warn("osv: skip corrected job", "job", cur.Key, "err", err)
				continue
			}
			e.markDashboard(repoName)
		case model.JobDone:
			if cur.PRURL == "" || e.Paused(ctx, repoName) {
				continue
			}
			cfg, err := RepoConfig(repo.Config)
			if err != nil || !cfg.ClosesSuperseded() {
				continue
			}
			if !opened {
				if open, err = e.Platform.ListOpenPRs(ctx, repo, BranchPrefix); err != nil {
					e.log().Warn("osv: list open pull requests", "repo", repoName, "err", err)
					return false
				}
				opened = true
			}
			e.closeCorrected(ctx, repo, cur, open, v.ID)
		}
	}
	return false
}

func (e *Engine) closeCorrected(ctx context.Context, repo model.Repo, j model.Job, open []platform.PR, id string) {
	for _, pr := range open {
		if pr.URL != j.PRURL {
			continue
		}
		comment := fmt.Sprintf("Closing: ripple opened this because of %s, and OSV has since corrected that record so it no longer affects `%s` at the version this repository uses. Nothing here is needed for it. Reopen it if you want the update anyway.", id, j.Package)
		done, err := platform.CloseUntouchedPR(ctx, e.Platform, repo, pr, comment)
		switch {
		case err != nil:
			e.log().Warn("osv: pull request of a corrected advisory not closed", "repo", repo.FullName, "pr", pr.URL, "err", err)
		case !done:
			e.log().Info("osv: pull request of a corrected advisory left open: it was touched by someone else", "repo", repo.FullName, "pr", pr.URL)
		default:
			e.log().Info("osv: closed the pull request of a corrected advisory", "repo", repo.FullName, "pr", pr.URL, "id", id)
		}
		return
	}
}

// corrected reports whether the record, as it reads now, no longer affects the
// dependency the job moves, while that dependency has not reached the job's version.
// A withdrawn record affects nothing.
func (e *Engine) corrected(v *advisory.Vuln, j model.Job, deps []model.Dependency) bool {
	seen := false
	for _, d := range deps {
		if d.Datasource != j.Datasource || d.Package != j.Package {
			continue
		}
		if !semver.IsValid(d.Version) || !semver.IsValid(j.Version) || semver.Compare(d.Version, j.Version) >= 0 {
			return false // already at the fix: the work was done, not corrected
		}
		seen = true
		if ver, ok := verdictFor(v, d); ok && ver.Affected && !v.Withdrawn() {
			return false
		}
	}
	return seen
}

// tryRepo takes the repo's in-process lock and lease for a short job of the advisory
// poller, which must not wait out a PR run that may take minutes. It returns nil when
// the repo stays busy for correctWait (or the lease cannot be read). The returned hold
// fences the caller's job writes on the repo lease (see updateJob).
func (e *Engine) tryRepo(ctx context.Context, name string) (hold *repoLease, release func()) {
	m := e.repoMutex(name)
	deadline := time.Now().Add(correctWait)
	for !m.TryLock() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	wctx, cancel := context.WithTimeout(ctx, time.Until(deadline))
	defer cancel()
	hold, err := e.acquireRepo(wctx, name, true)
	if err != nil || hold == nil {
		m.Unlock()
		return nil, nil
	}
	return hold, func() {
		hold.Release()
		m.Unlock()
	}
}

// reconcileDashboards drops the dashboard entries of the record that no longer apply.
func (e *Engine) reconcileDashboards(ctx context.Context, v *advisory.Vuln) {
	repos, err := e.indexRead(ctx, advisoryOpenIxPrefix+v.ID)
	if err != nil {
		e.log().Warn("osv: read dashboard index", "id", v.ID, "err", err)
		return
	}
	for _, repo := range repos {
		e.advMu.Lock()
		list, err := e.loadOpenAdvisories(ctx, repo)
		if err == nil {
			kept := list[:0:0]
			for _, o := range list {
				if o.ID == v.ID {
					d := model.Dependency{Datasource: o.Datasource, Package: o.Package, Version: o.Version}
					if ver, ok := verdictFor(v, d); !ok || !ver.Affected || v.Withdrawn() {
						continue
					}
				}
				kept = append(kept, o)
			}
			if len(kept) != len(list) {
				e.markDashboard(repo)
				err = e.saveOpenAdvisories(ctx, repo, kept)
			}
		}
		e.advMu.Unlock()
		if err != nil {
			e.log().Warn("osv: update dashboard entries", "repo", repo, "id", v.ID, "err", err)
		}
	}
}
