package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
)

// The index backfill (entry 126). The osv-jobs:<id> index is written when a record makes a
// job, so a job made by a release before the index existed is not in it and a later
// correction of its record could not find the job. Once, after an upgrade, the watcher
// walks the registered repos, SweepBatch per poll in name order, lists each one's open
// jobs (pending and failed) and indexes the ones that carry an advisory note. The position
// is the cursor osv-jobs-backfill ("done" when finished), so a restart resumes it and a
// deployment that never had old jobs pays for one pass over the repo list.
//
// A job that is already done is not listed by the store, so the PR of an old done job is
// not reached by that pass. A second, opt-in pass (AdvisoryWatcher.BackfillOpenPRs, cursor
// osv-jobs-backfill-prs) reaches it from the host: it lists each repo's open ripple PRs once,
// reads the module and version out of a go branch (the shape supersede already relies on),
// looks the job up by key and indexes it when it is done, opened that very PR and carries an
// advisory note. It needs no host capability beyond ListOpenPRs, so every host has it.
const (
	advisoryBackfillCursor = "osv-jobs-backfill"
	advisoryBackfillPRs    = "osv-jobs-backfill-prs"
	backfillDone           = "done"
)

// backfillStep indexes the next batch of repos' open jobs, then (when enabled) the jobs
// behind their open PRs.
func (w *AdvisoryWatcher) backfillStep(ctx context.Context) error {
	if err := w.backfillWalk(ctx, advisoryBackfillCursor, w.backfillOpenJobs); err != nil {
		return err
	}
	if !w.BackfillOpenPRs {
		return nil
	}
	return w.backfillWalk(ctx, advisoryBackfillPRs, w.backfillPRJobs)
}

func (w *AdvisoryWatcher) backfillOpenJobs(ctx context.Context, name string) error {
	e := w.Engine
	lister, ok := e.Store.(store.OpenJobLister)
	if !ok {
		return nil
	}
	jobs, err := lister.ListOpenJobs(ctx, name)
	if err != nil {
		return fmt.Errorf("osv backfill of %s: %w", name, err)
	}
	for _, j := range jobs {
		if n := e.advisoryNoteFor(ctx, j); n != nil {
			if err := e.indexAdd(ctx, advisoryJobsPrefix+n.ID, j.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

// backfillPRJobs indexes the done jobs whose PR is still open on the host. A host error
// other than a rate limit skips the repo (a deleted or unreadable repo must not hold the
// walk); the rest of the repos are still covered.
func (w *AdvisoryWatcher) backfillPRJobs(ctx context.Context, name string) error {
	e := w.Engine
	repo, found, err := e.Store.GetRepo(ctx, name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	open, err := e.Platform.ListOpenPRs(ctx, repo, BranchPrefix)
	if err != nil {
		var rl *platform.RateLimitError
		if errors.As(err, &rl) {
			return err
		}
		e.log().Warn("osv: PR backfill skipped a repo", "repo", name, "err", err)
		return nil
	}
	if len(open) == 0 {
		return nil
	}
	deps, err := e.Store.ListRepoDependencies(ctx, name)
	if err != nil {
		return err
	}
	for _, pr := range open {
		for _, d := range deps {
			if d.Datasource != model.DatasourceGo {
				continue
			}
			ver, ok := staleVersion(pr.Head, d.Package)
			if !ok {
				continue
			}
			j, ok, err := e.Store.GetJob(ctx, model.JobKey(name, d.Datasource, d.Package, ver))
			if err != nil {
				return err
			}
			if !ok || j.State != model.JobDone || j.PRURL != pr.URL {
				continue
			}
			if n := e.advisoryNoteFor(ctx, j); n != nil {
				if err := e.indexAdd(ctx, advisoryJobsPrefix+n.ID, j.Key); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// backfillWalk runs visit over the next batch of repos, in name order, under cursor.
func (w *AdvisoryWatcher) backfillWalk(ctx context.Context, cursor string, visit func(context.Context, string) error) error {
	e := w.Engine
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil || raw == backfillDone {
		return err
	}
	rl, ok := e.Store.(store.RepoLister)
	if !e.Advisories || !ok {
		return nil
	}
	after := ""
	if raw != "" {
		var st sweepState
		if json.Unmarshal([]byte(raw), &st) == nil {
			after = st.After
		}
	}
	batch := w.SweepBatch
	if batch <= 0 {
		batch = DefaultSweepBatch
	}
	names, err := rl.ListRepos(ctx, after, batch)
	if err != nil {
		return fmt.Errorf("osv backfill: list repos: %w", err)
	}
	for _, name := range names {
		if err := visit(ctx, name); err != nil {
			return err
		}
		b, _ := json.Marshal(sweepState{After: name})
		if err := e.Store.SetCursor(ctx, cursor, string(b)); err != nil {
			return err
		}
	}
	if len(names) < batch {
		e.log().Info("osv: job index backfill finished", "pass", cursor)
		return e.Store.SetCursor(ctx, cursor, backfillDone)
	}
	return nil
}
