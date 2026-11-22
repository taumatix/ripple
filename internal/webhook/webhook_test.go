package webhook

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
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
