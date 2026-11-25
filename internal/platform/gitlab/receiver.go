package gitlab

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/webhook"
)

const (
	// maxBody bounds a delivery. A push payload carries at most 20 commits
	// (user_project_integrations_webhook_events.md, "Push events"), far below this.
	maxBody = 10 << 20
	// SignatureTolerance is how far webhook-timestamp may be from now; the docs ask
	// receivers to reject stale timestamps (user_project_integrations_webhooks.md,
	// "Security considerations").
	SignatureTolerance = 5 * time.Minute
)

// ParseSigningToken decodes a `whsec_<base64>` signing token into its HMAC key
// (api_project_webhooks.md: "Must be in `whsec_<base64>` format encoding a 32-byte key").
func ParseSigningToken(tok string) ([]byte, error) {
	b64, ok := strings.CutPrefix(tok, "whsec_")
	if !ok {
		return nil, errors.New("signing token must start with whsec_")
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) == 0 {
		return nil, errors.New("signing token is not whsec_ followed by base64")
	}
	return key, nil
}

// Signature is the webhook-signature value for a delivery:
// `v1,` + base64(HMAC-SHA256(key, "{id}.{timestamp}.{body}")).
func Signature(key []byte, id, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + timestamp + "."))
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// Receiver is the http.Handler for POST /webhook/gitlab. It authenticates before it
// reads the body: with SigningKey set, a valid webhook-signature is required (the
// secret token is then not enough, since it is a plain value that a leaked log
// reveals); otherwise the X-Gitlab-Token header must equal SecretToken. With neither
// set, every delivery is refused.
type Receiver struct {
	SecretToken string
	SigningKey  []byte
	Sink        platform.Sink
	Log         *slog.Logger
	Now         func() time.Time

	guard  *webhook.Guard  // set by NewHost
	dedupe *webhook.Dedupe // set by NewHost
}

func (h *Receiver) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (h *Receiver) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// headersOK checks the headers alone; the signature needs the body, so for a
// signed delivery it only checks the headers are present and the timestamp fresh,
// and verify finishes the job.
func (h *Receiver) headersOK(r *http.Request) bool {
	if len(h.SigningKey) > 0 {
		ts, err := strconv.ParseInt(r.Header.Get("webhook-timestamp"), 10, 64)
		if err != nil || r.Header.Get("webhook-id") == "" || r.Header.Get("webhook-signature") == "" {
			return false
		}
		d := h.now().Sub(time.Unix(ts, 0))
		return d < SignatureTolerance && d > -SignatureTolerance
	}
	if h.SecretToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gitlab-Token")), []byte(h.SecretToken)) == 1
}

// read reads the body under the shared guard and hands it to consume (nil to discard
// it). A signed delivery's signature is computed while the body streams in and consume
// parses it as it goes, so a forgery is never held and its parse is thrown away; an
// unsigned one was authenticated from its headers, so it is parsed as it streams too.
// ok is false once the response has been written.
func (h *Receiver) read(w http.ResponseWriter, r *http.Request, consume func(io.Reader) error) (ok bool) {
	if len(h.SigningKey) == 0 { // the secret token was checked from the headers alone
		if consume == nil {
			_, res := h.guard.ReadBody(w, r, maxBody)
			return res == webhook.ReadOK
		}
		_, res := h.guard.ReadStream(w, r, maxBody, consume)
		return res == webhook.ReadOK
	}
	id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
	got := strings.Fields(r.Header.Get("webhook-signature"))
	_, res := h.guard.ReadVerifiedStream(w, r, maxBody, h.SigningKey, []byte(id+"."+ts+"."), func(sum []byte) bool {
		want := "v1," + base64.StdEncoding.EncodeToString(sum)
		for _, g := range got {
			if subtle.ConstantTimeCompare([]byte(g), []byte(want)) == 1 {
				return true
			}
		}
		return false
	}, consume)
	if res == webhook.ReadForged {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
	return res == webhook.ReadOK
}

type pushPayload struct {
	ObjectKind   string
	Ref          string
	Project      struct{ Path, DefaultBranch string }
	TotalCommits int64
	Commits      []struct{ Added, Modified, Removed []string }
}

// parsePush reads the fields of a push event as the body streams.
func parsePush(r io.Reader) (pushPayload, error) {
	var p pushPayload
	x := &webhook.Extractor{}
	last := func() *struct{ Added, Modified, Removed []string } { return &p.Commits[len(p.Commits)-1] }
	c := webhook.Obj(map[string]*webhook.Node{
		"added":    x.StrsInto(func() *[]string { return &last().Added }),
		"modified": x.StrsInto(func() *[]string { return &last().Modified }),
		"removed":  x.StrsInto(func() *[]string { return &last().Removed }),
	}).OnObject(func() {
		p.Commits = append(p.Commits, struct{ Added, Modified, Removed []string }{})
	})
	err := webhook.Extract(r, webhook.Obj(map[string]*webhook.Node{
		"object_kind": x.Str(&p.ObjectKind),
		"ref":         x.Str(&p.Ref),
		"project": webhook.Obj(map[string]*webhook.Node{
			"path_with_namespace": x.Str(&p.Project.Path),
			"default_branch":      x.Str(&p.Project.DefaultBranch),
		}),
		"total_commits_count": x.I64(&p.TotalCommits),
		"commits":             webhook.Array(c),
	}))
	return p, err
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.headersOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Only push events matter; the hook may subscribe to more and they are not errors.
	push := r.Header.Get("X-Gitlab-Event") == "Push Hook"
	var p pushPayload
	var perr error
	var consume func(io.Reader) error
	if push {
		consume = func(rd io.Reader) error { p, perr = parsePush(rd); return nil }
	}
	if !h.read(w, r, consume) {
		return
	}
	if !push {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if perr != nil || p.ObjectKind != "push" {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if err := CheckPath(p.Project.Path); err != nil {
		http.Error(w, "bad project path", http.StatusBadRequest)
		return
	}
	// webhook-id is "consistent across webhook retries" (user_project_integrations_webhooks.md,
	// "Delivery headers"); X-Gitlab-Event-UUID is shared by recursive webhooks, so it is not the key.
	id := r.Header.Get("webhook-id")
	if id == "" {
		id = r.Header.Get("Idempotency-Key")
	}
	e := platform.Event{Kind: platform.Push, Delivery: id, Repos: []string{p.Project.Path},
		Ref: p.Ref, DefaultBranch: p.Project.DefaultBranch}
	// A push of more than 20 commits lists only the newest 20, so the changed files
	// are incomplete: leave Files empty and the repo is scanned in full.
	if len(p.Commits) > 0 && p.TotalCommits <= int64(len(p.Commits)) {
		for _, c := range p.Commits {
			e.Files = append(e.Files, c.Added...)
			e.Files = append(e.Files, c.Modified...)
			e.Files = append(e.Files, c.Removed...)
		}
	}
	if !h.dedupe.Claim(h.now(), id) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := h.Sink.HandleEvent(r.Context(), e); err != nil {
		h.dedupe.Release(id)
		h.log().Error("gitlab webhook: sink", "repo", p.Project.Path, "err", err)
		http.Error(w, fmt.Sprintf("try again: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
