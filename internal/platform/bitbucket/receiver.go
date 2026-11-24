package bitbucket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/webhook"
)

const (
	// maxBody bounds a delivery. Bitbucket documents no payload cap; a repo:push
	// payload lists at most five commits per change, so this is generous.
	maxBody = 5 << 20
)

// Sign is the X-Hub-Signature header value for body: "sha256=" and the hex HMAC-SHA256
// (docs/vendor/bitbucket-cloud/webhooks.md, "Validating webhook deliveries").
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func verify(secret, body []byte, header string) bool {
	hexPart, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(hexPart)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// wellFormed reports whether header looks like "sha256=" and 64 hex digits, so a
// request that cannot be signed is refused before its body is read.
func wellFormed(header string) bool {
	hexPart, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(hexPart) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

// FileResolver says which paths a push changed, because the payload does not.
type FileResolver interface {
	// PushFiles returns the changed paths of the commits, or nil to force a re-scan.
	PushFiles(ctx context.Context, repo, branch string, commits []string) ([]string, error)
}

// Receiver is the webhook handler for Bitbucket Cloud's repo:push. Unlike GitHub,
// Bitbucket sends no event when a repository appears (the Client lists workspaces
// for that), and the webhook secret is optional on its side: this receiver refuses
// to run without one, so every accepted delivery is authenticated.
type Receiver struct {
	Secret []byte
	Sink   platform.Sink
	// Files resolves a push's changed paths; nil leaves Files empty (always re-scan).
	Files FileResolver
	Log   *slog.Logger
	// MaxInflight bounds the bodies buffered at once; default 4.
	MaxInflight int
	// Now is the clock for the dedupe window; default time.Now.
	Now func() time.Time

	once   sync.Once
	guard  webhook.Guard
	dedupe webhook.Dedupe
}

func (h *Receiver) init() {
	h.once.Do(func() {
		if h.Now == nil {
			h.Now = time.Now
		}
		if h.Log == nil {
			h.Log = slog.New(slog.DiscardHandler)
		}
		h.guard.MaxInflight = h.MaxInflight
	})
}

type pushChange struct {
	// HasNew is whether "new" was an object: a deleted branch has a null one.
	HasNew           bool
	NewType, NewName string
	Created, Forced  bool
	Truncated        bool
	Hashes           []string
}

type pushPayload struct {
	Changes  []pushChange
	FullName string
}

// parsePush reads the fields of a repo:push payload as the body streams.
func parsePush(r io.Reader) (pushPayload, error) {
	var p pushPayload
	x := &webhook.Extractor{}
	cur := func() *pushChange { return &p.Changes[len(p.Changes)-1] }
	var nStr = func(f func(c *pushChange) *string) *webhook.Node {
		return x.StrAt(func() *string { return f(cur()) })
	}
	change := webhook.Obj(map[string]*webhook.Node{
		"new": webhook.Obj(map[string]*webhook.Node{
			"type": nStr(func(c *pushChange) *string { return &c.NewType }),
			"name": nStr(func(c *pushChange) *string { return &c.NewName }),
		}).OnObject(func() { cur().HasNew = true }),
		"created":   x.BoolAt(func() *bool { return &cur().Created }),
		"forced":    x.BoolAt(func() *bool { return &cur().Forced }),
		"truncated": x.BoolAt(func() *bool { return &cur().Truncated }),
		"commits": webhook.Array(webhook.Obj(map[string]*webhook.Node{
			"hash": x.StrAt(func() *string {
				c := cur()
				c.Hashes = append(c.Hashes, "")
				return &c.Hashes[len(c.Hashes)-1]
			}),
		})),
	}).OnObject(func() { p.Changes = append(p.Changes, pushChange{}) })
	err := webhook.Extract(r, webhook.Obj(map[string]*webhook.Node{
		"push":       webhook.Obj(map[string]*webhook.Node{"changes": webhook.Array(change)}),
		"repository": webhook.Obj(map[string]*webhook.Node{"full_name": x.Str(&p.FullName)}),
	}))
	return p, err
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.init()
	sig := r.Header.Get("X-Hub-Signature")
	request := r.Header.Get("X-Request-UUID")
	if len(h.Secret) == 0 || !wellFormed(sig) {
		h.Log.Warn("bitbucket webhook signature rejected", "request", request)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	want, _ := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	// The body is parsed as it streams and never held; the parse of a forgery is
	// discarded with the 401. Other events are only hashed.
	push := r.Header.Get("X-Event-Key") == "repo:push"
	var p pushPayload
	var perr error
	var consume func(io.Reader) error
	if push {
		consume = func(rd io.Reader) error { p, perr = parsePush(rd); return nil }
	}
	_, res := h.guard.ReadVerifiedStream(w, r, maxBody, h.Secret, nil, func(got []byte) bool { return hmac.Equal(got, want) }, consume)
	switch res {
	case webhook.ReadAnswered:
		return
	case webhook.ReadForged:
		h.Log.Warn("bitbucket webhook signature rejected", "request", request)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if !push {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if perr != nil {
		http.Error(w, "bad push payload", http.StatusBadRequest)
		return
	}
	repo := p.FullName
	if checkRepo(model.Repo{FullName: repo}) != nil {
		http.Error(w, "bad repository name", http.StatusBadRequest)
		return
	}
	keys := []string{request, webhook.SignatureKey(sig)}
	if !h.dedupe.Claim(h.Now(), keys...) {
		h.Log.Info("bitbucket webhook duplicate delivery dropped", "request", request)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	for i, ch := range p.Changes {
		// Only branch updates matter; a deleted branch or a tag cannot move the default branch.
		if !ch.HasNew || ch.NewType != "branch" || ch.NewName == "" {
			continue
		}
		var files []string
		// A new branch, a force push or a truncated commit list cannot be diffed from
		// the payload; leaving Files empty forces a re-scan, which is always correct.
		if h.Files != nil && !ch.Created && !ch.Forced && !ch.Truncated && len(ch.Hashes) > 0 {
			hashes := ch.Hashes
			var err error
			if files, err = h.Files.PushFiles(r.Context(), repo, ch.NewName, hashes); err != nil {
				h.Log.Warn("bitbucket push files unavailable; re-scanning", "repo", repo, "err", err)
				files = nil
			}
		}
		delivery := ""
		if request != "" {
			delivery = fmt.Sprintf("%s-%d", request, i)
		}
		ev := platform.Event{Kind: platform.Push, Delivery: delivery, Repos: []string{repo},
			Ref: "refs/heads/" + ch.NewName, Files: files}
		if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
			h.dedupe.Release(keys...)
			h.Log.Error("bitbucket webhook sink", "repo", repo, "err", err)
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}
