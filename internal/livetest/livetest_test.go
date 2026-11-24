package livetest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastTransport() (*Transport, *[]time.Duration) {
	var slept []time.Duration
	return &Transport{Gap: -1, Sleep: func(d time.Duration) { slept = append(slept, d) }}, &slept
}

func TestRetryAfterIsHonouredThenSucceeds(t *testing.T) {
	var n atomic.Int32
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	tr, slept := fastTransport()
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" || len(*slept) != 1 || (*slept)[0] != 7*time.Second || tr.Requests() != 2 || tr.RetryWaited() != 7*time.Second {
		t.Fatalf("body %q slept %v requests %d", b, *slept, tr.Requests())
	}
	if !strings.HasPrefix(ua, "ripple-live-verify") {
		t.Fatalf("user agent %q", ua)
	}
}

func TestAPersistent429IsReturnedNotHidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	tr, _ := fastTransport()
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil || resp.StatusCode != 429 || tr.Requests() != maxAttempts {
		t.Fatalf("%v %v requests %d", resp, err, tr.Requests())
	}
}

func TestAnAbsurdRetryAfterIsNotWaitedOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "86400")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	tr, slept := fastTransport()
	resp, _ := (&http.Client{Transport: tr}).Get(srv.URL)
	if resp.StatusCode != 429 || len(*slept) != 0 || tr.Requests() != 1 {
		t.Fatalf("status %d slept %v requests %d", resp.StatusCode, *slept, tr.Requests())
	}
}

func TestRetryAfterAsHTTPDate(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	h := http.Header{"Retry-After": {now.Add(30 * time.Second).Format(http.TimeFormat)}}
	if d, ok := RetryAfter(h, now); !ok || d != 30*time.Second {
		t.Fatalf("%v %v", d, ok)
	}
	if _, ok := RetryAfter(http.Header{}, now); ok {
		t.Fatal("absent header read as present")
	}
}

func TestRequestsArePaced(t *testing.T) {
	var slept []time.Duration
	clock := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tr := &Transport{Gap: time.Second, Now: func() time.Time { return clock }, Sleep: func(d time.Duration) { slept = append(slept, d); clock = clock.Add(d) }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := &http.Client{Transport: tr}
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if len(slept) != 2 || slept[0] != time.Second {
		t.Fatalf("slept %v", slept)
	}
}
