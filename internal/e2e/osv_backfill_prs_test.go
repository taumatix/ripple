package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// Entry 126, the done job: a job that finished (and opened a PR) before the osv-jobs index
// existed is not listed by any store, so the backfill cannot see it. With
// BackfillOpenPRs the watcher finds it from the host instead: the open ripple/go branch
// names the module and version, the job is read by key and its advisory note decides.
func olderDoneJobEnv(t *testing.T, st store.Store, backfillPRs bool) (*env, string) {
	t.Helper()
	ctx := context.Background()
	e, osv := correctedEnv(t, st, limitOne)
	e.app.Advisories.BackfillOpenPRs = backfillPRs
	for _, id := range []string{"GO-2026-7400", "GO-2026-7401"} {
		if err := e.st.SetCursor(ctx, "osv-jobs:"+id, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []string{"osv-jobs-backfill", "osv-jobs-backfill-prs"} {
		if err := e.st.SetCursor(ctx, c, ""); err != nil {
			t.Fatal(err)
		}
	}
	e.tick() // the backfills run
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "1.0.5", "1.1.0"))
	osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "1.0.5", "1.1.0"))
	e.tick()
	return e, e.gh.PRs("acme/app")[0].Title
}

func TestTheBackfillFindsTheOpenPROfAJobDoneBeforeTheIndex(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, _ := olderDoneJobEnv(t, st, true)
		pr := e.gh.PRs("acme/app")[0]
		if !pr.Closed || !strings.Contains(strings.Join(pr.Comments, "\n"), "corrected") {
			t.Errorf("the older PR must be closed by the correction: closed=%v comments=%q", pr.Closed, pr.Comments)
		}
		if raw, _ := e.st.GetCursor(context.Background(), "osv-jobs-backfill-prs"); raw != "done" {
			t.Errorf("the PR backfill did not finish: %q", raw)
		}
	})
}

// Opt-in: without the setting no host call is made and the older PR is left alone, as before.
func TestWithoutTheOptionAnOlderPRIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, _ := olderDoneJobEnv(t, st, false)
		if pr := e.gh.PRs("acme/app")[0]; pr.Closed {
			t.Errorf("closed without the option: %+v", pr)
		}
		if raw, _ := e.st.GetCursor(context.Background(), "osv-jobs-backfill-prs"); raw != "" {
			t.Errorf("the PR backfill ran: %q", raw)
		}
	})
}
