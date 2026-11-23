package engine

import (
	"context"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// JobRunner runs due jobs continuously and off the feed poll's path: a repo whose
// jobs are running (a slow major-bump verify, say) holds one of Engine.Workers
// slots and nothing else, so the poll and the other repos carry on.
type JobRunner struct {
	Engine *Engine

	mu       sync.Mutex
	inflight map[string]bool
	// finished records, per repo, the generation its last run ended in; a listing
	// taken before that may hold jobs the run has since finished.
	finished map[string]uint64
	gen      uint64
	running  int
	wake     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
}

func (r *JobRunner) init() {
	r.once.Do(func() {
		r.inflight, r.finished = map[string]bool{}, map[string]uint64{}
		r.wake = make(chan struct{}, 1)
	})
}

// Wake asks for a pass now, e.g. after a poll enqueued jobs.
func (r *JobRunner) Wake() {
	r.init()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run makes a pass every period, on Wake, and whenever a repo's run ends, until
// ctx ends; then it waits for the runs in flight.
func (r *JobRunner) Run(ctx context.Context, every time.Duration) {
	r.init()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		r.dispatch(ctx)
		select {
		case <-ctx.Done():
			r.wg.Wait()
			return
		case <-r.wake:
		case <-t.C:
		}
	}
}

func (r *JobRunner) workers() int {
	if r.Engine.Workers < 1 {
		return 4
	}
	return r.Engine.Workers
}

// dispatch starts a run for each repo with due jobs that is not already running,
// while worker slots are free, and returns without waiting for them.
func (r *JobRunner) dispatch(ctx context.Context) {
	e := r.Engine
	r.mu.Lock()
	snap, full := r.gen, r.running >= r.workers()
	var busy []string
	for name := range r.inflight {
		busy = append(busy, name)
	}
	r.mu.Unlock()
	if full || ctx.Err() != nil {
		return
	}
	// Repos in flight are left out of the listing itself: otherwise one repo with more
	// due jobs than a batch holds would fill every listing while it runs, and no other
	// repo's jobs would be seen until it finished.
	jobs, err := e.Store.ListDueJobsExcluding(ctx, e.now(), e.dueBatch(), busy)
	if err != nil {
		if ctx.Err() == nil {
			e.log().Error("list due jobs", "err", err)
		}
		return
	}
	byRepo := map[string][]model.Job{}
	var order []string
	for _, j := range jobs {
		if _, ok := byRepo[j.Repo]; !ok {
			order = append(order, j.Repo)
		}
		byRepo[j.Repo] = append(byRepo[j.Repo], j)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range order {
		if r.running >= r.workers() {
			return
		}
		if r.inflight[name] || r.finished[name] > snap {
			continue // its next pass lists fresh state
		}
		r.inflight[name] = true
		r.running++
		r.wg.Add(1)
		go func(name string, jobs []model.Job) {
			defer r.wg.Done()
			if n := e.runRepo(ctx, name, jobs); n > 0 {
				e.log().Info("jobs finished", "repo", name, "jobs", n)
			}
			r.mu.Lock()
			r.gen++
			r.finished[name] = r.gen
			delete(r.inflight, name)
			r.running--
			r.mu.Unlock()
			r.Wake()
		}(name, byRepo[name])
	}
}
