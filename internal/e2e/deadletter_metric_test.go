package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/store"
)

// Entry 60: the one thing an operator can scrape, GET /replica, reports the dead events,
// both how many this process dead-lettered (a counter to alert a rise of) and how many the
// store holds now (which falls when one is requeued).
func TestReplicaStatusReportsDeadEvents(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{queue: func(q *app.Queue) {
			q.Backoff = func(int) time.Duration { return time.Millisecond }
			q.Rescan = 20 * time.Millisecond
		}})
		status := func() (lettered int64, dead *int) {
			t.Helper()
			resp, err := http.Get(strings.TrimSuffix(e.hook, "/webhook") + "/replica")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var out struct {
				Lettered int64 `json:"events_dead_lettered"`
				Dead     *int  `json:"events_dead"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			return out.Lettered, out.Dead
		}
		if l, d := status(); l != 0 || d == nil || *d != 0 {
			t.Fatalf("before any failure: lettered %d dead %v, want 0 and 0", l, d)
		}
		if code := e.deliver("installation", map[string]any{"action": "created", "installation": map[string]any{"id": 7}, "repositories": repoList("acme/late")}, ""); code != http.StatusAccepted {
			t.Fatalf("installation webhook: %d", code)
		}
		var id string
		e.eventually("event dead-lettered", func() bool {
			evs, err := st.ListDeadEvents(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(evs) == 1 {
				id = evs[0].ID
			}
			return len(evs) == 1
		})
		if l, d := status(); l != 1 || d == nil || *d != 1 {
			t.Fatalf("after a dead letter: lettered %d dead %v, want 1 and 1", l, d)
		}
		e.gh.AddRepo("acme/late", map[string]string{"go.mod": gomod("example.com/late", "example.com/lib v1.0.0")})
		if ok, err := st.RequeueDeadEvent(context.Background(), id); err != nil || !ok {
			t.Fatalf("requeue: %v %v", ok, err)
		}
		e.waitRepo("acme/late")
		if l, d := status(); l != 1 || d == nil || *d != 0 {
			t.Fatalf("after the requeue: lettered %d dead %v, want 1 (a counter) and 0", l, d)
		}
	})
}
