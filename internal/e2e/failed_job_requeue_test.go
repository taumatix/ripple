package e2e

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// A job ripple gave up on stays failed however long the server runs; `ripple events
// requeue-job` (app.EventsRequeueJob, over the same store) puts it back and the running
// server opens the pull request on its next tick.
func TestFailedJobIsRequeuedFromTheOperatorCommand(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
		e.gh.AddRepo(repo, map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
		start(t, e, repo)
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		j := e.job(repo, "example.com/lib", "v1.1.0")
		j.State, j.Attempts, j.Reason, j.PRURL = model.JobFailed, 5, "gave up", ""
		if err := st.UpdateJob(context.Background(), j); err != nil {
			t.Fatal(err)
		}
		e.tick()
		if got := e.job(repo, "example.com/lib", "v1.1.0"); got.State != model.JobFailed {
			t.Fatalf("a failed job must stay failed on its own: %+v", got)
		}

		var out bytes.Buffer
		if err := app.EventsFailedJobs(context.Background(), st, repo, &out); err != nil || !bytes.Contains(out.Bytes(), []byte(j.Key)) {
			t.Fatalf("list: %v %q", err, out.String())
		}
		if err := app.EventsRequeueJob(context.Background(), st, e.clock.Now(), j.Key, &out); err != nil {
			t.Fatal(err)
		}
		e.tick()
		if got := e.job(repo, "example.com/lib", "v1.1.0"); got.State != model.JobDone || got.Attempts != 0 {
			t.Fatalf("job after requeue and a tick: %+v", got)
		}
	})
}
