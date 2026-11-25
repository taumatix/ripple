package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// osvSummary is osvRange with a chosen summary, as OSV amends a record's text.
func osvSummary(id, pkg, introduced, fixed, summary string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"1.7.5","id":%q,"modified":"2026-11-20T00:00:00Z","summary":%q,`+
		`"affected":[{"package":{"name":%q,"ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":%q},{"fixed":%q}]}]}]}`, id, summary, pkg, introduced, fixed))
}

const roomy = "updates:\n  - ecosystem: gomod\n"

// Entry 126: a record whose text is amended while the PR opened from it is open gets
// its line in the PR body rewritten, so the body does not keep the old summary.
func TestAnAmendedRecordRewritesTheSummaryInTheOpenPRBody(t *testing.T) {
	e, osv := correctedEnv(t, store.NewMemory(), roomy)
	for _, pr := range e.gh.PRs("acme/app") {
		if !strings.Contains(pr.Body, "A flaw in") {
			t.Fatalf("setup: the body must carry the summary:\n%s", pr.Body)
		}
	}
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvSummary("GO-2026-7400", "example.com/one", "0", "1.1.0", "Amended text about one."))
	e.tick()

	for _, pr := range e.gh.PRs("acme/app") {
		switch {
		case strings.Contains(pr.Title, "example.com/one"):
			if !strings.Contains(pr.Body, "Amended text about one.") || strings.Contains(pr.Body, "A flaw in example.com/one") {
				t.Errorf("the body of the PR for one keeps the old summary:\n%s", pr.Body)
			}
			if !strings.Contains(pr.Body, "\nOpened by [ripple]") || !strings.Contains(pr.Body, "[GO-2026-7400](") {
				t.Errorf("the rewrite lost the rest of the body:\n%s", pr.Body)
			}
		default:
			if !strings.Contains(pr.Body, "A flaw in example.com/two") {
				t.Errorf("the other PR's body must not change:\n%s", pr.Body)
			}
		}
	}
}

// A body ripple rewrote is still ripple's own: a later correction closes the PR, which
// it would not if the rewrite counted as a person's edit.
func TestARewrittenBodyDoesNotStopALaterCorrectionClosingThePR(t *testing.T) {
	e, osv := correctedEnv(t, store.NewMemory(), roomy)
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvSummary("GO-2026-7400", "example.com/one", "0", "1.1.0", "Amended text about one."))
	e.tick()
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "1.0.5", "1.1.0"))
	e.tick()
	for _, pr := range e.gh.PRs("acme/app") {
		if strings.Contains(pr.Title, "example.com/one") && !pr.Closed {
			t.Fatalf("a PR whose body ripple rewrote was not closed by the correction: %+v", pr)
		}
	}
}

// A person's edit of the description is theirs: the next amendment leaves it alone.
func TestAnAmendedRecordLeavesAPersonsEditedBodyAlone(t *testing.T) {
	e, osv := correctedEnv(t, store.NewMemory(), roomy)
	var num int
	var before string
	for _, pr := range e.gh.PRs("acme/app") {
		if strings.Contains(pr.Title, "example.com/one") {
			num, before = pr.Number, pr.Body
		}
	}
	e.gh.HumanEditBody("acme/app", num)
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvSummary("GO-2026-7400", "example.com/one", "0", "1.1.0", "Amended text about one."))
	e.tick()
	for _, pr := range e.gh.PRs("acme/app") {
		if pr.Number == num && pr.Body != before {
			t.Errorf("the body a person edited was rewritten:\n%s", pr.Body)
		}
	}
}
