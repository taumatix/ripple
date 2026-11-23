package engine

import (
	"context"
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
