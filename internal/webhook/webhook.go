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
)

// maxBody is GitHub's documented 25 MB payload cap.
const maxBody = 25 << 20

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

type Handler struct {
	Secret []byte
	Sink   Sink
	Log    *slog.Logger
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "payload too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if !Verify(h.Secret, body, r.Header.Get("X-Hub-Signature-256")) {
		h.Log.Warn("webhook signature rejected", "delivery", r.Header.Get("X-GitHub-Delivery"))
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
	if err := h.Sink.Handle(r.Context(), ev); err != nil {
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
