package webhook

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test vector from GitHub's "Validating webhook deliveries" documentation.
func TestSignMatchesGitHubDocsVector(t *testing.T) {
	got := Sign([]byte("It's a Secret to Everybody"), []byte("Hello, World!"))
	if want := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestVerify(t *testing.T) {
	secret, body := []byte("It's a Secret to Everybody"), []byte("Hello, World!")
	good := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if !Verify(secret, body, good) {
		t.Fatal("valid signature rejected")
	}
	for name, h := range map[string]string{
		"empty": "", "no prefix": good[7:], "sha1 prefix": "sha1=" + good[7:], "bad hex": "sha256=zz",
		"truncated": good[:len(good)-2], "wrong": "sha256=" + string(bytes.Repeat([]byte("0"), 64)),
	} {
		if Verify(secret, body, h) {
			t.Errorf("%s accepted", name)
		}
	}
	if Verify(nil, body, good) || Verify([]byte{}, body, Sign(nil, body)) {
		t.Fatal("empty secret must never verify")
	}
}

type sinkFn func(Event) error

func (f sinkFn) Handle(_ context.Context, e Event) error { return f(e) }

func TestHandlerParsesEvents(t *testing.T) {
	secret := []byte("s3cret")
	var got []Event
	h := &Handler{Secret: secret, Log: slog.New(slog.DiscardHandler), Sink: sinkFn(func(e Event) error { got = append(got, e); return nil })}
	post := func(event, body, sig string) int {
		r := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(body))
		r.Header.Set("X-GitHub-Event", event)
		if sig == "" {
			sig = Sign(secret, []byte(body))
		}
		r.Header.Set("X-Hub-Signature-256", sig)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := post("installation", `{"action":"created","installation":{"id":7},"repositories":[{"full_name":"acme/web"},{"full_name":"acme/api"}]}`, ""); c != 202 {
		t.Fatalf("code %d", c)
	}
	post("installation_repositories", `{"action":"removed","installation":{"id":7},"repositories_removed":[{"full_name":"acme/web"}]}`, "")
	post("installation_repositories", `{"action":"added","installation":{"id":7},"repositories_added":[{"full_name":"acme/new"}]}`, "")
	post("push", `{"ref":"refs/heads/main","installation":{"id":7},"repository":{"full_name":"acme/web","default_branch":"main"},"commits":[{"added":["a.txt"],"modified":["go.mod"],"removed":[]},{"modified":["go.mod","x"]}]}`, "")
	want := []Event{
		{Kind: "repos_added", InstallationID: 7, Repos: []string{"acme/web", "acme/api"}},
		{Kind: "repos_removed", InstallationID: 7, Repos: []string{"acme/web"}},
		{Kind: "repos_added", InstallationID: 7, Repos: []string{"acme/new"}},
		{Kind: "push", InstallationID: 7, Repos: []string{"acme/web"}, Ref: "refs/heads/main", DefaultBranch: "main", Files: []string{"a.txt", "go.mod", "x"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	n := len(got)
	if c := post("installation", `{"action":"created"}`, "sha256=00"); c != 401 || len(got) != n {
		t.Fatalf("bad signature: code %d, events %d", c, len(got))
	}
	if c := post("ping", `{}`, ""); c != 202 || len(got) != n {
		t.Fatalf("ping: code %d", c)
	}
	if c := post("push", `{`, ""); c != 400 {
		t.Fatalf("malformed json: %d", c)
	}
	r := httptest.NewRequest("GET", "/webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", w.Code)
	}
}

func TestSinkErrorIs503(t *testing.T) {
	h := &Handler{Secret: []byte("k"), Log: slog.New(slog.DiscardHandler), Sink: sinkFn(func(Event) error { return context.Canceled })}
	r := httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"action":"created"}`))
	r.Header.Set("X-GitHub-Event", "installation")
	r.Header.Set("X-Hub-Signature-256", Sign([]byte("k"), []byte(`{"action":"created"}`)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("%d", w.Code)
	}
}

// harness builds a Handler with a controllable clock and a recording sink.
type harness struct {
	h       *Handler
	secret  []byte
	now     time.Time
	mu      sync.Mutex
	events  int
	sinkErr error
}

func newHarness() *harness {
	hr := &harness{secret: []byte("k"), now: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)}
	hr.h = &Handler{Secret: hr.secret, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { hr.mu.Lock(); defer hr.mu.Unlock(); return hr.now },
		Sink: sinkFn(func(Event) error { hr.mu.Lock(); defer hr.mu.Unlock(); hr.events++; return hr.sinkErr })}
	return hr
}

func (hr *harness) advance(d time.Duration) { hr.mu.Lock(); hr.now = hr.now.Add(d); hr.mu.Unlock() }
func (hr *harness) count() int              { hr.mu.Lock(); defer hr.mu.Unlock(); return hr.events }

const pushBody = `{"ref":"refs/heads/main","installation":{"id":7},"repository":{"full_name":"a/b","default_branch":"main"}}`

func (hr *harness) post(delivery, body string) int {
	r := httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	r.Header.Set("X-GitHub-Event", "push")
	r.Header.Set("X-GitHub-Delivery", delivery)
	r.Header.Set("X-Hub-Signature-256", Sign(hr.secret, []byte(body)))
	w := httptest.NewRecorder()
	hr.h.ServeHTTP(w, r)
	return w.Code
}

func TestReplayedDeliveryIsHandledOnce(t *testing.T) {
	hr := newHarness()
	if c := hr.post("d1", pushBody); c != 202 {
		t.Fatal(c)
	}
	if c := hr.post("d1", pushBody); c != 202 || hr.count() != 1 {
		t.Fatalf("replay: code %d events %d", c, hr.count())
	}
	hr.post("d2", pushBody)
	if hr.count() != 2 {
		t.Fatalf("distinct delivery dropped: %d", hr.count())
	}
}

func TestDeliveryIDForgottenAfterTTL(t *testing.T) {
	hr := newHarness()
	hr.h.DedupeTTL = time.Hour
	hr.post("d1", pushBody)
	hr.advance(59 * time.Minute)
	hr.post("d1", pushBody)
	if hr.count() != 1 {
		t.Fatalf("inside TTL: %d", hr.count())
	}
	hr.advance(2 * time.Minute)
	hr.post("d1", pushBody)
	if hr.count() != 2 {
		t.Fatalf("after TTL: %d", hr.count())
	}
}

func TestFailedDeliveryCanBeRedelivered(t *testing.T) {
	hr := newHarness()
	hr.sinkErr = context.Canceled
	if c := hr.post("d1", pushBody); c != 503 {
		t.Fatal(c)
	}
	hr.sinkErr = nil
	if c := hr.post("d1", pushBody); c != 202 || hr.count() != 2 {
		t.Fatalf("redelivery: code %d events %d", c, hr.count())
	}
}

func TestUnsignedReplayDoesNotPoisonDeliveryID(t *testing.T) {
	hr := newHarness()
	r := httptest.NewRequest("POST", "/", bytes.NewBufferString(pushBody))
	r.Header.Set("X-GitHub-Event", "push")
	r.Header.Set("X-GitHub-Delivery", "d1")
	r.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	hr.h.ServeHTTP(httptest.NewRecorder(), r)
	if c := hr.post("d1", pushBody); c != 202 || hr.count() != 1 {
		t.Fatalf("code %d events %d", c, hr.count())
	}
}

func TestConcurrentSameDeliveryHandledOnce(t *testing.T) {
	hr := newHarness()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); hr.post("same", pushBody) }()
	}
	wg.Wait()
	if hr.count() != 1 {
		t.Fatalf("events %d", hr.count())
	}
}

func TestMissingDeliveryHeaderStillHandled(t *testing.T) {
	hr := newHarness()
	hr.post("", pushBody)
	hr.post("", pushBody)
	if hr.count() != 2 {
		t.Fatalf("events %d", hr.count())
	}
}

func TestUnsignedRequestsNeverReadTheBody(t *testing.T) {
	hr := newHarness()
	for name, sig := range map[string]string{"missing": "", "malformed": "sha256=zz", "short": "sha256=00"} {
		rb := &countingBody{r: strings.NewReader(strings.Repeat("x", 1<<20))}
		r := httptest.NewRequest("POST", "/", rb)
		if sig != "" {
			r.Header.Set("X-Hub-Signature-256", sig)
		}
		w := httptest.NewRecorder()
		hr.h.ServeHTTP(w, r)
		if w.Code != 401 || rb.n != 0 {
			t.Errorf("%s: code %d, read %d bytes", name, w.Code, rb.n)
		}
	}
}

type countingBody struct {
	r io.Reader
	n int
}

func (c *countingBody) Read(p []byte) (int, error) { n, err := c.r.Read(p); c.n += n; return n, err }

func TestOversizedBodyRejected(t *testing.T) {
	hr := newHarness()
	hr.h.MaxBody = 1024
	big := strings.Repeat("x", 2048)
	rb := &countingBody{r: strings.NewReader(big)}
	r := httptest.NewRequest("POST", "/", rb)
	r.ContentLength = int64(len(big))
	r.Header.Set("X-Hub-Signature-256", Sign(hr.secret, []byte(big)))
	w := httptest.NewRecorder()
	hr.h.ServeHTTP(w, r)
	if w.Code != 413 || rb.n != 0 {
		t.Fatalf("declared length: code %d read %d", w.Code, rb.n)
	}
	// An undeclared length is cut off by the limit rather than buffered.
	rb = &countingBody{r: strings.NewReader(big)}
	r = httptest.NewRequest("POST", "/", rb)
	r.ContentLength = -1
	r.Header.Set("X-Hub-Signature-256", Sign(hr.secret, []byte(big)))
	w = httptest.NewRecorder()
	hr.h.ServeHTTP(w, r)
	if w.Code != 413 || rb.n > 1024+512 {
		t.Fatalf("chunked: code %d read %d", w.Code, rb.n)
	}
}

func TestConcurrentBodyReadsAreBounded(t *testing.T) {
	hr := newHarness()
	hr.h.MaxInflight = 2
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	slow := func() int {
		pr := &blockingBody{started: started, release: release}
		r := httptest.NewRequest("POST", "/", pr)
		r.Header.Set("X-Hub-Signature-256", Sign(hr.secret, []byte("{}")))
		w := httptest.NewRecorder()
		hr.h.ServeHTTP(w, r)
		return w.Code
	}
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- slow() }()
	}
	<-started
	<-started
	r := httptest.NewRequest("POST", "/", bytes.NewBufferString("{}"))
	r.Header.Set("X-Hub-Signature-256", Sign(hr.secret, []byte("{}")))
	w := httptest.NewRecorder()
	hr.h.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("third request: %d", w.Code)
	}
	close(release)
	<-done
	<-done
	// Slots are released afterwards.
	if c := hr.post("d9", pushBody); c != 202 {
		t.Fatalf("after release: %d", c)
	}
}

type blockingBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.started <- struct{}{} })
	<-b.release
	return 0, io.EOF
}
