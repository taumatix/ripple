package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, h http.Handler, header ...string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if len(header) == 2 {
		req.Header.Set(header[0], header[1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestExpositionFormat(t *testing.T) {
	r := New("")
	r.Gauge("ripple_events_dead", "Dead events held by the store.", func() float64 { return 3 })
	r.Counter("ripple_events_dead_lettered_total", "Events given up on since start.", func() float64 { return 7 })
	code, body := scrape(t, r.Handler())
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		"# HELP ripple_events_dead Dead events held by the store.\n# TYPE ripple_events_dead gauge\nripple_events_dead 3\n",
		"# TYPE ripple_events_dead_lettered_total counter\nripple_events_dead_lettered_total 7\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestHistogramIsCumulative(t *testing.T) {
	r := New("")
	h := r.Histogram("ripple_http_request_duration_seconds", "Request latency.", "route", []float64{0.1, 1})
	h.Observe("webhook", 50*time.Millisecond)
	h.Observe("webhook", 500*time.Millisecond)
	h.Observe("webhook", 5*time.Second)
	_, body := scrape(t, r.Handler())
	for _, want := range []string{
		`ripple_http_request_duration_seconds_bucket{route="webhook",le="0.1"} 1`,
		`ripple_http_request_duration_seconds_bucket{route="webhook",le="1"} 2`,
		`ripple_http_request_duration_seconds_bucket{route="webhook",le="+Inf"} 3`,
		`ripple_http_request_duration_seconds_count{route="webhook"} 3`,
		`ripple_http_request_duration_seconds_sum{route="webhook"} 5.55`,
		"# TYPE ripple_http_request_duration_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestTokenGuardsTheEndpoint(t *testing.T) {
	r := New("s3cret")
	if code, _ := scrape(t, r.Handler()); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code, _ := scrape(t, r.Handler(), "Authorization", "Bearer wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := scrape(t, r.Handler(), "Authorization", "Bearer s3cret"); code != http.StatusOK {
		t.Errorf("right token: %d", code)
	}
}

func TestMiddlewareTimesTheRoute(t *testing.T) {
	r := New("")
	h := r.Histogram("d", "d.", "route", []float64{1})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(http.ResponseWriter, *http.Request) {})
	srv := Instrument(h, func(req *http.Request) string { return "healthz" }, mux)
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	_, body := scrape(t, r.Handler())
	if !strings.Contains(body, `d_count{route="healthz"} 1`) {
		t.Errorf("not timed:\n%s", body)
	}
}
