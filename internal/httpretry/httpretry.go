// Package httpretry waits out a rate-limited or briefly unavailable public service. A 429
// (or a 503 that carries Retry-After) is retried after the delay the server names in Retry-After (or a short growing
// backoff when it names none), for a bounded number of attempts and never longer than a
// cap, and the wait ends the moment the context does. When it gives up it returns a
// *LimitError that says which limit was hit, so a log line explains itself.
package httpretry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Defaults for the zero Policy.
const (
	DefaultAttempts = 3
	DefaultMaxWait  = 30 * time.Second
	baseBackoff     = time.Second
)

// Policy bounds the retrying. The zero value is DefaultAttempts attempts, each wait at
// most DefaultMaxWait, real sleeping.
type Policy struct {
	// MaxAttempts is how many requests may be sent in all; 1 turns retrying off.
	MaxAttempts int
	// MaxWait caps one wait: a server asking for longer is not waited on, it is given up on.
	MaxWait time.Duration
	// Sleep waits for d or until ctx ends; nil sleeps for real. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock for an HTTP-date Retry-After; nil is time.Now.
	Now func() time.Time
}

// LimitError is what Do returns when a service stayed limited.
type LimitError struct {
	URL        string
	Status     int
	RetryAfter string        // the header as the server sent it, "" when absent
	Attempts   int           // requests sent
	Waited     time.Duration // total time spent waiting between them
	// Limit names what stopped the retrying: "attempts" or "max wait".
	Limit string
	// Cause is the context error when the wait was cut short.
	Cause error
}

func (e *LimitError) Error() string {
	ra := "none"
	if e.RetryAfter != "" {
		ra = strconv.Quote(e.RetryAfter)
	}
	s := fmt.Sprintf("rate limited by %s (status %d, Retry-After %s): gave up after %d attempts and %s waited, limit: %s",
		e.URL, e.Status, ra, e.Attempts, e.Waited.Round(time.Millisecond), e.Limit)
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

func (e *LimitError) Unwrap() error { return e.Cause }

func (p Policy) attempts() int {
	if p.MaxAttempts <= 0 {
		return DefaultAttempts
	}
	return p.MaxAttempts
}

func (p Policy) maxWait() time.Duration {
	if p.MaxWait <= 0 {
		return DefaultMaxWait
	}
	return p.MaxWait
}

func (p Policy) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ParseRetryAfter reads a Retry-After header, seconds or an HTTP date; ok is false when it
// is absent or unreadable.
func ParseRetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
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

// limited reports whether a response asks the client to come back later: any 429, and a
// 503 that says when (Retry-After). A bare 503 is an outage, not a limit; it is returned
// as is so the caller fails fast and the next poll tries again.
func limited(resp *http.Response) bool {
	return resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get("Retry-After") != "")
}

// Next decides what follows a limited answer. le describes it (Attempts is the number of
// requests sent so far; Waited the time already spent waiting). wait is the delay the server
// asked for, and known is false when it named none, which means a short growing backoff.
// Next sleeps and returns nil when the caller should send the request again, or returns le,
// with Limit (and Cause) filled in, when the policy gives up. It is for callers whose notion
// of "limited" is wider than a 429, such as a 403 carrying GitHub's rate-limit headers.
func (p Policy) Next(ctx context.Context, le *LimitError, wait time.Duration, known bool) error {
	if !known {
		wait = baseBackoff << (le.Attempts - 1)
		if wait > p.maxWait() {
			wait = p.maxWait()
		}
	}
	switch {
	case wait > p.maxWait():
		le.Limit = fmt.Sprintf("max wait %s (server asked for %s)", p.maxWait(), wait)
		return le
	case le.Attempts >= p.attempts():
		le.Limit = fmt.Sprintf("%d attempts", p.attempts())
		return le
	}
	if err := p.sleep(ctx, wait); err != nil {
		le.Limit = "context ended while waiting"
		le.Cause = err
		return le
	}
	le.Waited += wait
	return nil
}

// Do sends the request build returns, again after a 429 or 503 per the policy, and
// returns the first other response (the caller closes its body). build is called once per
// attempt so a body is fresh each time. A response that is still limited when the policy
// gives up comes back as a *LimitError, never as a response.
func (p Policy) Do(ctx context.Context, c *http.Client, build func() (*http.Request, error)) (*http.Response, error) {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	var waited time.Duration
	for attempt := 1; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil || !limited(resp) {
			return resp, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		le := &LimitError{URL: req.URL.Redacted(), Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Attempts: attempt, Waited: waited}
		wait, ok := ParseRetryAfter(resp.Header, now())
		if err := p.Next(ctx, le, wait, ok); err != nil {
			return nil, err
		}
		waited = le.Waited
	}
}
