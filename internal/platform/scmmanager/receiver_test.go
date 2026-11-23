package scmmanager_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

type sinkRec struct {
	mu  sync.Mutex
	got []platform.Event
	err error
}

func (s *sinkRec) HandleEvent(_ context.Context, e platform.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, e)
	return s.err
}

type filesRec struct {
	files  []string
	err    error
	def    string
	defErr error
	calls  [][]string
}

func (f *filesRec) DefaultBranch(context.Context, string) (string, error) { return f.def, f.defErr }

func (f *filesRec) PushFiles(_ context.Context, repo, branch string, ids []string) ([]string, error) {
	f.calls = append(f.calls, append([]string{repo, branch}, ids...))
	return f.files, f.err
}

func newReceiver(files scmmanager.Resolver) (*scmmanager.Receiver, *sinkRec) {
	s := &sinkRec{}
	r := &scmmanager.Receiver{Secret: []byte("s3cret"), Sink: s, Files: files}
	return r, s
}

func do(h http.Handler, target, secret, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if secret != "" {
		req.Header.Set(scmmanager.SecretHeader, secret)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

const hook = "/webhook/scm-manager?namespace=acme&name=app"

// webhook-plugin-configuration.md, "Webhook Payload": {"changeset": [{"id", "branches", ...}]}.
const onePush = `{"changeset":[{"id":"4fa9832c50e0ac57183023a30195d0f11a9dc0ef","branches":["main"],"parents":["74fb9da82be3d878be25a3c1c70a69fcbd3c0d66"],"tags":null}]}`

func TestPushBecomesAnEventWithItsFiles(t *testing.T) {
	files := &filesRec{files: []string{"go.mod"}}
	h, sink := newReceiver(files)
	if w := do(h, hook, "s3cret", onePush); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	want := platform.Event{Kind: platform.Push, Delivery: "acme/app@main:4fa9832c50e0ac57183023a30195d0f11a9dc0ef", Repos: []string{"acme/app"}, Ref: "refs/heads/main", Files: []string{"go.mod"}}
	if len(sink.got) != 1 || !reflect.DeepEqual(sink.got[0], want) {
		t.Fatalf("events %+v, want %+v", sink.got, want)
	}
	if !reflect.DeepEqual(files.calls, [][]string{{"acme/app", "main", "4fa9832c50e0ac57183023a30195d0f11a9dc0ef"}}) {
		t.Errorf("resolver calls %v", files.calls)
	}
}

func TestEveryBranchOfThePushGetsOneEvent(t *testing.T) {
	h, sink := newReceiver(&filesRec{files: []string{"a"}})
	body := `{"changeset":[{"id":"aaaaaaa1","branches":["main","dev"]},{"id":"bbbbbbb2","branches":["main"]}]}`
	if w := do(h, hook, "s3cret", body); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	var refs []string
	for _, e := range sink.got {
		refs = append(refs, e.Ref)
	}
	if !reflect.DeepEqual(refs, []string{"refs/heads/main", "refs/heads/dev"}) {
		t.Fatalf("refs %v", refs)
	}
}

// With "send commit data" off the plugin sends no body, and with "every commit" on it
// sends the single changeset (source-excerpts.md#webhook-send). A delivery that names
// no branch is taken as a push to the default branch, which is the only one that matters.
func TestBodylessAndBareChangesetDeliveries(t *testing.T) {
	h, sink := newReceiver(&filesRec{def: "trunk"})
	if w := do(h, hook, "s3cret", ""); w.Code != http.StatusAccepted {
		t.Fatalf("empty body: %d", w.Code)
	}
	want := platform.Event{Kind: platform.Push, Repos: []string{"acme/app"}, Ref: "refs/heads/trunk"}
	if len(sink.got) != 1 || !reflect.DeepEqual(sink.got[0], want) {
		t.Fatalf("a bodyless push is a default-branch push with no files: %+v, want %+v", sink.got, want)
	}
	sink.got = nil
	if w := do(h, hook, "s3cret", `{"id":"aaaaaaa1","branches":["main"]}`); w.Code != http.StatusAccepted {
		t.Fatalf("bare changeset: %d", w.Code)
	}
	if len(sink.got) != 1 || sink.got[0].Ref != "refs/heads/main" {
		t.Fatalf("events %+v", sink.got)
	}
}

// A push whose branch cannot be told must not be dropped quietly.
func TestUnknownBranchWithoutADefaultIsRefused(t *testing.T) {
	for name, files := range map[string]scmmanager.Resolver{"no resolver": nil, "resolver fails": &filesRec{defErr: context.DeadlineExceeded}} {
		h, sink := newReceiver(files)
		if w := do(h, hook, "s3cret", `{"changeset":[{"id":"aaaaaaa1"}]}`); w.Code < 400 {
			t.Errorf("%s: status %d, want an error status", name, w.Code)
		}
		if len(sink.got) != 0 {
			t.Errorf("%s: events %+v", name, sink.got)
		}
	}
}

func TestNoResolverOrResolverFailureLeavesFilesEmpty(t *testing.T) {
	h, sink := newReceiver(nil)
	do(h, hook, "s3cret", onePush)
	h2, sink2 := newReceiver(&filesRec{err: context.DeadlineExceeded})
	do(h2, hook, "s3cret", onePush)
	for _, s := range []*sinkRec{sink, sink2} {
		if len(s.got) != 1 || len(s.got[0].Files) != 0 || s.got[0].Ref != "refs/heads/main" {
			t.Errorf("events %+v, want one push with no files (a re-scan)", s.got)
		}
	}
}

func TestAuthenticationComesBeforeAnythingElse(t *testing.T) {
	h, sink := newReceiver(nil)
	for name, secret := range map[string]string{"missing": "", "wrong": "nope", "prefix": "s3cre", "longer": "s3cret!"} {
		if w := do(h, hook, secret, onePush); w.Code != http.StatusUnauthorized {
			t.Errorf("%s secret: %d, want 401", name, w.Code)
		}
	}
	// A server with no secret configured refuses everything, even an empty header.
	open := &scmmanager.Receiver{Sink: sink}
	if w := do(open, hook, "anything", onePush); w.Code != http.StatusUnauthorized {
		t.Errorf("no secret configured: %d, want 401", w.Code)
	}
	if len(sink.got) != 0 {
		t.Fatalf("unauthenticated deliveries reached the sink: %+v", sink.got)
	}
}

// The plugin's method is configurable: GET, POST, PUT, or AUTO which is POST with a
// body and GET without (source-excerpts.md#webhook-send).
func TestOnlyTheMethodsThePluginSendsAreServed(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		h, _ := newReceiver(&filesRec{def: "main"})
		req := httptest.NewRequest(m, hook, nil)
		req.Header.Set(scmmanager.SecretHeader, "s3cret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Errorf("%s: %d, want 202", m, w.Code)
		}
	}
	h, _ := newReceiver(nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, hook, nil))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, POST, PUT" {
		t.Fatalf("DELETE: %d allow %q", w.Code, w.Header().Get("Allow"))
	}
}

func TestRepositoryComesFromAValidatedQuery(t *testing.T) {
	h, sink := newReceiver(nil)
	for _, target := range []string{
		"/webhook/scm-manager", "/webhook/scm-manager?namespace=acme", "/webhook/scm-manager?name=app",
		"/webhook/scm-manager?namespace=..&name=app", "/webhook/scm-manager?namespace=a%2Fb&name=app",
		"/webhook/scm-manager?namespace=acme&name=a%20b",
	} {
		if w := do(h, target, "s3cret", onePush); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", target, w.Code)
		}
	}
	if len(sink.got) != 0 {
		t.Fatalf("events %+v", sink.got)
	}
}

func TestBadAndHugeBodies(t *testing.T) {
	h, _ := newReceiver(nil)
	if w := do(h, hook, "s3cret", "{not json"); w.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodPost, hook, bytes.NewReader(bytes.Repeat([]byte("x"), 6<<20)))
	req.Header.Set(scmmanager.SecretHeader, "s3cret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("huge body: %d", w.Code)
	}
}

func TestSinkRefusalAnswers503SoTheDeliveryIsVisible(t *testing.T) {
	h, sink := newReceiver(nil)
	sink.err = context.Canceled
	if w := do(h, hook, "s3cret", onePush); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestBusyReceiverAnswers503WithRetryAfter(t *testing.T) {
	s := &sinkRec{}
	h := &scmmanager.Receiver{Secret: []byte("s3cret"), Sink: s, MaxInflight: 1}
	pr, pw := newPipe()
	req := httptest.NewRequest(http.MethodPost, hook, pr)
	req.Header.Set(scmmanager.SecretHeader, "s3cret")
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	<-pw.started
	w := do(h, hook, "s3cret", onePush)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Errorf("second request while one body is buffering: %d retry-after %q", w.Code, w.Header().Get("Retry-After"))
	}
	pw.release()
	<-done
}

// A push is identified by the changesets it carries, so a redelivery has the same
// Delivery and a push of other changesets does not.
func TestPushCarriesItsChangesetsAsItsDeliveryID(t *testing.T) {
	h, sink := newReceiver(&filesRec{})
	other := strings.Replace(onePush, "4fa9832c", "5fa9832c", 1)
	do(h, hook, "s3cret", onePush)
	do(h, hook, "s3cret", onePush)
	do(h, hook, "s3cret", other)
	if len(sink.got) != 3 || sink.got[0].Delivery == "" {
		t.Fatalf("events %+v", sink.got)
	}
	if sink.got[0].Delivery != sink.got[1].Delivery || sink.got[2].Delivery == sink.got[0].Delivery {
		t.Errorf("ids %q %q %q", sink.got[0].Delivery, sink.got[1].Delivery, sink.got[2].Delivery)
	}
}
