package npmreg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/npmreg"
	"github.com/taumatix/ripple/internal/testutil"
)

func retrySleeps(attempts int) (httpretry.Policy, *[]time.Duration) {
	var waits []time.Duration
	return httpretry.Policy{MaxAttempts: attempts, Sleep: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}}, &waits
}

// A 429 with Retry-After is waited out for exactly the time the server named, then read.
func TestReadWaitsOutA429(t *testing.T) {
	f := testutil.NewNpmRegistry(t)
	f.Publish("left-pad", "1.0.0")
	f.Publish("left-pad", "1.1.0")
	f.Refuse("left-pad", 1, 429, "7")
	p, waits := retrySleeps(0)
	c := &npmreg.Client{BaseURL: f.URL, Retry: p}
	got, err := c.Packument(context.Background(), "left-pad")
	if err != nil || got.Newest("1.0.0") != "1.1.0" {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if len(*waits) != 1 || (*waits)[0] != 7*time.Second || f.Refused() != 1 {
		t.Fatalf("waits %v refused %d", *waits, f.Refused())
	}
}

// A service that stays limited is an error that names the limit, not a silent empty list.
func TestReadGivesUpNamingTheLimit(t *testing.T) {
	f := testutil.NewNpmRegistry(t)
	f.Publish("left-pad", "1.0.0")
	f.Publish("left-pad", "1.1.0")
	f.Refuse("left-pad", -1, 429, "")
	p, waits := retrySleeps(3)
	c := &npmreg.Client{BaseURL: f.URL, Retry: p}
	_, err := c.Packument(context.Background(), "left-pad")
	var le *httpretry.LimitError
	if !errors.As(err, &le) || le.Status != 429 || le.Attempts != 3 || !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("err=%v", err)
	}
	if len(*waits) != 2 {
		t.Errorf("waits %v", *waits)
	}
}

// The wait ends with the caller's context.
func TestReadWaitEndsWithTheContext(t *testing.T) {
	f := testutil.NewNpmRegistry(t)
	f.Publish("left-pad", "1.0.0")
	f.Publish("left-pad", "1.1.0")
	f.Refuse("left-pad", -1, 429, "20")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (&npmreg.Client{BaseURL: f.URL}).Packument(ctx, "left-pad")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}
