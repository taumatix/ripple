package bitbucketserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	maxBody       = 8 << 20
	branchTTL     = time.Hour
	maxBranchKeys = 10_000
	zeroHash      = "0000000000000000000000000000000000000000"
)

// Sign is the X-Hub-Signature value for body: "sha256=" and the lowercase hex
// HMAC-SHA256 (docs/vendor/bitbucket-server/webhooks.md, "Webhook secrets").
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Lookup is what a push needs from the REST API: a repo:refs_changed payload names
// neither the default branch nor the changed files (docs/vendor/bitbucket-server/event-payload.md).
type Lookup interface {
	DefaultBranch(ctx context.Context, repo model.Repo) (string, error)
	ChangedFiles(ctx context.Context, repo model.Repo, fromHash, toHash string) ([]string, error)
}

// Receiver is the webhook http.Handler. It authenticates a delivery from its
// headers before reading the body, bounds the body and the number held at once, and
// drops a repeated X-Request-Id. A push to a branch other than the default costs one
// cached default-branch lookup and nothing else.
type Receiver struct {
	Secret []byte
	Sink   platform.Sink
	Log    *slog.Logger
	Lookup Lookup
	Now    func() time.Time

	once   sync.Once
	guard  webhook.Guard
	dedupe webhook.Dedupe
	mu     sync.Mutex
	defs   map[string]branchEntry
}

type branchEntry struct {
	name    string
	expires time.Time
}

func (h *Receiver) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Receiver) defaultBranch(ctx context.Context, repo model.Repo) (string, error) {
	h.mu.Lock()
	e, ok := h.defs[repo.FullName]
	h.mu.Unlock()
	if ok && h.now().Before(e.expires) {
		return e.name, nil
	}
	name, err := h.Lookup.DefaultBranch(ctx, repo)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	if len(h.defs) >= maxBranchKeys {
		h.defs = map[string]branchEntry{}
	}
	h.defs[repo.FullName] = branchEntry{name: name, expires: h.now().Add(branchTTL)}
	h.mu.Unlock()
	return name, nil
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.once.Do(func() {
		h.defs = map[string]branchEntry{}
	})
	delivery := r.Header.Get("X-Request-Id")
	hexSig, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature"), "sha256=")
	want, derr := hex.DecodeString(hexSig)
	if len(h.Secret) == 0 || !ok || derr != nil || len(want) != sha256.Size {
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	body, res := h.guard.ReadVerified(w, r, maxBody, h.Secret, nil, func(got []byte) bool { return hmac.Equal(got, want) })
	switch res {
	case webhook.ReadAnswered:
		return
	case webhook.ReadForged:
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	key := r.Header.Get("X-Event-Key")
	if key == "diagnostics:ping" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if key != "repo:refs_changed" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !h.dedupe.Claim(h.now(), delivery) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	evs, err := h.push(r.Context(), body)
	if err != nil {
		h.dedupe.Release(delivery)
		h.Log.Error("webhook lookup", "delivery", delivery, "err", err)
		http.Error(w, "lookup failed", http.StatusServiceUnavailable)
		return
	}
	for _, ev := range evs {
		ev.Delivery = delivery
		if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
			h.dedupe.Release(delivery)
			h.Log.Error("webhook sink", "event", ev.Kind, "err", err)
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// push turns a repo:refs_changed payload into at most one Push event: for the change
// to the default branch, if the push has one. A branch creation, or a lookup of
// the changed files that fails, leaves Files empty, which forces a re-scan.
func (h *Receiver) push(ctx context.Context, body []byte) ([]platform.Event, error) {
	var p struct {
		Repository struct {
			Slug    string `json:"slug"`
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
		} `json:"repository"`
		Changes []struct {
			RefID    string `json:"refId"`
			FromHash string `json:"fromHash"`
			ToHash   string `json:"toHash"`
			Type     string `json:"type"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, nil // malformed payloads are dropped, not retried
	}
	if p.Repository.Slug == "" || p.Repository.Project.Key == "" || len(p.Changes) == 0 {
		return nil, nil
	}
	repo := model.Repo{FullName: p.Repository.Project.Key + "/" + p.Repository.Slug}
	def, err := h.defaultBranch(ctx, repo)
	if err != nil {
		return nil, err
	}
	for _, c := range p.Changes {
		if c.RefID != branches+def || c.Type == "DELETE" || c.ToHash == zeroHash || c.ToHash == "" {
			continue
		}
		ev := platform.Event{Kind: platform.Push, Repos: []string{repo.FullName}, Ref: c.RefID, DefaultBranch: def}
		if c.Type == "UPDATE" && c.FromHash != "" && c.FromHash != zeroHash {
			files, err := h.Lookup.ChangedFiles(ctx, repo, c.FromHash, c.ToHash)
			if err != nil {
				h.Log.Warn("changed files unavailable, forcing a re-scan", "repo", repo.FullName, "err", err)
			} else if len(files) > 0 {
				ev.Files = files
			}
		}
		return []platform.Event{ev}, nil
	}
	return nil, nil
}
