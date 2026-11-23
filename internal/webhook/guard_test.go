package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var guardSecret = []byte("s3cret")

// hexSigWant returns the authenticity check for an HMAC-SHA256 digest of the body.
func hexSigWant(secret, body []byte) func([]byte) bool {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	want := m.Sum(nil)
	return func(got []byte) bool { return hmac.Equal(got, want) }
}

// forgedWant never matches: every request using it is a forgery.
func forgedWant(got []byte) bool { return false }

// countingReader yields n zero bytes without holding them, and counts what was read.
type countingReader struct {
	n    int64
	read *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, io.EOF
	}
	k := int64(len(p))
	if k > c.n {
		k = c.n
	}
	c.n -= k
	c.read.Add(k)
	return int(k), nil
}

func TestReadVerifiedReturnsAuthenticBody(t *testing.T) {
	g := &Guard{SpoolDir: t.TempDir()}
	body := bytes.Repeat([]byte("x"), 3*defaultSpoolAbove) // forces the spool file path
	var out []byte
	var res ReadResult
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, res = g.ReadVerified(w, r, 1<<20, guardSecret, nil, hexSigWant(guardSecret, body))
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", bytes.NewReader(body)))
	if res != ReadOK || !bytes.Equal(out, body) {
		t.Fatalf("res %v, body equal %v", res, bytes.Equal(out, body))
	}
	if left, _ := os.ReadDir(g.SpoolDir); len(left) != 0 {
		t.Fatalf("%d spool files left behind", len(left))
	}
}

func TestReadVerifiedPrefixIsPartOfTheMAC(t *testing.T) {
	g := &Guard{}
	prefix, body := []byte("id.ts."), []byte(`{"a":1}`)
	want := hexSigWant(guardSecret, append(append([]byte{}, prefix...), body...))
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	_, res := g.ReadVerified(httptest.NewRecorder(), r, 1<<20, guardSecret, prefix, want)
	if res != ReadOK {
		t.Fatalf("prefixed MAC rejected: %v", res)
	}
	r = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	if _, res = g.ReadVerified(httptest.NewRecorder(), r, 1<<20, guardSecret, nil, want); res != ReadForged {
		t.Fatalf("MAC without its prefix accepted: %v", res)
	}
}

func TestReadVerifiedForgedLeavesNoSpoolFile(t *testing.T) {
	g := &Guard{SpoolDir: t.TempDir()}
	r := httptest.NewRequest("POST", "/", bytes.NewReader(bytes.Repeat([]byte("x"), 4*defaultSpoolAbove)))
	body, res := g.ReadVerified(httptest.NewRecorder(), r, 1<<20, guardSecret, nil, forgedWant)
	if res != ReadForged || body != nil {
		t.Fatalf("res %v body %d bytes", res, len(body))
	}
	if left, _ := os.ReadDir(g.SpoolDir); len(left) != 0 {
		t.Fatalf("%d spool files left behind", len(left))
	}
}

func TestReadVerifiedOversizedIs413(t *testing.T) {
	g := &Guard{}
	// Unknown length (chunked), so only the bounded read can notice.
	r := httptest.NewRequest("POST", "/", io.NopCloser(io.MultiReader(strings.NewReader(strings.Repeat("x", 100)))))
	r.ContentLength = -1
	w := httptest.NewRecorder()
	_, res := g.ReadVerified(w, r, 10, guardSecret, nil, forgedWant)
	if res != ReadAnswered || w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("res %v code %d", res, w.Code)
	}
}

// The residual this closes: a forged signature must not cost MaxBody of memory per
// in-flight request. N concurrent oversized forged requests over a real server are
// read to the end (counting reader), yet heap stays far below what buffering them
// (MaxInflight * body) would take.
func TestForgedBodiesAreBoundedInMemory(t *testing.T) {
	const (
		bodySize = 16 << 20
		clients  = 16
		inflight = 4
	)
	g := &Guard{MaxInflight: inflight, SpoolDir: t.TempDir()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, res := g.ReadVerified(w, r, 32<<20, guardSecret, nil, forgedWant); res == ReadForged {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > peak.Load() {
					peak.Store(ms.HeapAlloc)
				}
			}
		}
	}()

	var sent atomic.Int64
	var served, unauthorized atomic.Int32
	var wg sync.WaitGroup
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", srv.URL, &countingReader{n: bodySize, read: &sent})
			req.ContentLength = -1 // chunked: the server cannot refuse on Content-Length
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				unauthorized.Add(1)
			}
			served.Add(1)
		}()
	}
	wg.Wait()
	close(stop)
	<-sampled

	if unauthorized.Load() == 0 {
		t.Fatalf("no request got as far as the signature check (served %d)", served.Load())
	}
	// Bodies that got a slot were read in full, which is what makes the memory claim meaningful.
	if sent.Load() < int64(unauthorized.Load())*bodySize {
		t.Fatalf("server read %d bytes for %d forged requests of %d", sent.Load(), unauthorized.Load(), bodySize)
	}
	grew := int64(peak.Load()) - int64(base.HeapAlloc)
	// Buffering would hold inflight*bodySize = 64 MiB (more with ReadAll's doubling).
	if limit := int64(8 << 20); grew > limit {
		t.Fatalf("heap grew by %d bytes under forged load, want < %d", grew, limit)
	}
	if left, _ := os.ReadDir(g.SpoolDir); len(left) != 0 {
		t.Fatalf("%d spool files left behind", len(left))
	}
}

// A sender that trickles must not hold a slot past ReadTimeout.
func TestSlowSenderReleasesItsSlot(t *testing.T) {
	g := &Guard{MaxInflight: 1, ReadTimeout: 300 * time.Millisecond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, res := g.ReadBody(w, r, 1<<20)
		if res == ReadOK {
			w.Write(body)
		}
	}))
	defer srv.Close()

	pr, pw := io.Pipe()
	defer pw.Close()
	slowDone := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("POST", srv.URL, pr)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slowDone <- -1
			return
		}
		resp.Body.Close()
		slowDone <- resp.StatusCode
	}()
	pw.Write([]byte("tr")) // one trickle, then silence
	time.Sleep(50 * time.Millisecond)

	// While the slow request holds the only slot, a genuine one is told to retry.
	resp, err := http.Post(srv.URL, "text/plain", strings.NewReader("ok"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while the slot is held, got %d", resp.StatusCode)
	}

	select {
	case code := <-slowDone:
		if code != http.StatusRequestTimeout {
			t.Fatalf("slow sender got %d, want 408", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow sender still holds its slot long after ReadTimeout")
	}
	resp, err = http.Post(srv.URL, "text/plain", strings.NewReader("ok"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(got) != "ok" {
		t.Fatalf("slot not released: %d %q", resp.StatusCode, got)
	}
}

func TestReadBodyBusyAndTooLargeContentLength(t *testing.T) {
	g := &Guard{MaxInflight: 1}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 20)))
	if _, res := g.ReadBody(w, r, 10); res != ReadAnswered || w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared oversize: res %v code %d", res, w.Code)
	}
	release, ok := g.Acquire(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), 10)
	if !ok {
		t.Fatal("first Acquire refused")
	}
	w = httptest.NewRecorder()
	if _, res := g.ReadBody(w, httptest.NewRequest("POST", "/", strings.NewReader("x")), 10); res != ReadAnswered ||
		w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("busy: res %v code %d", res, w.Code)
	}
	release()
}

func TestDedupeKeysAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	d := &Dedupe{TTL: time.Minute}
	if !d.Claim(now, "id-1", "sig-a") {
		t.Fatal("first claim refused")
	}
	if d.Claim(now, "id-1") {
		t.Fatal("repeated id accepted")
	}
	if d.Claim(now, "id-2", "sig-a") { // id rewritten on a captured body: same signature
		t.Fatal("rewritten id on a known body accepted")
	}
	if d.Claim(now.Add(30*time.Second), "sig-a") {
		t.Fatal("claim inside TTL accepted")
	}
	if !d.Claim(now.Add(2*time.Minute), "id-1") {
		t.Fatal("claim after TTL refused")
	}
	d.Release("id-1", "sig-a")
	if !d.Claim(now.Add(2*time.Minute), "id-1", "sig-a") {
		t.Fatal("released keys not claimable")
	}
	if !d.Claim(now, "", "") {
		t.Fatal("empty keys must never dedupe")
	}
	off := &Dedupe{TTL: -1}
	if !off.Claim(now, "x") || !off.Claim(now, "x") {
		t.Fatal("negative TTL must disable dedupe")
	}
}

func TestDedupeFailsOpenWhenFull(t *testing.T) {
	now := time.Unix(1000, 0)
	d := &Dedupe{TTL: time.Hour, Max: 2}
	d.Claim(now, "a")
	d.Claim(now, "b")
	if !d.Claim(now, "c") || !d.Claim(now, "c") {
		t.Fatal("a full table must fail open, not drop deliveries")
	}
}

func TestSpoolMemoryNeverTouchesTheDisk(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 3*defaultSpoolAbove)
	missing := t.TempDir() + "/not-there" // a spill would fail with 500
	for name, spool := range map[string]SpoolMode{"set on the guard": SpoolMemory, "set as the process default": SpoolDefault} {
		t.Run(name, func(t *testing.T) {
			if spool == SpoolDefault {
				old := DefaultSpool
				DefaultSpool = SpoolMemory
				t.Cleanup(func() { DefaultSpool = old })
			}
			g := &Guard{SpoolDir: missing, Spool: spool}
			r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
			w := httptest.NewRecorder()
			out, res := g.ReadVerified(w, r, 1<<20, guardSecret, nil, hexSigWant(guardSecret, body))
			if res != ReadOK || !bytes.Equal(out, body) {
				t.Fatalf("res=%v code=%d len=%d", res, w.Code, len(out))
			}
		})
	}
	// The disk mode is still what an unset guard does.
	g := &Guard{SpoolDir: missing}
	w := httptest.NewRecorder()
	if _, res := g.ReadVerified(w, httptest.NewRequest("POST", "/", bytes.NewReader(body)), 1<<20, guardSecret, nil, hexSigWant(guardSecret, body)); res != ReadAnswered || w.Code != 500 {
		t.Fatalf("default should spill to SpoolDir: res=%v code=%d", res, w.Code)
	}
}

func TestSpoolMemoryStillBoundsAndRejectsForgery(t *testing.T) {
	g := &Guard{Spool: SpoolMemory}
	w := httptest.NewRecorder()
	big := bytes.Repeat([]byte("x"), 100)
	if _, res := g.ReadVerified(w, httptest.NewRequest("POST", "/", bytes.NewReader(big)), 10, guardSecret, nil, forgedWant); res != ReadAnswered || w.Code != 413 {
		t.Fatalf("over the cap: res=%v code=%d", res, w.Code)
	}
	if _, res := g.ReadVerified(httptest.NewRecorder(), httptest.NewRequest("POST", "/", bytes.NewReader(big)), 1<<20, guardSecret, nil, forgedWant); res != ReadForged {
		t.Fatalf("forged: %v", res)
	}
}
