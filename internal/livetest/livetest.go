// Package livetest is the polite HTTP client the `live`-tagged tests use against public
// services (proxy.golang.org, registry.npmjs.org, api.osv.dev, ...). Those tests read
// only, anonymously, a handful of requests each; this transport keeps them that way: a
// descriptive User-Agent, a pause between requests, and Retry-After honoured on 429 and
// 503. A service that still refuses fails the test with the status, it never skips it.
package livetest

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// UserAgent names the caller to the services it reads.
const UserAgent = "ripple-live-verify (+https://github.com/taumatix/ripple; read-only, a few requests per weekly run)"

const (
	maxAttempts = 4
	maxWait     = 60 * time.Second
)

// Transport wraps a RoundTripper with the politeness rules above. The zero value uses
// http.DefaultTransport, a 500ms gap and real sleeping.
type Transport struct {
	Base http.RoundTripper
	// Gap is the minimum time between two requests; zero means 500ms, negative none.
	Gap time.Duration
	// Sleep is time.Sleep unless a test replaces it.
	Sleep func(time.Duration)
	// Now is time.Now unless a test replaces it.
	Now func() time.Time

	mu       sync.Mutex
	last     time.Time
	requests atomic.Int64
	waited   atomic.Int64 // nanoseconds spent honouring Retry-After
}

// Requests is how many requests went out, retries included.
func (t *Transport) Requests() int { return int(t.requests.Load()) }

// RetryWaited is the time spent waiting on Retry-After.
func (t *Transport) RetryWaited() time.Duration { return time.Duration(t.waited.Load()) }

func (t *Transport) sleep(d time.Duration) {
	if t.Sleep != nil {
		t.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (t *Transport) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Transport) pace() {
	gap := t.Gap
	if gap == 0 {
		gap = 500 * time.Millisecond
	}
	if gap < 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if wait := gap - t.now().Sub(t.last); !t.last.IsZero() && wait > 0 {
		t.sleep(wait)
	}
	t.last = t.now()
}

// RetryAfter reads a Retry-After header (seconds or an HTTP date); ok is false when absent
// or unreadable.
func RetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// RoundTrip implements http.RoundTripper. A request with a body is not retried.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent)
	}
	for attempt := 1; ; attempt++ {
		t.pace()
		t.requests.Add(1)
		resp, err := base.RoundTrip(req)
		if err != nil || (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable) {
			return resp, err
		}
		wait, ok := RetryAfter(resp.Header, t.now())
		if !ok {
			wait = time.Duration(attempt) * 2 * time.Second
		}
		if attempt >= maxAttempts || wait > maxWait || (req.Body != nil && req.GetBody == nil) {
			return resp, nil // the caller sees the 429; the test fails with it
		}
		resp.Body.Close()
		t.waited.Add(int64(wait))
		t.sleep(wait)
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("livetest: replaying body: %w", err)
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

// Client returns an http.Client with a 60s timeout over a fresh Transport.
func Client() (*http.Client, *Transport) {
	tr := &Transport{}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}, tr
}
