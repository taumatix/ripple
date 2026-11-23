package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// Queue defaults; see config.ServerEnv for the matching variables.
const (
	DefaultEventWorkers             = 4
	DefaultEventWorkersPerInst      = 2
	DefaultEventBacklog             = 10_000
	DefaultEventInstallationBacklog = 1_000
	DefaultEventMaxAttempts         = 3
	// DefaultEventDeadRetention is how long a dead-lettered event is kept before it is purged.
	DefaultEventDeadRetention = 30 * 24 * time.Hour
	// DefaultEventRescan is how often the queue looks for events that appeared in the store
	// without passing through Add (a requeue by `ripple events requeue`).
	DefaultEventRescan = 30 * time.Second
	purgeEvery         = time.Hour
)

// ErrBacklogFull means the persisted backlog is at its cap; the receiver answers 503.
var ErrBacklogFull = errors.New("event backlog full")

// Queue is the durable webhook event queue. Every event is persisted before Add
// returns and deleted once handled, so a restart resumes where it stopped. An event that
// fails MaxAttempts times is not deleted but kept dead, with its last error, for an operator
// to list and requeue (`ripple events`), and purged after DeadRetention. Dead events are not
// work, so they do not count against the backlog caps. Workers
// are shared fairly: installations take turns, each runs at most PerInstallation
// events at once and all of them at most Workers, so one noisy installation cannot
// starve the others. Events of one repo run one at a time, in the order received.
type Queue struct {
	Store   store.Store
	Process func(ctx context.Context, ev model.Event) error
	Log     *slog.Logger

	// Workers bounds events handled at once; default DefaultEventWorkers.
	Workers int
	// PerInstallation bounds one installation's events handled at once; default DefaultEventWorkersPerInst.
	PerInstallation int
	// Backlog caps the queued events (handled or waiting) over all installations, and
	// InstallationBacklog those of one installation; past either, Add refuses with
	// ErrBacklogFull and persists nothing. Defaults DefaultEventBacklog and
	// DefaultEventInstallationBacklog.
	Backlog, InstallationBacklog int
	// MaxAttempts is how often a failing event is tried before it is logged and
	// dead-lettered; default DefaultEventMaxAttempts.
	MaxAttempts int
	// DeadRetention is how long a dead event is kept; default DefaultEventDeadRetention.
	DeadRetention time.Duration
	// Rescan is how often events requeued through the store are picked up; default
	// DefaultEventRescan.
	Rescan time.Duration
	// Backoff is the delay before retry number attempt; default attempt * 30s.
	Backoff func(attempt int) time.Duration
	Now     func() time.Time

	admit sync.RWMutex // Add holds it shared across persist+insert; loading holds it exclusively.

	mu       sync.Mutex
	ready    bool
	byInst   map[int64][]*entry
	ring     []int64 // installations with queued events, in turn order
	turn     int
	running  int
	runInst  map[int64]int
	busyRepo map[string]bool
	total    int
	lastSeq  int64
	wake     chan struct{}
	wg       sync.WaitGroup

	lastPurge time.Time // Run's goroutine only
}

type entry struct {
	ev      model.Event
	running bool
}

func (q *Queue) init() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.wake != nil {
		return
	}
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&q.Workers, DefaultEventWorkers)
	def(&q.PerInstallation, DefaultEventWorkersPerInst)
	def(&q.Backlog, DefaultEventBacklog)
	def(&q.InstallationBacklog, DefaultEventInstallationBacklog)
	def(&q.MaxAttempts, DefaultEventMaxAttempts)
	if q.DeadRetention <= 0 {
		q.DeadRetention = DefaultEventDeadRetention
	}
	if q.Rescan <= 0 {
		q.Rescan = DefaultEventRescan
	}
	if q.Backoff == nil {
		q.Backoff = func(a int) time.Duration { return time.Duration(a) * 30 * time.Second }
	}
	if q.Now == nil {
		q.Now = time.Now
	}
	if q.Log == nil {
		q.Log = slog.New(slog.DiscardHandler)
	}
	q.byInst, q.runInst, q.busyRepo = map[int64][]*entry{}, map[int64]int{}, map[string]bool{}
	q.wake = make(chan struct{}, 1)
}

func (q *Queue) loaded() bool { q.mu.Lock(); defer q.mu.Unlock(); return q.ready }

// Len is the number of queued events, including those being handled.
func (q *Queue) Len() int { q.mu.Lock(); defer q.mu.Unlock(); return q.total }

func (q *Queue) poke() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Add persists evs and queues them, assigning Seq in argument order. Events whose ID
// is already stored are skipped (a redelivery of a queued delivery). Before Run has
// loaded the store, events are only persisted; the load picks them up.
func (q *Queue) Add(ctx context.Context, evs ...model.Event) error {
	q.init()
	q.admit.RLock()
	defer q.admit.RUnlock()
	q.mu.Lock()
	if q.ready {
		per := map[int64]int{}
		for _, e := range evs {
			per[e.InstallationID]++
		}
		if q.total+len(evs) > q.Backlog {
			q.mu.Unlock()
			return fmt.Errorf("%w: %d queued, cap %d", ErrBacklogFull, q.total, q.Backlog)
		}
		for inst, n := range per {
			if have := len(q.byInst[inst]); have+n > q.InstallationBacklog {
				q.mu.Unlock()
				return fmt.Errorf("%w: installation %d has %d queued, cap %d", ErrBacklogFull, inst, have, q.InstallationBacklog)
			}
		}
		q.total += len(evs) // reserved; released below for events not queued
	}
	for i := range evs {
		q.lastSeq = max(q.lastSeq+1, q.Now().UnixNano())
		evs[i].Seq = q.lastSeq
	}
	ready := q.ready
	q.mu.Unlock()

	var added []model.Event
	var err error
	for _, e := range evs {
		ok, perr := q.Store.EnqueueEvent(ctx, e)
		if perr != nil {
			err = fmt.Errorf("persist event: %w", perr)
			break
		}
		if ok {
			added = append(added, e)
		}
	}
	if !ready {
		return err
	}
	q.mu.Lock()
	q.total -= len(evs) - len(added)
	for _, e := range added {
		q.push(e)
	}
	q.mu.Unlock()
	q.poke()
	return err
}

// push appends to an installation's queue; q.mu held, total already counted.
func (q *Queue) push(e model.Event) {
	inst := e.InstallationID
	if len(q.byInst[inst]) == 0 {
		q.ring = append(q.ring, inst)
	}
	q.byInst[inst] = append(q.byInst[inst], &entry{ev: e})
}

func (q *Queue) load(ctx context.Context) error {
	q.admit.Lock()
	defer q.admit.Unlock()
	evs, err := q.Store.ListEvents(ctx)
	if err != nil {
		return fmt.Errorf("load queued events: %w", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range evs {
		q.push(e)
		q.lastSeq = max(q.lastSeq, e.Seq)
	}
	q.total = len(evs)
	q.ready = true
	if len(evs) > 0 {
		q.Log.Info("resuming queued events", "events", len(evs))
	}
	return nil
}

// Run loads the persisted backlog and handles events until ctx ends, then waits for
// the events in flight. An event interrupted by shutdown stays stored and is handled
// again after the restart.
func (q *Queue) Run(ctx context.Context) error {
	q.init()
	for {
		err := q.load(ctx)
		if err == nil {
			break
		}
		q.Log.Error("event queue", "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	rescan := time.NewTicker(q.Rescan)
	defer rescan.Stop()
	q.purgeDead(ctx)
	for {
		next := q.dispatch(ctx)
		if !next.IsZero() {
			timer.Reset(max(next.Sub(q.Now()), time.Millisecond))
		}
		select {
		case <-ctx.Done():
			q.wg.Wait()
			return nil
		case <-q.wake:
		case <-timer.C:
		case <-rescan.C:
			q.rescan(ctx)
			q.purgeDead(ctx)
		}
	}
}

// rescan queues the live events in the store that the queue does not hold: those an
// operator requeued, possibly from another process. It excludes Add (as load does) so an
// event persisted by Add but not yet queued is not queued twice, and holds q.mu across the
// listing so an event deleted by a finishing worker is still held in memory when seen.
func (q *Queue) rescan(ctx context.Context) {
	q.admit.Lock()
	defer q.admit.Unlock()
	q.mu.Lock()
	defer q.mu.Unlock()
	evs, err := q.Store.ListEvents(ctx)
	if err != nil {
		if ctx.Err() == nil {
			q.Log.Error("rescan queued events", "err", err)
		}
		return
	}
	known := map[string]bool{}
	for _, list := range q.byInst {
		for _, c := range list {
			known[c.ev.ID] = true
		}
	}
	n := 0
	for _, e := range evs {
		if known[e.ID] {
			continue
		}
		q.push(e)
		q.total++
		q.lastSeq = max(q.lastSeq, e.Seq)
		n++
	}
	if n > 0 {
		q.Log.Info("queued events picked up from the store", "events", n)
		q.poke()
	}
}

// purgeDead deletes the dead events older than DeadRetention, at most once an hour.
func (q *Queue) purgeDead(ctx context.Context) {
	now := q.Now()
	if !q.lastPurge.IsZero() && now.Sub(q.lastPurge) < purgeEvery {
		return
	}
	q.lastPurge = now
	n, err := q.Store.PurgeDeadEvents(ctx, now.Add(-q.DeadRetention))
	switch {
	case err != nil && ctx.Err() == nil:
		q.Log.Error("purge dead events", "err", err)
	case n > 0:
		q.Log.Info("purged dead events", "events", n, "older_than", q.DeadRetention.String())
	}
}

// dispatch starts every event it may, installations taking turns one event at a
// time, and returns when the earliest deferred event becomes due (zero if none).
func (q *Queue) dispatch(ctx context.Context) time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.Now()
	var next time.Time
	for started := true; started && q.running < q.Workers && ctx.Err() == nil; {
		started = false
		n := len(q.ring)
		for i := 0; i < n && q.running < q.Workers; i++ {
			inst := q.ring[(q.turn+i)%n]
			e, due := q.eligible(inst, now)
			if !due.IsZero() && (next.IsZero() || due.Before(next)) {
				next = due
			}
			if e == nil {
				continue
			}
			q.start(ctx, e)
			started = true
		}
		if n > 0 {
			q.turn = (q.turn + 1) % n
		}
	}
	return next
}

// eligible returns the oldest event of inst that may start now: its repo is not
// busy and no older event of the same repo is waiting. due is the earliest NotBefore
// of a deferred event it passed over.
func (q *Queue) eligible(inst int64, now time.Time) (e *entry, due time.Time) {
	if q.runInst[inst] >= q.PerInstallation {
		return nil, time.Time{}
	}
	blocked := map[string]bool{}
	for _, c := range q.byInst[inst] {
		repo := c.ev.Repo
		if blocked[repo] {
			continue
		}
		if c.running || q.busyRepo[repo] || c.ev.NotBefore.After(now) {
			if !c.running && c.ev.NotBefore.After(now) && (due.IsZero() || c.ev.NotBefore.Before(due)) {
				due = c.ev.NotBefore
			}
			blocked[repo] = true
			continue
		}
		return c, due
	}
	return nil, due
}

// start runs e in its own goroutine; q.mu held.
func (q *Queue) start(ctx context.Context, e *entry) {
	e.running = true
	q.running++
	q.runInst[e.ev.InstallationID]++
	q.busyRepo[e.ev.Repo] = true
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		err := q.Process(ctx, e.ev)
		q.finish(ctx, e, err)
	}()
}

func (q *Queue) finish(ctx context.Context, e *entry, err error) {
	ev := e.ev
	remove := false
	switch {
	case ctx.Err() != nil:
		// Shutdown: leave the event stored for the next start.
	case err == nil:
		remove = true
	default:
		now := q.Now()
		ev.Attempts++
		ev.LastError, ev.LastAttemptAt = err.Error(), now
		if ev.Attempts >= q.MaxAttempts {
			// Kept, not deleted: dead, with its error and times, until requeued or purged.
			// If persisting that fails the event stays live in the store and is tried
			// again after a restart, which is the at-least-once side to err on.
			ev.DeadAt = now
			q.Log.Error("event failed; dead-lettered", "event", ev.ID, "kind", ev.Kind, "repo", ev.Repo, "attempts", ev.Attempts, "err", err)
			if uerr := q.Store.UpdateEvent(context.WithoutCancel(ctx), ev); uerr != nil {
				q.Log.Error("persist dead event", "event", ev.ID, "err", uerr)
			}
			q.mu.Lock()
			defer q.mu.Unlock()
			e.running = false
			e.ev = ev
			q.running--
			q.runInst[ev.InstallationID]--
			delete(q.busyRepo, ev.Repo)
			q.drop(e)
			q.poke()
			return
		}
		ev.NotBefore = now.Add(q.Backoff(ev.Attempts))
		q.Log.Warn("event failed; will retry", "event", ev.ID, "kind", ev.Kind, "repo", ev.Repo, "attempt", ev.Attempts, "retry_at", ev.NotBefore, "err", err)
		if uerr := q.Store.UpdateEvent(context.WithoutCancel(ctx), ev); uerr != nil {
			q.Log.Error("persist event retry", "event", ev.ID, "err", uerr)
		}
	}
	if remove {
		// At least once: if this delete fails the event is handled again after a restart.
		if derr := q.Store.DeleteEvent(context.WithoutCancel(ctx), ev.ID); derr != nil {
			q.Log.Error("delete handled event", "event", ev.ID, "err", derr)
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e.running = false
	e.ev = ev
	q.running--
	q.runInst[ev.InstallationID]--
	delete(q.busyRepo, ev.Repo)
	if remove {
		q.drop(e)
	}
	q.poke()
}

// drop removes e from its installation's queue; q.mu held.
func (q *Queue) drop(e *entry) {
	inst := e.ev.InstallationID
	list := q.byInst[inst]
	for i, c := range list {
		if c == e {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	q.total--
	if len(list) > 0 {
		q.byInst[inst] = list
		return
	}
	delete(q.byInst, inst)
	delete(q.runInst, inst)
	for i, id := range q.ring {
		if id == inst {
			q.ring = append(q.ring[:i], q.ring[i+1:]...)
			break
		}
	}
	if len(q.ring) > 0 {
		q.turn %= len(q.ring)
	} else {
		q.turn = 0
	}
}
