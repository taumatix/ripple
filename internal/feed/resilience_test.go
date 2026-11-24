package feed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/testutil"
)

// recordingSleep stands in for waiting: it records what a client would have slept.
type recordingSleep struct{ waits []time.Duration }

func (r *recordingSleep) sleep(_ context.Context, d time.Duration) error {
	r.waits = append(r.waits, d)
	return nil
}

func (r *recordingSleep) policy(attempts int) httpretry.Policy {
	return httpretry.Policy{MaxAttempts: attempts, Sleep: r.sleep}
}

func TestGoIndexWaitsOutA429AndReadsTheEntries(t *testing.T) {
	x := testutil.NewIndex(t)
	x.Publish("example.com/a", "v1.0.0", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	x.Refuse("/index", 2, 429, "3")
	rs := &recordingSleep{}
	rels, _, err := (&GoIndex{BaseURL: x.URL, Retry: rs.policy(0)}).Fetch(context.Background(), "")
	if err != nil || len(rels) != 1 || rels[0].Package != "example.com/a" {
		t.Fatalf("rels=%v err=%v", rels, err)
	}
	if len(rs.waits) != 2 || rs.waits[0] != 3*time.Second || rs.waits[1] != 3*time.Second {
		t.Fatalf("should honour Retry-After twice, waited %v", rs.waits)
	}
}

func TestGoIndexGivesUpNamingTheLimit(t *testing.T) {
	x := testutil.NewIndex(t)
	x.Refuse("/index", -1, 429, "3")
	rs := &recordingSleep{}
	_, _, err := (&GoIndex{BaseURL: x.URL, Retry: rs.policy(3)}).Fetch(context.Background(), "")
	var le *httpretry.LimitError
	if !errors.As(err, &le) || le.Status != 429 || le.Attempts != 3 {
		t.Fatalf("want a LimitError after 3 attempts, got %v", err)
	}
	for _, want := range []string{"429", `Retry-After "3"`, "3 attempts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if x.Requests() != 0 || x.Refused() != 3 {
		t.Errorf("served %d refused %d", x.Requests(), x.Refused())
	}
}

func TestGoIndexDoesNotWaitLongerThanTheCap(t *testing.T) {
	x := testutil.NewIndex(t)
	x.Refuse("/index", -1, 429, "3600")
	rs := &recordingSleep{}
	_, _, err := (&GoIndex{BaseURL: x.URL, Retry: rs.policy(5)}).Fetch(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "max wait") || len(rs.waits) != 0 || x.Refused() != 1 {
		t.Fatalf("an hour-long Retry-After must be refused at once: err=%v waits=%v refused=%d", err, rs.waits, x.Refused())
	}
}

func TestGoIndexWaitEndsWithTheContext(t *testing.T) {
	x := testutil.NewIndex(t)
	x.Refuse("/index", -1, 429, "20")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := (&GoIndex{BaseURL: x.URL}).Fetch(ctx, "")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("the wait must stop with the context: err=%v after %s", err, time.Since(start))
	}
}

func TestGoIndexBareUnavailableFailsFast(t *testing.T) {
	x := testutil.NewIndex(t)
	x.Refuse("/index", -1, 503, "")
	rs := &recordingSleep{}
	_, _, err := (&GoIndex{BaseURL: x.URL, Retry: rs.policy(3)}).Fetch(context.Background(), "")
	if err == nil || len(rs.waits) != 0 || x.Refused() != 1 {
		t.Fatalf("a 503 with no Retry-After is an outage, not a limit: err=%v waits=%v refused=%d", err, rs.waits, x.Refused())
	}
}

func publishRet(p *testutil.GoProxy) {
	p.Publish("example.com/m", "v1.0.0", nil)
}

func TestGoProxyLookupWaitsOutA429OnLatest(t *testing.T) {
	p := testutil.NewGoProxy(t)
	publishRet(p)
	p.Refuse("/@latest", 1, 429, "2")
	rs := &recordingSleep{}
	r, ok, err := (&GoProxyLookup{BaseURL: p.URL, Retry: rs.policy(0)}).Latest(context.Background(), "example.com/m")
	if err != nil || !ok || r.Version != "v1.0.0" {
		t.Fatalf("got %+v ok=%v err=%v", r, ok, err)
	}
	if len(rs.waits) != 1 || rs.waits[0] != 2*time.Second {
		t.Fatalf("waits %v", rs.waits)
	}
}

func TestGoProxyLookupWaitsOutA503WithRetryAfterOnTheList(t *testing.T) {
	p := testutil.NewGoProxy(t)
	publishRet(p)
	p.Refuse("/@v/list", 1, 503, "1")
	rs := &recordingSleep{}
	r, ok, err := (&GoProxyLookup{BaseURL: p.URL, Retry: rs.policy(0)}).Latest(context.Background(), "example.com/m")
	if err != nil || !ok || r.Version != "v1.0.0" || len(rs.waits) != 1 {
		t.Fatalf("got %+v ok=%v err=%v waits=%v", r, ok, err, rs.waits)
	}
}

func TestGoProxyLookupGivesUpNamingTheLimit(t *testing.T) {
	p := testutil.NewGoProxy(t)
	publishRet(p)
	p.Refuse("/@latest", -1, 429, "2")
	rs := &recordingSleep{}
	g := &GoProxyLookup{BaseURL: p.URL, Retry: rs.policy(2)}
	_, ok, err := g.Latest(context.Background(), "example.com/m")
	var le *httpretry.LimitError
	if ok || !errors.As(err, &le) || le.Attempts != 2 || !strings.Contains(err.Error(), "example.com/m") {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// A failed lookup is not cached: the next poll asks again.
	if _, _, err := g.Latest(context.Background(), "example.com/m"); err == nil {
		t.Fatal("still limited, still an error")
	}
	if p.Refused() != 4 {
		t.Errorf("an error must not be cached: refused %d", p.Refused())
	}
}

// 180: a module whose @latest (the highest stable version) is retracted by its own go.mod.
func TestGoProxyLookupSkipsARetractedLatestOnTheFake(t *testing.T) {
	p := testutil.NewGoProxy(t)
	p.Publish("example.com/r", "v1.1.0", nil)
	p.Publish("example.com/r", "v1.2.0", map[string]string{"go.mod": "module example.com/r\n\ngo 1.21\n\nretract v1.2.0 // published by mistake\n"})
	r, ok, err := (&GoProxyLookup{BaseURL: p.URL}).Latest(context.Background(), "example.com/r")
	if err != nil || !ok || r.Version != "v1.1.0" {
		t.Fatalf("want v1.1.0, got %+v ok=%v err=%v", r, ok, err)
	}
}

// 180: every release retracted: the module is still known, and what it says is returned
// rather than nothing, so a repo is not told the module vanished.
func TestGoProxyLookupAllRetractedStillAnswers(t *testing.T) {
	p := testutil.NewGoProxy(t)
	p.Publish("example.com/all", "v1.0.0", map[string]string{"go.mod": "module example.com/all\n\ngo 1.21\n\nretract [v1.0.0, v1.0.0]\n"})
	r, ok, err := (&GoProxyLookup{BaseURL: p.URL}).Latest(context.Background(), "example.com/all")
	if err != nil || !ok || r.Version != "v1.0.0" {
		t.Fatalf("got %+v ok=%v err=%v", r, ok, err)
	}
}

// 180: a module with prereleases only is offered its newest prerelease.
func TestGoProxyLookupPrereleaseOnlyOnTheFake(t *testing.T) {
	p := testutil.NewGoProxy(t)
	p.Publish("example.com/pre", "v0.1.0-alpha.1", nil)
	p.Publish("example.com/pre", "v0.1.0-beta.1", nil)
	r, ok, err := (&GoProxyLookup{BaseURL: p.URL}).Latest(context.Background(), "example.com/pre")
	if err != nil || !ok || r.Version != "v0.1.0-beta.1" {
		t.Fatalf("got %+v ok=%v err=%v", r, ok, err)
	}
}
