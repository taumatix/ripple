package engine

import (
	"context"
	"encoding/json"
	"fmt"

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
// still not reached by a correction; only pending jobs are.
const (
	advisoryBackfillCursor = "osv-jobs-backfill"
	backfillDone           = "done"
)

// backfillStep indexes the next batch of repos' open jobs.
func (w *AdvisoryWatcher) backfillStep(ctx context.Context) error {
	e := w.Engine
	raw, err := e.Store.GetCursor(ctx, advisoryBackfillCursor)
	if err != nil || raw == backfillDone {
		return err
	}
	rl, ok := e.Store.(store.RepoLister)
	lister, ok2 := e.Store.(store.OpenJobLister)
	if !e.Advisories || !ok || !ok2 {
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
		b, _ := json.Marshal(sweepState{After: name})
		if err := e.Store.SetCursor(ctx, advisoryBackfillCursor, string(b)); err != nil {
			return err
		}
	}
	if len(names) < batch {
		e.log().Info("osv: job index backfill finished")
		return e.Store.SetCursor(ctx, advisoryBackfillCursor, backfillDone)
	}
	return nil
}
