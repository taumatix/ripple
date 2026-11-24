package testutil

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Throttle makes a fake answer like a rate-limited or slow public service. It is embedded
// in the feed, OSV, module proxy and registry fakes, so a test can say
// `x.Refuse("/index", 2, 429, "3")` and the next two matching requests get a 429 with
// Retry-After: 3 before the fake serves normally again.
type Throttle struct {
	mu      sync.Mutex
	rules   []*throttleRule
	slow    []slowRule
	refused int
}

type throttleRule struct {
	match      string
	remaining  int // negative: refuse forever
	status     int
	retryAfter string
	headers    map[string]string
}

type slowRule struct {
	match string
	delay time.Duration
}

// Refuse answers the next n requests whose path or query contains match with status (and a
// Retry-After header when retryAfter is not empty). A negative n refuses every one.
func (t *Throttle) Refuse(match string, n, status int, retryAfter string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rules = append(t.rules, &throttleRule{match: match, remaining: n, status: status, retryAfter: retryAfter})
}

// RefuseWithHeaders is Refuse with arbitrary response headers (GitHub's X-RateLimit-Reset
// and X-RateLimit-Remaining, for one).
func (t *Throttle) RefuseWithHeaders(match string, n, status int, headers map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rules = append(t.rules, &throttleRule{match: match, remaining: n, status: status, headers: headers})
}

// Slow delays every request whose path or query contains match by d (the delay ends early when
// the client goes away).
func (t *Throttle) Slow(match string, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.slow = append(t.slow, slowRule{match, d})
}

// Refused is how many requests were answered with a refusal.
func (t *Throttle) Refused() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.refused
}

// Wrap puts the throttle in front of next.
func (t *Throttle) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var delay time.Duration
		t.mu.Lock()
		for _, s := range t.slow {
			if strings.Contains(r.URL.RequestURI(), s.match) {
				delay += s.delay
			}
		}
		var hit *throttleRule
		for _, rule := range t.rules {
			if rule.remaining != 0 && strings.Contains(r.URL.RequestURI(), rule.match) {
				hit = rule
				if rule.remaining > 0 {
					rule.remaining--
				}
				t.refused++
				break
			}
		}
		t.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if hit != nil {
			if hit.retryAfter != "" {
				w.Header().Set("Retry-After", hit.retryAfter)
			}
			for k, v := range hit.headers {
				w.Header().Set(k, v)
			}
			http.Error(w, "slow down", hit.status)
			return
		}
		next.ServeHTTP(w, r)
	})
}
