package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
)

func get(t *testing.T, url, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// With metrics on, GET /metrics on the real server reports the dead-letter counters and a
// latency histogram per route; with it off the path does not exist.
func TestMetricsEndpointOverHTTP(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	g.fail["broken"] = 100
	q := &Queue{Store: st, Process: g.process, MaxAttempts: 2, Backoff: func(int) time.Duration { return time.Millisecond }}
	startQueue(t, q)
	a := &App{Engine: &engine.Engine{Store: st}, Queue: q, Log: slog.New(slog.DiscardHandler)}

	off := httptest.NewServer(a.Handler())
	defer off.Close()
	if code, _ := get(t, off.URL+"/metrics", ""); code != http.StatusNotFound {
		t.Fatalf("metrics off: /metrics answered %d, want 404", code)
	}

	a.EnableMetrics("tok")
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	_ = q.Add(context.Background(), ev(1, "broken", "acme/b"))
	eventually(t, "broken dead-lettered", func() bool { return len(dead(t, st)) == 1 })
	get(t, srv.URL+"/healthz", "")

	if code, _ := get(t, srv.URL+"/metrics", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", code)
	}
	code, body := get(t, srv.URL+"/metrics", "tok")
	if code != http.StatusOK {
		t.Fatalf("scrape: %d", code)
	}
	for _, want := range []string{
		"ripple_events_dead_lettered_total 1\n",
		"ripple_events_dead 1\n",
		"ripple_lease_takeovers_total ",
		`ripple_http_request_duration_seconds_count{route="healthz"} 1`,
		`ripple_http_request_duration_seconds_bucket{route="healthz",le="+Inf"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}
