package httpretry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseRetryAfterSecondsAndDate(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for hdr, want := range map[string]time.Duration{"5": 5 * time.Second, "0": 0, now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second, now.Add(-time.Hour).Format(http.TimeFormat): 0} {
		h := http.Header{"Retry-After": {hdr}}
		if d, ok := ParseRetryAfter(h, now); !ok || d != want {
			t.Errorf("%q: %v %v want %v", hdr, d, ok, want)
		}
	}
	for _, bad := range []string{"", "soon", "-3"} {
		if _, ok := ParseRetryAfter(http.Header{"Retry-After": {bad}}, now); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestWithoutRetryAfterBackoffGrowsAndIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 429) }))
	defer srv.Close()
	var waits []time.Duration
	p := Policy{MaxAttempts: 5, MaxWait: 3 * time.Second, Sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}
	_, err := p.Do(context.Background(), srv.Client(), func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, srv.URL, nil)
	})
	if err == nil || len(waits) != 4 || waits[0] != time.Second || waits[1] != 2*time.Second || waits[2] != 3*time.Second || waits[3] != 3*time.Second {
		t.Fatalf("err=%v waits=%v", err, waits)
	}
}

// Next is the decision Do makes, for a caller with a wider idea of "limited".
func TestNextSleepsThenGivesUp(t *testing.T) {
	var waits []time.Duration
	p := Policy{MaxAttempts: 2, MaxWait: 10 * time.Second, Sleep: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}}
	le := &LimitError{Attempts: 1}
	if err := p.Next(context.Background(), le, 4*time.Second, true); err != nil || le.Waited != 4*time.Second || len(waits) != 1 {
		t.Fatalf("first: err %v waited %v waits %v", err, le.Waited, waits)
	}
	le.Attempts = 2
	if err := p.Next(context.Background(), le, time.Second, true); err != le || le.Limit != "2 attempts" {
		t.Fatalf("second: err %v limit %q", err, le.Limit)
	}
	le = &LimitError{Attempts: 1}
	if err := p.Next(context.Background(), le, time.Minute, true); err != le || len(waits) != 1 {
		t.Fatalf("over the cap must not sleep: %v %v", err, waits)
	}
}
