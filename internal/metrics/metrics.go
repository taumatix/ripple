// Package metrics is a small Prometheus text-format (0.0.4) exposition: gauges and counters
// read from a function at scrape time, and histograms with one label. It has no dependency
// and no global state; the server mounts a Registry's Handler on GET /metrics only when
// RIPPLE_METRICS is on.
package metrics

import (
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registry holds the metrics one server exposes.
type Registry struct {
	token string

	mu      sync.Mutex
	metrics []writer // in registration order
}

type writer interface{ write(io.Writer) }

// New returns an empty Registry. A non-empty token makes the handler require
// "Authorization: Bearer <token>".
func New(token string) *Registry { return &Registry{token: token} }

func (r *Registry) add(w writer) {
	r.mu.Lock()
	r.metrics = append(r.metrics, w)
	r.mu.Unlock()
}

type scalar struct {
	name, help, kind string
	fn               func() float64
}

func (s scalar) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %s\n", s.name, escapeHelp(s.help), s.name, s.kind, s.name, num(s.fn()))
}

// Gauge registers a value read from fn at every scrape.
func (r *Registry) Gauge(name, help string, fn func() float64) {
	r.add(scalar{name, help, "gauge", fn})
}

// Counter registers a monotonically increasing value read from fn at every scrape; name
// should end in _total.
func (r *Registry) Counter(name, help string, fn func() float64) {
	r.add(scalar{name, help, "counter", fn})
}

// Histogram observes durations in seconds, one series per value of its single label.
type Histogram struct {
	name, help, label string
	bounds            []float64

	mu     sync.Mutex
	series map[string]*hseries
}

type hseries struct {
	buckets []uint64 // non-cumulative, one per bound
	count   uint64
	sum     float64
}

// Histogram registers a histogram with the given upper bounds (seconds, ascending).
func (r *Registry) Histogram(name, help, label string, bounds []float64) *Histogram {
	h := &Histogram{name: name, help: help, label: label, bounds: append([]float64(nil), bounds...), series: map[string]*hseries{}}
	r.add(h)
	return h
}

// Observe records one duration under the label value.
func (h *Histogram) Observe(labelValue string, d time.Duration) {
	v := d.Seconds()
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[labelValue]
	if s == nil {
		s = &hseries{buckets: make([]uint64, len(h.bounds))}
		h.series[labelValue] = s
	}
	for i, b := range h.bounds {
		if v <= b {
			s.buckets[i]++
			break
		}
	}
	s.count++
	s.sum += v
}

func (h *Histogram) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.name, escapeHelp(h.help), h.name)
	keys := make([]string, 0, len(h.series))
	for k := range h.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, lv := h.series[k], escapeLabel(k)
		var cum uint64
		for i, b := range h.bounds {
			cum += s.buckets[i]
			fmt.Fprintf(w, "%s_bucket{%s=\"%s\",le=\"%s\"} %d\n", h.name, h.label, lv, num(b), cum)
		}
		fmt.Fprintf(w, "%s_bucket{%s=\"%s\",le=\"+Inf\"} %d\n", h.name, h.label, lv, s.count)
		fmt.Fprintf(w, "%s_sum{%s=\"%s\"} %s\n%s_count{%s=\"%s\"} %d\n", h.name, h.label, lv, num(s.sum), h.name, h.label, lv, s.count)
	}
}

// Handler serves the registry in the Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.token != "" {
			got := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(r.token)) != 1 || !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		r.mu.Lock()
		ms := append([]writer(nil), r.metrics...)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		for _, m := range ms {
			m.write(w)
		}
	})
}

// Instrument times every request served by next into h under route(req).
func Instrument(h *Histogram, route func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		defer func() { h.Observe(route(req), time.Since(start)) }()
		next.ServeHTTP(w, req)
	})
}

func num(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}
