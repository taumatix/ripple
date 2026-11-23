package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	// Owner names this replica in the leases it takes on events; empty picks a random id.
	// LeaseTTL is how long a claim on an event lasts without renewal, i.e. how long a dead
	// replica's event waits before another retakes it; default store-side 30s.
	Owner    string
	LeaseTTL time.Duration

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
	// hold keeps the event unstarted until then: another replica claimed it.
	hold time.Time
}

// eventLease names the store lease that makes one replica at a time handle an event.
func eventLease(id string) string { return "event:" + id }

// DefaultEventLeaseTTL is how long a replica's claim on an event lasts without renewal.
const DefaultEventLeaseTTL = 30 * time.Second

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
	if q.LeaseTTL <= 0 {
		q.LeaseTTL = DefaultEventLeaseTTL
	}
	if q.Owner == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		q.Owner = hex.EncodeToString(b)
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
		nb := c.ev.NotBefore
		if c.hold.After(nb) {
			nb = c.hold
		}
		if c.running || q.busyRepo[repo] || nb.After(now) {
			if !c.running && nb.After(now) && (due.IsZero() || nb.Before(due)) {
				due = nb
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
		held, ev, state := q.claim(ctx, e.ev)
		switch state {
		case claimedElsewhere:
			q.standDown(e, ev, q.Now().Add(q.LeaseTTL/3), false)
			return
		case claimGone:
			q.standDown(e, ev, time.Time{}, true)
			return
		case claimDeferred:
			q.standDown(e, ev, time.Time{}, false)
			return
		case claimBehind:
			q.standDown(e, ev, q.Now().Add(q.LeaseTTL/3), false)
			return
		}
		defer held.Release()
		err := q.Process(held.Ctx, ev)
		if errors.Is(context.Cause(held.Ctx), store.ErrLeaseLost) && ctx.Err() == nil {
			// Another replica owns the event now; its outcome is the event's.
			q.Log.Warn("event lease lost; leaving the event to its new holder", "event", ev.ID, "repo", ev.Repo)
			q.standDown(e, ev, q.Now().Add(q.LeaseTTL/3), false)
			return
		}
		q.mu.Lock()
		e.ev = ev
		q.mu.Unlock()
		q.finish(ctx, e, held, err)
	}()
}

type claimState int

const (
	claimed claimState = iota
	claimedElsewhere
	claimGone     // the event no longer exists: another replica handled it
	claimDeferred // the event is not due, or dead, by the store's account
	claimBehind   // an older live event of the same repo is still in the store
)

// claim takes the event's lease and re-reads the event under it, so what runs is the
// store's current state (another replica may have retried, deferred, dead-lettered or
// finished it since this replica loaded it).
func (q *Queue) claim(ctx context.Context, ev model.Event) (*store.Held, model.Event, claimState) {
	held, err := store.Hold(ctx, q.Store, q.Log, eventLease(ev.ID), q.Owner, q.LeaseTTL)
	if err != nil {
		if ctx.Err() == nil {
			q.Log.Error("claim event", "event", ev.ID, "err", err)
		}
		return nil, ev, claimedElsewhere
	}
	if held == nil {
		return nil, ev, claimedElsewhere
	}
	fresh, ok, err := q.Store.GetEvent(ctx, ev.ID)
	switch {
	case err != nil:
		held.Release()
		if ctx.Err() == nil {
			q.Log.Error("read claimed event", "event", ev.ID, "err", err)
		}
		return nil, ev, claimedElsewhere
	case !ok:
		held.Release()
		return nil, ev, claimGone
	case fresh.Dead():
		held.Release()
		return nil, fresh, claimGone
	case fresh.NotBefore.After(q.Now()):
		held.Release()
		return nil, fresh, claimDeferred
	}
	// eligible only orders the events this replica has loaded; an older event of the repo
	// that another replica received is in the store and not here (until the rescan), so ask.
	older, err := q.Store.HasOlderEvent(ctx, fresh)
	switch {
	case err != nil:
		held.Release()
		if ctx.Err() == nil {
			q.Log.Error("check for an older event of the repo", "event", ev.ID, "repo", ev.Repo, "err", err)
		}
		return nil, fresh, claimedElsewhere
	case older:
		held.Release()
		return nil, fresh, claimBehind
	}
	return held, fresh, claimed
}

// standDown returns an event that was not handled here to the queue: held until a time,
// or dropped when it is gone from the store.
func (q *Queue) standDown(e *entry, ev model.Event, hold time.Time, drop bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e.running = false
	e.ev, e.hold = ev, hold
	q.running--
	q.runInst[ev.InstallationID]--
	delete(q.busyRepo, ev.Repo)
	if drop {
		q.drop(e)
	}
	q.poke()
}

func (q *Queue) finish(ctx context.Context, e *entry, held *store.Held, err error) {
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
			if uerr := q.updateEvent(ctx, held, ev); uerr != nil {
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
		if uerr := q.updateEvent(ctx, held, ev); uerr != nil {
			q.Log.Error("persist event retry", "event", ev.ID, "err", uerr)
		}
	}
	if remove {
		// At least once: if this delete fails the event is handled again after a restart.
		if derr := q.deleteEvent(ctx, held, ev.ID); derr != nil {
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

// updateEvent persists an event's outcome under the event lease: the write applies only
// while held is still this replica's claim, so one that stalled past its lease cannot
// overwrite what the new holder decided. A refused write is not an error to retry; the
// new holder's outcome stands.
func (q *Queue) updateEvent(ctx context.Context, held *store.Held, ev model.Event) error {
	err := q.Store.UpdateEventFenced(context.WithoutCancel(ctx), ev, held.Lease())
	if errors.Is(err, store.ErrLeaseLost) {
		q.Log.Warn("event lease lost; its outcome is left to the new holder", "event", ev.ID, "repo", ev.Repo)
		return nil
	}
	return err
}

// deleteEvent removes a handled event under the event lease; see updateEvent.
func (q *Queue) deleteEvent(ctx context.Context, held *store.Held, id string) error {
	err := q.Store.DeleteEventFenced(context.WithoutCancel(ctx), id, held.Lease())
	if errors.Is(err, store.ErrLeaseLost) {
		q.Log.Warn("event lease lost; its outcome is left to the new holder", "event", id)
		return nil
	}
	return err
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
