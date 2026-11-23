package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/store"
)

// An event that always fails is retried, kept dead with its error rather than deleted,
// listed, and handled once after the cause is fixed and it is requeued.
func TestDeadLetterThenRequeue(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{queue: func(q *app.Queue) {
			q.Backoff = func(int) time.Duration { return time.Millisecond }
			q.Rescan = 20 * time.Millisecond
		}})
		// The installation names a repo GitHub does not (yet) serve: registering it fails.
		if code := e.deliver("installation", map[string]any{"action": "created", "installation": map[string]any{"id": 7}, "repositories": repoList("acme/late")}, ""); code != http.StatusAccepted {
			t.Fatalf("installation webhook: %d", code)
		}
		var dead []string
		e.eventually("event dead-lettered", func() bool {
			evs, err := st.ListDeadEvents(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			dead = nil
			for _, ev := range evs {
				dead = append(dead, ev.ID)
			}
			return len(evs) == 1
		})
		evs, _ := st.ListDeadEvents(context.Background())
		if ev := evs[0]; ev.Attempts != 3 || ev.LastError == "" || ev.DeadAt.IsZero() || ev.LastAttemptAt.IsZero() || ev.Repo != "acme/late" {
			t.Fatalf("dead event %+v", ev)
		}
		if n := e.queued(); n != 0 {
			t.Fatalf("%d live events, want 0: a dead event is not queued", n)
		}
		if e.registered("acme/late") {
			t.Fatal("repo registered although it failed")
		}
		failed := e.scans("acme/late")
		if failed != 3 {
			t.Fatalf("repo scanned %d times before dying, want 3", failed)
		}

		// Cause fixed; the operator requeues.
		e.gh.AddRepo("acme/late", map[string]string{"go.mod": gomod("example.com/late", "example.com/lib v1.0.0")})
		ok, err := st.RequeueDeadEvent(context.Background(), dead[0])
		if err != nil || !ok {
			t.Fatalf("requeue: %v %v", ok, err)
		}
		e.waitRepo("acme/late")
		e.eventually("requeued event gone", func() bool {
			l, _ := st.ListDeadEvents(context.Background())
			return e.queued() == 0 && len(l) == 0
		})
		if n := e.scans("acme/late"); n != failed+1 {
			t.Fatalf("repo scanned %d times, want %d: handled once after the requeue", n, failed+1)
		}
	})
}
