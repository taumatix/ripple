// Package webhook receives and authenticates GitHub webhook deliveries.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// maxBody is GitHub's documented 25 MB payload cap.
	maxBody = 25 << 20
	// defaultMaxInflight bounds how many request bodies are buffered at once, so
	// worst-case body memory is defaultMaxInflight * maxBody.
	defaultMaxInflight = 4
	// defaultDedupeTTL is how long a handled X-GitHub-Delivery id is remembered.
	defaultDedupeTTL = time.Hour
	// maxRemembered caps the delivery ids held; beyond it, dedupe fails open.
	maxRemembered = 100_000
)

// Sign returns the X-Hub-Signature-256 header value for body.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks header against body in constant time.
func Verify(secret, body []byte, header string) bool {
	if len(secret) == 0 || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// Event is the subset of a delivery ripple acts on.
type Event struct {
	Kind           string // "repos_added", "repos_removed", "push"
	InstallationID int64
	Repos          []string
	// push only
	Ref           string
	DefaultBranch string
	Files         []string
}

// Sink consumes events. Returning an error makes the receiver answer 503, but
// GitHub does not redeliver a failed delivery on its own: the event is lost unless
// someone redelivers it from the App's "Recent Deliveries" page or the API.
type Sink interface {
	Handle(ctx context.Context, e Event) error
}

// Handler is the http.Handler for POST /webhook. The zero value of every field but
// Secret, Sink and Log selects a safe default.
type Handler struct {
	Secret []byte
	Sink   Sink
	Log    *slog.Logger

	// MaxBody is the largest accepted payload in bytes; default and ceiling 25 MB.
	MaxBody int64
	// MaxInflight bounds the bodies buffered concurrently; further requests get 503
	// with Retry-After. Default 4.
	MaxInflight int
	// DedupeTTL is how long a handled X-GitHub-Delivery id is remembered and repeats
	// dropped (answered 202, not handled again). Default 1h; negative disables.
	// The id is an unsigned header, so this stops accidental redelivery and naive
	// replays, not an attacker who rewrites the header on a captured body.
	DedupeTTL time.Duration
	// Now is the clock; default time.Now.
	Now func() time.Time

	once      sync.Once
	sem       chan struct{}
	mu        sync.Mutex
	seen      map[string]time.Time
	lastPurge time.Time
}

func (h *Handler) init() {
	h.once.Do(func() {
		if h.MaxBody <= 0 || h.MaxBody > maxBody {
			h.MaxBody = maxBody
		}
		if h.MaxInflight <= 0 {
			h.MaxInflight = defaultMaxInflight
		}
		if h.DedupeTTL == 0 {
			h.DedupeTTL = defaultDedupeTTL
		}
		if h.Now == nil {
			h.Now = time.Now
		}
		h.sem = make(chan struct{}, h.MaxInflight)
		h.seen = map[string]time.Time{}
	})
}

// claim reserves a delivery id; false means it was already handled or is in flight.
func (h *Handler) claim(id string) bool {
	if id == "" || h.DedupeTTL < 0 {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.Now()
	if exp, ok := h.seen[id]; ok && now.Before(exp) {
		return false
	}
	if len(h.seen) >= maxRemembered || now.Sub(h.lastPurge) > time.Minute {
		for k, exp := range h.seen {
			if !now.Before(exp) {
				delete(h.seen, k)
			}
		}
		h.lastPurge = now
	}
	if len(h.seen) >= maxRemembered {
		h.Log.Warn("webhook dedupe table full; not remembering delivery", "delivery", id)
		return true
	}
	h.seen[id] = now.Add(h.DedupeTTL)
	return true
}

// release forgets a claimed id so a failed delivery can be redelivered.
func (h *Handler) release(id string) {
	if id == "" || h.DedupeTTL < 0 {
		return
	}
	h.mu.Lock()
	delete(h.seen, id)
	h.mu.Unlock()
}

// wellFormed reports whether header looks like "sha256=" and 64 hex digits, so
// requests that cannot possibly be signed are refused before any body is read.
func wellFormed(header string) bool {
	hexPart, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(hexPart) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

type repoList []struct {
	FullName string `json:"full_name"`
}

func (l repoList) names() []string {
	var out []string
	for _, r := range l {
		out = append(out, r.FullName)
	}
	return out
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.init()
	delivery := r.Header.Get("X-GitHub-Delivery")
	sig := r.Header.Get("X-Hub-Signature-256")
	if len(h.Secret) == 0 || !wellFormed(sig) {
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if r.ContentLength > h.MaxBody {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	select {
	case h.sem <- struct{}{}:
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.MaxBody))
	<-h.sem
	if err != nil {
		http.Error(w, "payload too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if !Verify(h.Secret, body, sig) {
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	ev, err := Parse(r.Header.Get("X-GitHub-Event"), body)
	if errors.Is(err, errIgnored) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.claim(delivery) {
		h.Log.Info("webhook duplicate delivery dropped", "delivery", delivery)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := h.Sink.Handle(r.Context(), ev); err != nil {
		h.release(delivery)
		h.Log.Error("webhook sink", "event", ev.Kind, "err", err)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

var errIgnored = errors.New("ignored event")

// Parse converts a delivery into an Event; errIgnored for events ripple does not act on.
func Parse(name string, body []byte) (Event, error) {
	switch name {
	case "installation":
		var p struct {
			Action       string             `json:"action"`
			Installation struct{ ID int64 } `json:"installation"`
			Repositories repoList           `json:"repositories"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return Event{}, fmt.Errorf("bad installation payload: %w", err)
		}
		switch p.Action {
		case "created":
			return Event{Kind: "repos_added", InstallationID: p.Installation.ID, Repos: p.Repositories.names()}, nil
		case "deleted":
			return Event{Kind: "repos_removed", InstallationID: p.Installation.ID, Repos: p.Repositories.names()}, nil
		}
	case "installation_repositories":
		var p struct {
			Installation struct{ ID int64 } `json:"installation"`
			Added        repoList           `json:"repositories_added"`
			Removed      repoList           `json:"repositories_removed"`
			Action       string             `json:"action"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return Event{}, fmt.Errorf("bad installation_repositories payload: %w", err)
		}
		switch p.Action {
		case "added":
			return Event{Kind: "repos_added", InstallationID: p.Installation.ID, Repos: p.Added.names()}, nil
		case "removed":
			return Event{Kind: "repos_removed", InstallationID: p.Installation.ID, Repos: p.Removed.names()}, nil
		}
	case "push":
		var p struct {
			Ref          string             `json:"ref"`
			Installation struct{ ID int64 } `json:"installation"`
			Repository   struct {
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
			} `json:"repository"`
			Commits []struct {
				Added, Modified, Removed []string
			} `json:"commits"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return Event{}, fmt.Errorf("bad push payload: %w", err)
		}
		seen := map[string]bool{}
		var files []string
		for _, c := range p.Commits {
			for _, l := range [][]string{c.Added, c.Modified, c.Removed} {
				for _, f := range l {
					if !seen[f] {
						seen[f] = true
						files = append(files, f)
					}
				}
			}
		}
		return Event{Kind: "push", InstallationID: p.Installation.ID, Repos: []string{p.Repository.FullName},
			Ref: p.Ref, DefaultBranch: p.Repository.DefaultBranch, Files: files}, nil
	}
	return Event{}, errIgnored
}
