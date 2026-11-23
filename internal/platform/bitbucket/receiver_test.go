package bitbucket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucket"
	"github.com/taumatix/ripple/internal/webhook"
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
	files []string
	err   error
	calls [][]string
}

func (f *filesRec) PushFiles(_ context.Context, repo, branch string, hashes []string) ([]string, error) {
	f.calls = append(f.calls, append([]string{repo, branch}, hashes...))
	return f.files, f.err
}

func post(h http.Handler, key, sig string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook/bitbucket", bytes.NewReader(body))
	if key != "" {
		req.Header.Set("X-Event-Key", key)
	}
	req.Header.Set("X-Request-UUID", "uuid-1")
	if sig != "" {
		req.Header.Set("X-Hub-Signature", sig)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// webhooks.md, "Testing the webhook payload validation": the literal vector Atlassian
// publishes for verifying an implementation.
func TestSignMatchesAtlassianTestVector(t *testing.T) {
	got := bitbucket.Sign([]byte("It's a Secret to Everybody"), []byte("Hello World!"))
	want := "sha256=a4771c39fbe90f317c7824e83ddef3caae9cb3d976c214ace1f2937e133263c9"
	if got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func body(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReceiverRejectsBeforeReadingTheBody(t *testing.T) {
	secret := []byte("s3cret")
	b := body(t, pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb"))
	for name, sig := range map[string]string{
		"missing":   "",
		"malformed": "sha256=zz",
		"sha1":      "sha1=" + bitbucket.Sign(secret, b)[7:47],
		"wrong":     bitbucket.Sign([]byte("other"), b),
		"truncated": bitbucket.Sign(secret, b)[:20],
	} {
		sink := &sinkRec{}
		h := &bitbucket.Receiver{Secret: secret, Sink: sink}
		if w := post(h, "repo:push", sig, b); w.Code != http.StatusUnauthorized {
			t.Errorf("%s signature: status %d, want 401", name, w.Code)
		}
		if len(sink.got) != 0 {
			t.Errorf("%s signature: event delivered", name)
		}
	}
}

func TestReceiverWithoutASecretAcceptsNothing(t *testing.T) {
	b := body(t, pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb"))
	sink := &sinkRec{}
	h := &bitbucket.Receiver{Sink: sink}
	if w := post(h, "repo:push", bitbucket.Sign(nil, b), b); w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: Bitbucket's secret is optional, ripple's is not", w.Code)
	}
}

func TestReceiverPushBecomesAnEventWithResolvedFiles(t *testing.T) {
	secret := []byte("s3cret")
	sink, files := &sinkRec{}, &filesRec{files: []string{"go.mod"}}
	h := &bitbucket.Receiver{Secret: secret, Sink: sink, Files: files}
	b := body(t, pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb"))
	if w := post(h, "repo:push", bitbucket.Sign(secret, b), b); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	want := []platform.Event{{Kind: platform.Push, Delivery: "uuid-1-0", Repos: []string{"acme/app"}, Ref: "refs/heads/main", Files: []string{"go.mod"}}}
	if !reflect.DeepEqual(sink.got, want) {
		t.Errorf("events = %+v, want %+v", sink.got, want)
	}
	if want := [][]string{{"acme/app", "main", "bbbbbbb"}}; !reflect.DeepEqual(files.calls, want) {
		t.Errorf("file lookups = %v, want %v", files.calls, want)
	}
}

// A push the payload cannot be diffed from leaves Files empty, which forces a re-scan.
func TestReceiverLeavesFilesEmptyWhenTheDiffIsUnknown(t *testing.T) {
	secret := []byte("s3cret")
	mod := map[string]func(c map[string]any){
		"created":   func(c map[string]any) { c["created"] = true },
		"forced":    func(c map[string]any) { c["forced"] = true },
		"truncated": func(c map[string]any) { c["truncated"] = true },
		"nocommits": func(c map[string]any) { c["commits"] = []any{} },
	}
	for name, f := range mod {
		p := pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb")
		f(p["push"].(map[string]any)["changes"].([]map[string]any)[0])
		sink, files := &sinkRec{}, &filesRec{files: []string{"x"}}
		h := &bitbucket.Receiver{Secret: secret, Sink: sink, Files: files}
		b := body(t, p)
		if w := post(h, "repo:push", bitbucket.Sign(secret, b), b); w.Code != http.StatusAccepted {
			t.Fatalf("%s: status %d", name, w.Code)
		}
		if len(sink.got) != 1 || len(sink.got[0].Files) != 0 || len(files.calls) != 0 {
			t.Errorf("%s: events %+v, lookups %v; want one event without files and no lookup", name, sink.got, files.calls)
		}
	}
	// A lookup that fails also re-scans rather than dropping the push.
	sink := &sinkRec{}
	h := &bitbucket.Receiver{Secret: secret, Sink: sink, Files: &filesRec{err: context.DeadlineExceeded}}
	b := body(t, pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb"))
	post(h, "repo:push", bitbucket.Sign(secret, b), b)
	if len(sink.got) != 1 || len(sink.got[0].Files) != 0 {
		t.Errorf("failed lookup: events %+v", sink.got)
	}
}

func TestReceiverIgnoresWhatCannotMoveABranch(t *testing.T) {
	secret := []byte("s3cret")
	del := map[string]any{"push": map[string]any{"changes": []map[string]any{{"new": nil, "old": map[string]any{"type": "branch", "name": "x"}, "closed": true}}},
		"repository": map[string]any{"full_name": "acme/app"}}
	tag := pushBody("acme/app", "v1", "a", "b")
	tag["push"].(map[string]any)["changes"].([]map[string]any)[0]["new"].(map[string]any)["type"] = "tag"
	for name, c := range map[string]struct {
		key  string
		body []byte
	}{
		"deleted branch": {"repo:push", body(t, del)},
		"tag":            {"repo:push", body(t, tag)},
		"other event":    {"pullrequest:created", body(t, pushBody("acme/app", "main", "a", "b"))},
		"no event key":   {"", body(t, pushBody("acme/app", "main", "a", "b"))},
	} {
		sink := &sinkRec{}
		h := &bitbucket.Receiver{Secret: secret, Sink: sink}
		if w := post(h, c.key, bitbucket.Sign(secret, c.body), c.body); w.Code != http.StatusAccepted {
			t.Errorf("%s: status %d, want 202", name, w.Code)
		}
		if len(sink.got) != 0 {
			t.Errorf("%s: event delivered: %+v", name, sink.got)
		}
	}
}

func TestReceiverRefusesAnUnsafeRepositoryName(t *testing.T) {
	secret := []byte("s3cret")
	b := body(t, pushBody("../etc/passwd", "main", "a", "b"))
	h := &bitbucket.Receiver{Secret: secret, Sink: &sinkRec{}}
	if w := post(h, "repo:push", bitbucket.Sign(secret, b), b); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestReceiverDropsARepeatedRequestAndRetriesAFailedOne(t *testing.T) {
	secret := []byte("s3cret")
	b := body(t, pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb"))
	sink := &sinkRec{}
	h := &bitbucket.Receiver{Secret: secret, Sink: sink}
	post(h, "repo:push", bitbucket.Sign(secret, b), b)
	if w := post(h, "repo:push", bitbucket.Sign(secret, b), b); w.Code != http.StatusAccepted {
		t.Fatalf("repeat: status %d", w.Code)
	}
	if len(sink.got) != 1 {
		t.Fatalf("repeated request delivered %d events, want 1", len(sink.got))
	}

	failing := &sinkRec{err: context.Canceled}
	h = &bitbucket.Receiver{Secret: secret, Sink: failing}
	if w := post(h, "repo:push", bitbucket.Sign(secret, b), b); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sink error: status %d, want 503 so Bitbucket retries", w.Code)
	}
	failing.err = nil
	if w := post(h, "repo:push", bitbucket.Sign(secret, b), b); w.Code != http.StatusAccepted || len(failing.got) != 2 {
		t.Fatalf("retry after failure: status %d, events %d; the failed request must not be remembered", w.Code, len(failing.got))
	}
}

func TestReceiverOnlyAcceptsPost(t *testing.T) {
	h := &bitbucket.Receiver{Secret: []byte("s"), Sink: &sinkRec{}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", w.Code)
	}
}

func TestReceiverDedupesARewrittenRequestIDBySignature(t *testing.T) {
	defer func(old bool) { webhook.DefaultDedupeBySignature = old }(webhook.DefaultDedupeBySignature)
	secret := []byte("s3cret")
	b := body(t, pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb"))
	run := func(on bool) int {
		webhook.DefaultDedupeBySignature = on
		sink := &sinkRec{}
		h := &bitbucket.Receiver{Secret: secret, Sink: sink}
		for _, id := range []string{"u1", "u2"} {
			req := httptest.NewRequest(http.MethodPost, "/webhook/bitbucket", bytes.NewReader(b))
			req.Header.Set("X-Event-Key", "repo:push")
			req.Header.Set("X-Request-UUID", id)
			req.Header.Set("X-Hub-Signature", bitbucket.Sign(secret, b))
			h.ServeHTTP(httptest.NewRecorder(), req)
		}
		return len(sink.got)
	}
	if n := run(false); n != 2 {
		t.Fatalf("off: %d events, want 2", n)
	}
	if n := run(true); n != 1 {
		t.Fatalf("on: %d events, want 1", n)
	}
}
