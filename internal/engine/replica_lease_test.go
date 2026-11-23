package engine

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

func leaseEngine(t *testing.T) (*Engine, *store.Memory) {
	t.Helper()
	st := store.NewMemory()
	return &Engine{Store: st, Replica: "a", LeaseTTL: 300 * time.Millisecond}, st
}

// holdElsewhere takes the repo's lease as another replica.
func holdElsewhere(t *testing.T, st store.Store, repo string) *store.Held {
	t.Helper()
	h, err := store.Hold(context.Background(), st, nil, LeaseName(repo), "b", 300*time.Millisecond)
	if err != nil || h == nil {
		t.Fatalf("other replica could not take the lease: %v %v", h, err)
	}
	return h
}

// Pause, Resume, Remove and Rename change a repo's state, so they wait for the replica that
// is scanning or running it instead of writing underneath it.
func TestRepoMutationsWaitForTheLeaseHolder(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range map[string]func(e *Engine) error{
		"pause":  func(e *Engine) error { return e.Pause(ctx, "o/r") },
		"remove": func(e *Engine) error { return e.Remove(ctx, "o/r") },
		"rename": func(e *Engine) error { return e.Rename(ctx, "o/r", "o/s", 0) },
		"resume": func(e *Engine) error { return e.Resume(ctx, "o/r", 0) },
	} {
		t.Run(name, func(t *testing.T) {
			e, st := leaseEngine(t)
			if err := st.UpsertRepo(ctx, model.Repo{FullName: "o/r"}); err != nil {
				t.Fatal(err)
			}
			if name == "resume" {
				if err := st.SetCursor(ctx, pausedCursor+"o/r", "1"); err != nil {
					t.Fatal(err)
				}
			}
			h := holdElsewhere(t, st, "o/r")
			done := make(chan error, 1)
			go func() { done <- mutate(e) }()
			select {
			case err := <-done:
				t.Fatalf("returned while another replica held the repo (err %v)", err)
			case <-time.After(150 * time.Millisecond):
			}
			h.Release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("after the holder released: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("never ran after the holder released")
			}
			if l, ok, _ := st.AcquireLease(ctx, LeaseName("o/r"), "c", time.Second); !ok {
				t.Fatalf("mutation left the repo lease held: %+v", l)
			}
		})
	}
}

// A replica whose lease lapsed and was taken over cannot write the job row: the fenced
// update is rejected and the new holder's state stands.
func TestJobWritesAreFencedOnTheRepoLease(t *testing.T) {
	ctx := context.Background()
	e, st := leaseEngine(t)
	job := model.Job{Key: "o/r|go|x|v1", Repo: "o/r", State: model.JobPending}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	h, err := store.Hold(ctx, st, nil, LeaseName("o/r"), "a", time.Hour)
	if err != nil || h == nil {
		t.Fatal(err)
	}
	defer h.Release()
	r := &runner{e: e, ctx: ctx, hold: h}

	// Lapse: the lease row goes and another replica takes it, as after a stall.
	if err := st.ReleaseLease(ctx, h.Lease()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.AcquireLease(ctx, LeaseName("o/r"), "b", time.Hour); err != nil || !ok {
		t.Fatalf("takeover: %v %v", ok, err)
	}

	r.finish(job, model.JobDone, "", "https://example/pr/1")
	r.deferJob(job, time.Now().Add(time.Hour), "late")
	r.fail(job, context.DeadlineExceeded)
	got, _, _ := st.GetJob(ctx, job.Key)
	if got.State != model.JobPending || got.PRURL != "" || got.Reason != "" || got.Attempts != 0 {
		t.Fatalf("stale replica changed the job row: %+v", got)
	}
	if r.finished != 0 {
		t.Fatalf("a rejected write counted as finished: %d", r.finished)
	}
}

// usurpingStore hands the lease out and gives it to another replica at once, as if this one
// had stalled just after taking it: the caller holds a lease that is already stale.
type usurpingStore struct {
	*store.Memory
	armed atomic.Bool
}

func (u *usurpingStore) AcquireLease(ctx context.Context, name, owner string, ttl time.Duration) (store.Lease, bool, error) {
	l, ok, err := u.Memory.AcquireLease(ctx, name, owner, ttl)
	if err != nil || !ok || !u.armed.Load() || owner == "usurper" {
		return l, ok, err
	}
	if err := u.Memory.ReleaseLease(ctx, l); err != nil {
		return l, ok, err
	}
	if _, took, err := u.Memory.AcquireLease(ctx, name, "usurper", time.Minute); err != nil || !took {
		return l, ok, fmt.Errorf("usurp: %v %v", took, err)
	}
	return l, ok, nil
}

// The cursor writes of pause, resume, remove and rename run under the repo lease and are
// fenced on it: a replica that lost the lease meanwhile changes nothing.
func TestCursorWritesAreFencedOnTheRepoLease(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		paused bool
		do     func(e *Engine) error
		cursor string // a cursor the stale mutation must not have written
	}{
		"pause":  {false, func(e *Engine) error { return e.Pause(ctx, "o/r") }, pausedCursor + "o/r"},
		"resume": {true, func(e *Engine) error { return e.Resume(ctx, "o/r", 0) }, pausedCursor + "o/r"},
		"remove": {false, func(e *Engine) error { return e.Remove(ctx, "o/r") }, removedCursor + "o/r"},
		"rename": {false, func(e *Engine) error { return e.Rename(ctx, "o/r", "o/s", 0) }, renamedCursor + "o/r"},
	} {
		t.Run(name, func(t *testing.T) {
			st := &usurpingStore{Memory: store.NewMemory()}
			e := &Engine{Store: st, Replica: "a", LeaseTTL: 300 * time.Millisecond}
			if err := st.UpsertRepo(ctx, model.Repo{FullName: "o/r"}); err != nil {
				t.Fatal(err)
			}
			const marker = "set-by-the-other-replica"
			if tc.paused {
				if err := st.SetCursor(ctx, pausedCursor+"o/r", marker); err != nil {
					t.Fatal(err)
				}
			}
			st.armed.Store(true)
			err := tc.do(e)
			if !errors.Is(err, store.ErrLeaseLost) {
				t.Fatalf("a mutation under a stolen lease returned %v, want ErrLeaseLost", err)
			}
			want := ""
			if tc.paused {
				want = marker
			}
			if v, _ := st.GetCursor(ctx, tc.cursor); v != want {
				t.Fatalf("cursor %s = %q after a refused write, want %q", tc.cursor, v, want)
			}
		})
	}
}

// The advisory pass pulls a pending job's NotBefore forward under the repo lease: it
// writes the row only while it still holds it, and not at all once the job is no longer pending.
func TestAdvisoryJobAdvanceIsFenced(t *testing.T) {
	ctx := context.Background()
	later := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sooner := later.Add(-time.Hour)
	setup := func(t *testing.T) (*Engine, *usurpingStore, model.Job) {
		st := &usurpingStore{Memory: store.NewMemory()}
		job := model.Job{Key: "o/r|go|x|v1", Repo: "o/r", State: model.JobPending, NotBefore: later}
		if _, err := st.EnqueueJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		return &Engine{Store: st, Replica: "a", LeaseTTL: 300 * time.Millisecond}, st, job
	}
	t.Run("moves a pending job", func(t *testing.T) {
		e, st, job := setup(t)
		if moved, err := e.advanceJob(ctx, "o/r", job.Key, sooner); err != nil || !moved {
			t.Fatalf("moved=%v err=%v", moved, err)
		}
		if got, _, _ := st.GetJob(ctx, job.Key); !got.NotBefore.Equal(sooner) {
			t.Fatalf("not before %v", got.NotBefore)
		}
	})
	t.Run("stale lease", func(t *testing.T) {
		e, st, job := setup(t)
		st.armed.Store(true)
		if _, err := e.advanceJob(ctx, "o/r", job.Key, sooner); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("err %v, want ErrLeaseLost", err)
		}
		if got, _, _ := st.GetJob(ctx, job.Key); !got.NotBefore.Equal(later) {
			t.Fatalf("a refused write landed: %v", got.NotBefore)
		}
	})
	t.Run("job finished meanwhile", func(t *testing.T) {
		e, st, job := setup(t)
		job.State, job.PRURL = model.JobDone, "https://example.test/pr/1"
		if err := st.UpdateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if moved, err := e.advanceJob(ctx, "o/r", job.Key, sooner); err != nil || moved {
			t.Fatalf("moved=%v err=%v", moved, err)
		}
		if got, _, _ := st.GetJob(ctx, job.Key); got.State != model.JobDone {
			t.Fatalf("a finished job was put back: %+v", got)
		}
	})
}
