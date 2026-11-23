package gerrit

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
	// maxBody bounds a delivery; an event is a few kilobytes.
	maxBody = 1 << 20
	// TokenParam is the query parameter that carries the webhook secret.
	TokenParam = "token"
)

// FileResolver says which paths a push changed, because the event does not.
type FileResolver interface {
	// PushFiles returns the changed paths of oldRev..newRev, or nil to force a re-scan.
	PushFiles(ctx context.Context, project, oldRev, newRev string) ([]string, error)
}

// Receiver is the webhook handler for Gerrit's webhooks plugin. The plugin signs
// nothing and sends no secret header (docs/vendor/gerrit/webhooks-plugin-config.md
// lists no authentication setting), so the only thing that can authenticate a
// delivery is the URL it was configured with: the secret is the "token" query
// parameter, or the password of HTTP Basic credentials in the URL, compared in
// constant time before the body is read. It travels in clear unless the hook URL is
// https, and appears in the Gerrit project's webhooks.config; docs/platform-gerrit.md
// says what that costs.
type Receiver struct {
	Secret []byte
	Sink   platform.Sink
	// Files resolves a ref update's changed paths; nil leaves Files empty (re-scan).
	Files FileResolver
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

// authorized compares digests, so neither the length nor the content of the secret
// shows in the timing.
func (h *Receiver) authorized(r *http.Request) bool {
	if len(h.Secret) == 0 {
		return false
	}
	got := r.URL.Query().Get(TokenParam)
	if _, pass, ok := r.BasicAuth(); ok && got == "" {
		got = pass
	}
	if got == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256(h.Secret)
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

type refUpdate struct {
	OldRev  string `json:"oldRev"`
	NewRev  string `json:"newRev"`
	RefName string `json:"refName"`
	Project string `json:"project"`
}

type event struct {
	Type        string      `json:"type"`
	RefUpdate   *refUpdate  `json:"refUpdate"`
	RefUpdates  []refUpdate `json:"refUpdates"`
	ProjectName string      `json:"projectName"`
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.init()
	if !h.authorized(r) {
		h.Log.Warn("gerrit webhook rejected: missing or wrong token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, res := h.guard.ReadBody(w, r, maxBody)
	if res != webhook.ReadOK {
		return
	}
	var ev event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad event payload", http.StatusBadRequest)
		return
	}
	var out []platform.Event
	switch ev.Type {
	case "project-created":
		if checkProject(ev.ProjectName) != nil {
			http.Error(w, "bad project name", http.StatusBadRequest)
			return
		}
		out = append(out, platform.Event{Kind: platform.ReposAdded, Repos: []string{ev.ProjectName}})
	case "ref-updated", "batch-ref-updated":
		updates := ev.RefUpdates
		if ev.RefUpdate != nil {
			updates = append(updates, *ev.RefUpdate)
		}
		for _, u := range updates {
			if checkProject(u.Project) != nil {
				http.Error(w, "bad project name", http.StatusBadRequest)
				return
			}
			// Only a moved branch matters: a deleted one (zero newRev) cannot move the
			// default branch, and refs/changes/* and refs/meta/* are Gerrit's own.
			if !strings.HasPrefix(u.RefName, "refs/heads/") || strings.Trim(u.NewRev, "0") == "" {
				continue
			}
			var files []string
			if h.Files != nil {
				var err error
				if files, err = h.Files.PushFiles(r.Context(), u.Project, u.OldRev, u.NewRev); err != nil {
					h.Log.Warn("gerrit push files unavailable; re-scanning", "project", u.Project, "err", err)
					files = nil
				}
			}
			// The transition is the push's identity: Gerrit sends no delivery id, and a
			// retry repeats these four values while any other push changes oldRev or newRev.
			delivery := u.Project + "@" + u.RefName + ":" + u.OldRev + ".." + u.NewRev
			out = append(out, platform.Event{Kind: platform.Push, Delivery: delivery, Repos: []string{u.Project}, Ref: u.RefName, Files: files})
		}
	}
	for _, e := range out {
		if err := h.Sink.HandleEvent(r.Context(), e); err != nil {
			h.Log.Error("gerrit webhook sink", "err", err)
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}
