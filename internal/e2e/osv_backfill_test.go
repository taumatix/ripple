package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// Entry 126: a job made before the osv-jobs index existed is not in it, so a corrected
// record could not find it. The watcher walks the repos' open jobs once and indexes the
// ones that carry an advisory note, after which the correction skips the job.
func TestAJobMadeBeforeTheIndexIsFoundByTheBackfill(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		ctx := context.Background()
		e, osv := correctedEnv(t, st, limitOne)
		waiting := "example.com/two"
		if strings.Contains(e.gh.PRs("acme/app")[0].Title, "two") {
			waiting = "example.com/one"
		}
		// Make the repo look like one written by an older release: no index, no backfill mark.
		for _, id := range []string{"GO-2026-7400", "GO-2026-7401"} {
			if err := e.st.SetCursor(ctx, "osv-jobs:"+id, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.st.SetCursor(ctx, "osv-jobs-backfill", ""); err != nil {
			t.Fatal(err)
		}
		e.tick() // the backfill indexes the pending job
		if raw, _ := e.st.GetCursor(ctx, "osv-jobs-backfill"); raw != "done" {
			t.Fatalf("the backfill did not finish: %q", raw)
		}

		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "1.0.5", "1.1.0"))
		osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "1.0.5", "1.1.0"))
		e.tick()
		if j := e.job("acme/app", waiting, "v1.1.0"); j.State != model.JobSkipped {
			t.Errorf("the corrected record did not find the older job: %+v", j)
		}
	})
}
