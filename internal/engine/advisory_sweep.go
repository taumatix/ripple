package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/taumatix/ripple/internal/store"
)

// The sweep (entry 80). The history check asks OSV about a (dependency, version) pair
// once per repo, and the poller reads at most Client.MaxLines lines of OSV's modified
// list. A gap longer than that (an outage, a stopped replica) skips the oldest changed
// records for good, and a repo whose go.mod has not changed is never asked again. So when
// a poll finds its read was truncated, the watcher sweeps: it re-asks OSV about every
// registered repo, SweepBatch repos per poll in name order, with the history memo of each
// cleared. The position is the cursor osv-sweep, so a restart resumes it; a second gap
// during a sweep starts it over, which covers a superset.
//
// It needs a store that lists repos (store.RepoLister: every store here) and the history querier; without them a truncated read only logs.
const (
	advisorySweepCursor = "osv-sweep"
	// DefaultSweepBatch is the repos one poll sweeps.
	DefaultSweepBatch = 10
)

type sweepState struct {
	After string `json:"after"` // the last repo swept; "" is the start
	// Cleared is the repo whose history memo was already cleared and which a poll left
	// part-checked (a repo with more pairs than one round reads), so the next poll
	// continues it instead of starting it over.
	Cleared string `json:"cleared,omitempty"`
}

// sweepRounds bounds the history rounds one poll spends on one repo.
const sweepRounds = 10

// beginSweep marks that the repos need re-checking, unless a sweep is already running
// and has not yet got past the start (restarting a sweep that is mid-way is also right,
// since the new gap is later than its start, so it always restarts).
func (w *AdvisoryWatcher) beginSweep(ctx context.Context) error {
	e := w.Engine
	if e.AdvisoryHistory == nil {
		return nil
	}
	if _, ok := e.Store.(store.RepoLister); !ok {
		e.log().Warn("osv: the skipped records cannot be swept for: this store cannot list repos")
		return nil
	}
	b, _ := json.Marshal(sweepState{})
	e.log().Warn("osv: starting a sweep of every repo for the records the poll skipped")
	return e.Store.SetCursor(ctx, advisorySweepCursor, string(b))
}

// sweepStep checks the next batch of repos of a running sweep.
func (w *AdvisoryWatcher) sweepStep(ctx context.Context) error {
	e := w.Engine
	raw, err := e.Store.GetCursor(ctx, advisorySweepCursor)
	if err != nil || raw == "" {
		return err
	}
	var st sweepState
	if json.Unmarshal([]byte(raw), &st) != nil {
		return e.Store.SetCursor(ctx, advisorySweepCursor, "") // unreadable: drop it
	}
	rl, ok := e.Store.(store.RepoLister)
	if !ok || e.AdvisoryHistory == nil {
		return nil
	}
	batch := w.SweepBatch
	if batch <= 0 {
		batch = DefaultSweepBatch
	}
	names, err := rl.ListRepos(ctx, st.After, batch)
	if err != nil {
		return fmt.Errorf("osv sweep: list repos: %w", err)
	}
	for _, name := range names {
		done, partial, err := e.sweepRepo(ctx, name, st.Cleared != name)
		if err != nil {
			return fmt.Errorf("osv sweep of %s: %w", name, err)
		}
		if partial {
			b, _ := json.Marshal(sweepState{After: st.After, Cleared: name})
			return e.Store.SetCursor(ctx, advisorySweepCursor, string(b))
		}
		if !done {
			return nil // busy: the next poll resumes at this repo
		}
		b, _ := json.Marshal(sweepState{After: name})
		if err := e.Store.SetCursor(ctx, advisorySweepCursor, string(b)); err != nil {
			return err
		}
	}
	if len(names) < batch {
		e.log().Info("osv: sweep finished")
		return e.Store.SetCursor(ctx, advisorySweepCursor, "")
	}
	return nil
}

// sweepRepo forgets what the history check remembered for repo (when clear) and runs it
// again, round after round while the repo has pairs a round could not read, up to
// sweepRounds. It reports done=false when the repo is busy and partial when rounds
// remain, so that the caller keeps the position.
func (e *Engine) sweepRepo(ctx context.Context, name string, clear bool) (done, partial bool, err error) {
	_, release := e.tryRepo(ctx, name)
	if release == nil {
		return false, false, nil
	}
	defer release()
	repo, found, err := e.Store.GetRepo(ctx, name)
	if err != nil || !found {
		return true, false, err
	}
	cfg, err := RepoConfig(repo.Config)
	if err != nil {
		return true, false, nil // an invalid config is reported by the scan, not here
	}
	deps, err := e.Store.ListRepoDependencies(ctx, name)
	if err != nil {
		return true, false, err
	}
	if clear {
		if err := e.Store.SetCursor(ctx, advisoryCheckedPrefix+name, ""); err != nil {
			return true, false, err
		}
	}
	for i := 0; i < sweepRounds; i++ {
		more, err := e.checkAdvisoryHistoryMore(ctx, name, cfg, deps)
		if err != nil {
			return true, false, err
		}
		if !more {
			return true, false, nil
		}
	}
	e.log().Info("osv: sweep continues this repo on the next poll", "repo", name)
	return true, true, nil
}
