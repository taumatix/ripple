package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/metrics"
	"github.com/taumatix/ripple/internal/store"
)

// EnableMetrics makes Handler serve GET /metrics in the Prometheus text format and time every
// request it serves (RIPPLE_METRICS; off by default). A non-empty token is required as a bearer
// credential. Call it before Handler.
func (a *App) EnableMetrics(token string) {
	r := metrics.New(token)
	r.Counter("ripple_events_dead_lettered_total", "Events this process gave up on since it started.",
		func() float64 {
			if a.Queue == nil {
				return 0
			}
			return float64(a.Queue.DeadLettered())
		})
	r.Gauge("ripple_events_dead", "Dead events the store holds now (every replica's).", func() float64 {
		if a.Engine == nil || a.Engine.Store == nil {
			return 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dead, err := a.Engine.Store.ListDeadEvents(ctx)
		if err != nil {
			return 0
		}
		return float64(len(dead))
	})
	r.Counter("ripple_lease_takeovers_total", "Leases this process took over from a holder that died or stalled.",
		func() float64 { return float64(store.LeaseTakeovers()) })
	a.metrics = r
	a.httpLatency = r.Histogram("ripple_http_request_duration_seconds", "Time to serve one HTTP request, by route.", "route",
		[]float64{0.005, 0.025, 0.1, 0.5, 1, 5, 30})
}

// metricsRoute names a request's route for the latency histogram: a fixed, small set so the
// label cannot grow with what a client sends.
func metricsRoute(r *http.Request) string {
	switch p := r.URL.Path; {
	case p == "/webhook" || strings.HasPrefix(p, "/webhook/"):
		return "webhook"
	case p == "/healthz", p == "/replica", p == "/metrics":
		return p[1:]
	}
	return "other"
}
