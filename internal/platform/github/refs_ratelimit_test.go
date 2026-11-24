package github_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

func throttledRefs(t *testing.T, attempts int) (*github.Client, model.Repo, *testutil.GitHub, *[]time.Duration) {
	t.Helper()
	_, via, gh := newRefsFixture(t)
	gh.Commit("up/action", map[string]string{"x": "1"})
	gh.Tag("up/action", "v1.0.0")
	var waits []time.Duration
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir(),
		Retry: httpretry.Policy{MaxAttempts: attempts, Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}}}
	return c, via, gh, &waits
}

// A 429 with Retry-After on a tag read is waited out for the time named, then read.
func TestTagReadsWaitOutA429(t *testing.T) {
	c, via, gh, waits := throttledRefs(t, 0)
	gh.Refuse("/tags", 1, 429, "7")
	gh.Refuse("/git/ref/tags/", 1, 429, "2")
	gh.Refuse("/releases/tags/", 1, 429, "3")
	gh.Release("up/action", "v1.0.0", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	if tags, err := c.ListTags(ctx, via, "up/action"); err != nil || len(tags) != 1 {
		t.Fatalf("ListTags = %v, %v", tags, err)
	}
	if sha, err := c.TagCommit(ctx, via, "up/action", "v1.0.0"); err != nil || sha == "" {
		t.Fatalf("TagCommit = %q, %v", sha, err)
	}
	if _, ok, err := c.ReleaseOf(ctx, via, "up/action", "v1.0.0"); err != nil || !ok {
		t.Fatalf("ReleaseOf ok=%v err=%v", ok, err)
	}
	want := []time.Duration{7 * time.Second, 2 * time.Second, 3 * time.Second}
	if len(*waits) != 3 || (*waits)[0] != want[0] || (*waits)[1] != want[1] || (*waits)[2] != want[2] || gh.Refused() != 3 {
		t.Fatalf("waits %v refused %d, want %v", *waits, gh.Refused(), want)
	}
}

// A secondary limit arrives as a 403 with Retry-After; a primary one as a 403 with
// X-RateLimit-Remaining 0 and X-RateLimit-Reset. Both are waited out, the second until the reset.
func TestTagReadsWaitOutA403SecondaryAndPrimaryLimit(t *testing.T) {
	c, via, gh, waits := throttledRefs(t, 0)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	gh.Refuse("/tags", 1, 403, "5")
	gh.RefuseWithHeaders("/git/ref/tags/", 1, 403, map[string]string{
		"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(now.Add(9*time.Second).Unix(), 10)})
	ctx := context.Background()
	if _, err := c.ListTags(ctx, via, "up/action"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TagCommit(ctx, via, "up/action", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 2 || (*waits)[0] != 5*time.Second || (*waits)[1] != 9*time.Second {
		t.Fatalf("waits %v", *waits)
	}
}

// A host that stays limited, or asks for more than the cap, is given up on with an error that is
// both a RateLimitError (the host is held back) and a LimitError (it names the limit).
func TestTagReadGivesUpNamingTheLimitAndHoldsTheHostBack(t *testing.T) {
	for name, retryAfter := range map[string]string{"attempts": "1", "max wait": "3600"} {
		t.Run(name, func(t *testing.T) {
			c, via, gh, waits := throttledRefs(t, 2)
			gh.Refuse("/tags", -1, 429, retryAfter)
			_, err := c.ListTags(context.Background(), via, "up/action")
			var le *httpretry.LimitError
			var rl *platform.RateLimitError
			if !errors.As(err, &le) || !errors.As(err, &rl) {
				t.Fatalf("err = %v, want a LimitError that is also a RateLimitError", err)
			}
			if le.Limit == "" || c.RateLimitedUntil(via).IsZero() {
				t.Errorf("limit %q, held until %v", le.Limit, c.RateLimitedUntil(via))
			}
			if name == "attempts" && (len(*waits) != 1 || gh.Refused() != 2) {
				t.Errorf("waits %v refused %d: want 2 requests and 1 wait", *waits, gh.Refused())
			}
			if name == "max wait" && (len(*waits) != 0 || gh.Refused() != 1) {
				t.Errorf("waits %v refused %d: a wait over the cap must not be slept", *waits, gh.Refused())
			}
		})
	}
}

// The wait ends with the context.
func TestTagReadWaitEndsWithContext(t *testing.T) {
	c, via, gh, _ := throttledRefs(t, 0)
	gh.Refuse("/tags", -1, 429, "5")
	ctx, cancel := context.WithCancel(context.Background())
	c.Retry.Sleep = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	_, err := c.ListTags(ctx, via, "up/action")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// A write is never repeated: a 429 on one is the same RateLimitError as before.
func TestWritesAreNotRetried(t *testing.T) {
	c, via, gh, waits := throttledRefs(t, 0)
	gh.Refuse("/pulls", -1, 429, "1")
	_, err := c.CreatePR(context.Background(), via, platform.PRSpec{Title: "t", Head: "h", Base: "main"})
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || len(*waits) != 0 || gh.Refused() != 1 {
		t.Fatalf("err %v waits %v refused %d", err, *waits, gh.Refused())
	}
}
