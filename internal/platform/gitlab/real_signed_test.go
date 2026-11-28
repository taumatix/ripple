//go:build gitlab

package gitlab_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

// TestRealSignedWebhook drives a project webhook that carries a signing token (`whsec_`, GitLab 19.0+)
// against the real instance: GitLab signs a real push delivery, and ripple's Receiver, configured with
// only the signing key, accepts it and refuses the same delivery with the body altered.
func TestRealSignedWebhook(t *testing.T) {
	f := newFixture(t)
	f.Host(t, gitws.Options{WorkDir: t.TempDir()}, time.Now)
	path := f.Path("signed")
	f.AddRepo(t, path, repoFiles())

	// A throwaway 32-byte key; the CI server is throwaway too.
	raw := []byte("ripple-ci-signing-key-0123456789")
	if len(raw) != 32 {
		t.Fatalf("key is %d bytes", len(raw))
	}
	tok := "whsec_" + base64.StdEncoding.EncodeToString(raw)
	key, err := gitlab.ParseSigningToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	f.api.must("POST", "projects/"+url.PathEscape(path)+"/hooks", map[string]any{
		"url": "http://" + relayAddr + "/signed", "signing_token": tok, "push_events": true, "enable_ssl_verification": false})
	head := f.pushFiles(t, path, "main", map[string]string{"signed.txt": "s\n"})

	var d *signedDelivery
	for end := time.Now().Add(90 * time.Second); d == nil && time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		theRelay.mu.Lock()
		for _, c := range theRelay.signed {
			if strings.Contains(string(c.Body), head) {
				d = c
			}
		}
		theRelay.mu.Unlock()
	}
	if d == nil {
		t.Fatal("no signed push delivery reached the relay within 90s")
	}
	t.Logf("FACT signed delivery headers: webhook-id=%q webhook-timestamp=%q webhook-signature has v1, prefix=%v X-Gitlab-Token=%q",
		d.Header.Get("Webhook-Id"), d.Header.Get("Webhook-Timestamp"), strings.HasPrefix(d.Header.Get("Webhook-Signature"), "v1,"), d.Header.Get("X-Gitlab-Token"))

	sink := &sinkRec{}
	h := gitlab.NewHost(f.client, gitlab.Receiver{SigningKey: key}, nil).Receiver(sink)
	replay := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhook/gitlab", strings.NewReader(body))
		req.Header = d.Header.Clone()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if c := replay(string(d.Body) + " "); c != http.StatusUnauthorized {
		t.Errorf("altered body: HTTP %d, want 401", c)
	}
	if n := len(sink.events()); n != 0 {
		t.Errorf("%d events from a forged delivery", n)
	}
	if c := replay(string(d.Body)); c != http.StatusAccepted {
		t.Fatalf("genuine signed delivery: HTTP %d, want 202", c)
	}
	if n := len(sink.events()); n != 1 {
		t.Errorf("%d events from one genuine delivery, want 1", n)
	}
}

// TestRealGroupWebhook records what a CE instance answers to a group webhook (documented as Premium).
func TestRealGroupWebhook(t *testing.T) {
	f := newFixture(t)
	f.Host(t, gitws.Options{WorkDir: t.TempDir()}, time.Now)
	st, _, body := f.api.do("POST", "groups/"+url.PathEscape(topPath)+"/hooks", map[string]any{
		"url": "http://" + relayAddr + "/hook", "token": hookSecret, "push_events": true, "enable_ssl_verification": false})
	t.Logf("FACT POST group hook on CE: HTTP %d: %.200s", st, body)
	if st >= 200 && st < 300 {
		t.Log("the group hook was accepted: group hooks are not Premium-only on this instance")
	}
}
