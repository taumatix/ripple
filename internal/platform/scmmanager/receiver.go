package scmmanager

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/webhook"
)

const (
	// maxBody bounds a delivery. The plugin documents no cap; a changeset is a few
	// hundred bytes and a push lists at most its commits.
	maxBody = 5 << 20
)

// SecretHeader is the custom header the webhook must carry, set in the plugin's
// header table (marked "encrypt on storage"). The plugin has no signature scheme of
// its own (docs/vendor/scm-manager/README.md), so this shared secret is the authentication.
const SecretHeader = "X-Ripple-Webhook-Secret"

// Resolver is what the receiver asks the API, because the payload does not say.
type Resolver interface {
	// PushFiles returns the changed paths of the changesets, or nil to force a re-scan.
	PushFiles(ctx context.Context, repo, branch string, ids []string) ([]string, error)
	// DefaultBranch is the branch a delivery that names none is taken to be a push to.
	DefaultBranch(ctx context.Context, repo string) (string, error)
}

// Receiver is the handler for the SCM-Manager webhook plugin. The payload names
// neither the repository nor, without "send commit data", the branch, so the webhook
// URL carries the repository as ?namespace=&name= and the receiver refuses to run
// without a secret: every accepted delivery is authenticated.
type Receiver struct {
	Secret []byte
	Sink   platform.Sink
	// Files resolves a push's changed paths and default branch; nil leaves Files
	// empty (always re-scan) and refuses a delivery that names no branch.
	Files Resolver
	Log   *slog.Logger
	// MaxInflight bounds the bodies buffered at once; default 4.
	MaxInflight int

	once  sync.Once
	guard webhook.Guard
}

func (h *Receiver) init() {
	h.once.Do(func() {
		if h.Log == nil {
			h.Log = slog.New(slog.DiscardHandler)
		}
		h.guard.MaxInflight = h.MaxInflight
	})
}

// authentic compares in constant time, over digests so the length is not leaked.
func (h *Receiver) authentic(got string) bool {
	if len(h.Secret) == 0 || got == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256(h.Secret)
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

type changeset struct {
	ID       string   `json:"id"`
	Branches []string `json:"branches"`
}

// payload is {"changeset": [...]} for a push, or one bare changeset when the plugin
// sends every commit separately (source-excerpts.md#webhook-send).
type payload struct {
	Changesets []changeset `json:"changeset"`
	changeset
}

func (p payload) all() []changeset {
	if len(p.Changesets) > 0 {
		return p.Changesets
	}
	if p.ID != "" {
		return []changeset{p.changeset}
	}
	return nil
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut:
	default:
		w.Header().Set("Allow", "GET, POST, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.init()
	if !h.authentic(r.Header.Get(SecretHeader)) {
		h.Log.Warn("scm-manager webhook secret rejected")
		http.Error(w, "invalid secret", http.StatusUnauthorized)
		return
	}
	repo := r.URL.Query().Get("namespace") + "/" + r.URL.Query().Get("name")
	if checkName(repo) != nil {
		http.Error(w, "the webhook URL needs ?namespace=&name= naming the repository", http.StatusBadRequest)
		return
	}
	body, res := h.guard.ReadBody(w, r, maxBody)
	if res != webhook.ReadOK {
		return
	}
	var p payload
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad webhook payload", http.StatusBadRequest)
			return
		}
	}
	// Changeset ids per branch, in the order the branches first appear.
	var order []string
	ids := map[string][]string{}
	for _, c := range p.all() {
		for _, b := range c.Branches {
			if b == "" {
				continue
			}
			if _, ok := ids[b]; !ok {
				order = append(order, b)
			}
			ids[b] = append(ids[b], c.ID)
		}
	}
	if len(order) == 0 {
		// Nothing says which branch moved. Only the default branch matters to ripple,
		// so take it as that one rather than drop a push nobody would notice missing.
		if h.Files == nil {
			http.Error(w, "payload names no branch", http.StatusBadRequest)
			return
		}
		def, err := h.Files.DefaultBranch(r.Context(), repo)
		if err != nil {
			h.Log.Warn("scm-manager default branch unavailable", "repo", repo, "err", err)
			http.Error(w, "cannot tell which branch was pushed", http.StatusServiceUnavailable)
			return
		}
		order = []string{def}
	}
	for _, branch := range order {
		var files []string
		if h.Files != nil && len(ids[branch]) > 0 {
			var err error
			if files, err = h.Files.PushFiles(r.Context(), repo, branch, ids[branch]); err != nil {
				h.Log.Warn("scm-manager push files unavailable; re-scanning", "repo", repo, "err", err)
				files = nil
			}
		}
		ev := platform.Event{Kind: platform.Push, Repos: []string{repo}, Ref: "refs/heads/" + branch, Files: files}
		if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
			h.Log.Error("scm-manager webhook sink", "repo", repo, "err", err)
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}
