package e2e

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// Entry 94: the job remembers the head commit and the body of the pull request ripple opened,
// so a later pass can tell that a person edited either. Driven through a real scan, the fake
// host and every store the suite runs.
func TestOpenedPRIsRecordedOnItsJob(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
		e.gh.AddRepo(repo, map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
		start(t, e, repo)
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()

		prs := e.gh.PRs(repo)
		if len(prs) != 1 {
			t.Fatalf("want one PR, got %+v", prs)
		}
		j := e.job(repo, "example.com/lib", "v1.1.0")
		if j.State != model.JobDone {
			t.Fatalf("job: %+v", j)
		}
		if want := e.gh.HeadSHA(repo, prs[0].Head); want == "" || j.HeadSHA != want {
			t.Fatalf("recorded head %q, the host's branch is at %q", j.HeadSHA, want)
		}
		if want := model.BodyHash(prs[0].Body); j.BodyHash != want {
			t.Fatalf("recorded body hash %q, want %q (body %q)", j.BodyHash, want, prs[0].Body)
		}
	})
}

func TestBodyHashIgnoresLineEndingsAndTrailingSpace(t *testing.T) {
	if model.BodyHash("a\r\nb\r\n") != model.BodyHash("a\nb") {
		t.Fatal("CRLF and the final newline must not change the hash")
	}
	if model.BodyHash("a\nb") == model.BodyHash("a\nc") {
		t.Fatal("different bodies must hash differently")
	}
}
