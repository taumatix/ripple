package registry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/testutil"
)

type recordingSleep struct{ waits []time.Duration }

func (r *recordingSleep) policy(attempts int) httpretry.Policy {
	return httpretry.Policy{MaxAttempts: attempts, Sleep: func(_ context.Context, d time.Duration) error {
		r.waits = append(r.waits, d)
		return nil
	}}
}

func pagedRegistry(t *testing.T) (*testutil.Registry, []string) {
	f := testutil.NewRegistry(t)
	var want []string
	for i := 0; i < 7; i++ {
		tag := fmt.Sprintf("1.%d.0", i)
		f.AddTag("org/img", tag)
		want = append(want, tag)
	}
	f.PageSize(3)
	return f, want
}

func countReqs(f *testutil.Registry, sub string) (n int) {
	for _, r := range f.Requests() {
		if strings.Contains(r, sub) {
			n++
		}
	}
	return n
}

// A 429 on the third page of a tag list is waited out and the list carries on from that page:
// the pages already read are not asked for again.
func TestTagsWaitsOutA429MidListWithoutRestarting(t *testing.T) {
	f, want := pagedRegistry(t)
	f.Refuse("last=1.5.0", 1, 429, "5") // the request for page 3
	f.Refuse("last=1.2.0", 1, 429, "5") // and for page 2
	rs := &recordingSleep{}
	c := clientFor(f)
	c.Retry = rs.policy(0)
	got, err := c.Tags(context.Background(), "ghcr.io/org/img")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("tags %v err=%v", got, err)
	}
	if len(rs.waits) != 2 || rs.waits[0] != 5*time.Second {
		t.Fatalf("waits %v", rs.waits)
	}
	// Each of the 3 pages is asked twice (the 401 challenge, then with the token); a list
	// that restarted after a 429 would show more.
	if n := countReqs(f, "GET /v2/org/img/tags/list"); n != 6 {
		t.Errorf("want 6 tag-list requests served, got %d", n)
	}
}

func TestRegistryGivesUpOnA429NamingTheLimit(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("library/nginx", "1.25.0")
	f.Refuse("/tags/list", -1, 429, "")
	rs := &recordingSleep{}
	c := clientFor(f)
	c.Retry = rs.policy(3)
	_, err := c.Tags(context.Background(), "docker.io/library/nginx")
	var le *httpretry.LimitError
	if !errors.As(err, &le) || le.Status != 429 || le.Attempts != 3 {
		t.Fatalf("err=%v", err)
	}
	for _, want := range []string{"rate limited", "429", "3 attempts", "registry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if len(rs.waits) != 2 || rs.waits[0] != time.Second || rs.waits[1] != 2*time.Second {
		t.Errorf("no Retry-After: backoff 1s then 2s, got %v", rs.waits)
	}
}

func TestRegistryWaitsOutA429OnTheTokenEndpoint(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("library/nginx", "1.25.0")
	f.Refuse("/token", 1, 429, "2")
	rs := &recordingSleep{}
	c := clientFor(f)
	c.Retry = rs.policy(0)
	got, err := c.Tags(context.Background(), "docker.io/library/nginx")
	if err != nil || len(got) != 1 || len(rs.waits) != 1 {
		t.Fatalf("got %v err=%v waits=%v", got, err, rs.waits)
	}
}

func TestRegistryWaitsOutA429OnADigest(t *testing.T) {
	f := testutil.NewRegistry(t)
	d := f.AddTag("org/img", "2.0.0")
	f.Refuse("/manifests/", 1, 429, "1")
	rs := &recordingSleep{}
	c := clientFor(f)
	c.Retry = rs.policy(0)
	got, err := c.Digest(context.Background(), "ghcr.io/org/img", "2.0.0")
	if err != nil || got != d || len(rs.waits) != 1 {
		t.Fatalf("got %q err=%v waits=%v", got, err, rs.waits)
	}
}

// A slow tag list does not hold the caller past its context: the deadline wins.
func TestSlowTagListEndsWithTheContext(t *testing.T) {
	f, _ := pagedRegistry(t)
	f.Slow("/tags/list", 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := clientFor(f).Tags(ctx, "ghcr.io/org/img")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestRegistryWaitEndsWithTheContext(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("org/img", "1.0.0")
	f.Refuse("/tags/list", -1, 429, "20")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := clientFor(f).Tags(ctx, "ghcr.io/org/img")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}
