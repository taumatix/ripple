package gitlab_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
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

func (s *sinkRec) events() []platform.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.got)
}

// user_project_integrations_webhook_events.md "Push events", trimmed: the real payload
// shape, with the project's path_with_namespace and default_branch.
const pushBody = `{"object_kind":"push","event_name":"push","ref":"refs/heads/master","total_commits_count":2,` +
	`"project":{"id":15,"path_with_namespace":"mike/sub/diaspora","default_branch":"master"},` +
	`"commits":[{"id":"b6568","added":["CHANGELOG"],"modified":["app/controller/application.rb"],"removed":[]},` +
	`{"id":"da156","added":[],"modified":["go.mod"],"removed":["old.txt"]}]}`

func serve(t *testing.T, r gitlab.Receiver, hdr map[string]string, body string) (*httptest.ResponseRecorder, *sinkRec) {
	t.Helper()
	sink := &sinkRec{}
	h := gitlab.NewHost(&gitlab.Client{}, r, nil).Receiver(sink)
	req := httptest.NewRequest(http.MethodPost, "/webhook/gitlab", strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w, sink
}

func TestSecretTokenAuth(t *testing.T) {
	r := gitlab.Receiver{SecretToken: "s3cret"}
	ok := map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "s3cret", "X-Gitlab-Event-UUID": "uuid-1"}
	w, sink := serve(t, r, ok, pushBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	e := sink.events()
	if len(e) != 1 || e[0].Kind != platform.Push || e[0].Repos[0] != "mike/sub/diaspora" || e[0].Ref != "refs/heads/master" ||
		e[0].DefaultBranch != "master" || e[0].Delivery != "uuid-1" {
		t.Fatalf("events = %+v", e)
	}
	slices.Sort(e[0].Files)
	if want := []string{"CHANGELOG", "app/controller/application.rb", "go.mod", "old.txt"}; !slices.Equal(e[0].Files, want) {
		t.Errorf("files = %v, want %v", e[0].Files, want)
	}

	for name, h := range map[string]map[string]string{
		"no token":    {"X-Gitlab-Event": "Push Hook"},
		"wrong token": {"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "nope"},
	} {
		w, sink := serve(t, r, h, pushBody)
		if w.Code != http.StatusUnauthorized || len(sink.events()) != 0 {
			t.Errorf("%s: status %d, events %d", name, w.Code, len(sink.events()))
		}
	}
	// A receiver with no secret configured accepts nothing, even an empty token header.
	w, _ = serve(t, gitlab.Receiver{}, map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": ""}, pushBody)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured receiver: status %d", w.Code)
	}
}

// user_project_integrations_webhooks.md "Verify the signature": the signature is
// "v1," + base64(HMAC-SHA256(key, "{webhook-id}.{webhook-timestamp}.{body}")), key from
// the base64 after `whsec_`. The vector was computed independently with openssl:
//
//	printf 'msg_2KWPBgLlAfxdpx2AI54pPJ85f4W.1700000000.{"object_kind":"push"}' |
//	  openssl dgst -sha256 -mac HMAC -macopt hexkey:<hex of the 32 key bytes> -binary | base64
const (
	vecToken = "whsec_YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU=" // base64 of "abcdefghijklmnopqrstuvwxyz012345"
	vecID    = "msg_2KWPBgLlAfxdpx2AI54pPJ85f4W"
	vecTS    = "1700000000"
	vecBody  = `{"object_kind":"push"}`
	vecSig   = "v1,FaavkPjU4pzxJw0oQ0x05dlaK8fsy8iVLsUd5q2AgEE="
)

func TestSignatureVector(t *testing.T) {
	key, err := gitlab.ParseSigningToken(vecToken)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitlab.Signature(key, vecID, vecTS, []byte(vecBody)); got != vecSig {
		t.Errorf("signature = %s, want %s", got, vecSig)
	}
	for _, bad := range []string{"", "abc", "whsec_", "whsec_!!!"} {
		if _, err := gitlab.ParseSigningToken(bad); err == nil {
			t.Errorf("token %q accepted", bad)
		}
	}
}

func TestSignedDeliveries(t *testing.T) {
	key, _ := gitlab.ParseSigningToken(vecToken)
	now := time.Unix(1700000000, 0)
	r := gitlab.Receiver{SigningKey: key, SecretToken: "also", Now: func() time.Time { return now }}
	sign := func(body string) map[string]string {
		return map[string]string{"X-Gitlab-Event": "Push Hook", "webhook-id": vecID, "webhook-timestamp": vecTS,
			"webhook-signature": gitlab.Signature(key, vecID, vecTS, []byte(body))}
	}
	body := strings.Replace(pushBody, "mike/sub/diaspora", "g/app", 1)
	if w, s := serve(t, r, sign(body), body); w.Code != http.StatusAccepted || len(s.events()) != 1 {
		t.Errorf("signed delivery: %d, %d events", w.Code, len(s.events()))
	}
	// Several space-separated signatures: any match is enough.
	h := sign(body)
	h["webhook-signature"] = "v1,AAAA " + h["webhook-signature"]
	if w, _ := serve(t, r, h, body); w.Code != http.StatusAccepted {
		t.Errorf("multi-signature header: %d", w.Code)
	}
	// Tampered body, missing headers, stale timestamp, and the secret token alone
	// (a signing key is configured, so it is required) are all refused.
	h = sign(body)
	for name, c := range map[string]struct {
		hdr  map[string]string
		body string
	}{
		"tampered":     {h, body + " "},
		"secret only":  {map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "also"}, body},
		"no signature": {map[string]string{"X-Gitlab-Event": "Push Hook", "webhook-id": vecID, "webhook-timestamp": vecTS}, body},
		"stale":        {map[string]string{"X-Gitlab-Event": "Push Hook", "webhook-id": vecID, "webhook-timestamp": "1699990000", "webhook-signature": gitlab.Signature(key, vecID, "1699990000", []byte(body))}, body},
	} {
		w, s := serve(t, r, c.hdr, c.body)
		if w.Code != http.StatusUnauthorized || len(s.events()) != 0 {
			t.Errorf("%s: status %d, %d events", name, w.Code, len(s.events()))
		}
	}
}

func TestPushPayloadHandling(t *testing.T) {
	r := gitlab.Receiver{SecretToken: "s"}
	hdr := map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "s"}

	// More commits than the payload lists (it carries the newest 20): the file list is
	// incomplete, so none is given and the repo is scanned in full.
	trunc := strings.Replace(pushBody, `"total_commits_count":2`, `"total_commits_count":45`, 1)
	if w, s := serve(t, r, hdr, trunc); w.Code != 202 || len(s.events()) != 1 || len(s.events()[0].Files) != 0 {
		t.Errorf("truncated push: %d %+v", w.Code, s.events())
	}
	// A push with no commits (a new branch) is a re-scan too.
	empty := `{"object_kind":"push","ref":"refs/heads/x","total_commits_count":0,"project":{"path_with_namespace":"g/app","default_branch":"main"},"commits":[]}`
	if w, s := serve(t, r, hdr, empty); w.Code != 202 || len(s.events()) != 1 || len(s.events()[0].Files) != 0 {
		t.Errorf("empty push: %d %+v", w.Code, s.events())
	}
	// Other event types the hook may be subscribed to are acknowledged and ignored.
	other := map[string]string{"X-Gitlab-Event": "Merge Request Hook", "X-Gitlab-Token": "s"}
	if w, s := serve(t, r, other, `{"object_kind":"merge_request"}`); w.Code != 202 || len(s.events()) != 0 {
		t.Errorf("other event: %d %+v", w.Code, s.events())
	}
	// A project path that is not a GitLab path never reaches the queue.
	bad := strings.Replace(pushBody, "mike/sub/diaspora", "../etc/passwd", 1)
	if w, s := serve(t, r, hdr, bad); w.Code != 400 || len(s.events()) != 0 {
		t.Errorf("bad path: %d %+v", w.Code, s.events())
	}
	if w, _ := serve(t, r, hdr, `not json`); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	// A sink failure is 503 so GitLab sees a failed delivery.
	sink := &sinkRec{err: context.DeadlineExceeded}
	h := gitlab.NewHost(&gitlab.Client{}, r, nil).Receiver(sink)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pushBody))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("sink error: %d", w.Code)
	}
	// Only POST.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}
