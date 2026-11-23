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
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// maxBody is GitHub's documented 25 MB payload cap.
	maxBody = 25 << 20
	// defaultMaxInflight bounds how many request bodies are read at once.
	defaultMaxInflight = 4
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
	Kind           string // "repos_added", "repos_removed", "push", "repo_renamed", "default_branch_changed", "repo_archived", "repo_unarchived"
	InstallationID int64
	// Delivery is the X-GitHub-Delivery header, empty when absent. It is unsigned.
	Delivery string
	Repos    []string
	// push only
	Ref           string
	DefaultBranch string
	Files         []string
	// repo_renamed only: Repos holds the old full name, NewRepo the new one.
	NewRepo string
	// push only: the repository's immutable id (repository.id), 0 when absent.
	RepoID int64
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
	// MaxInflight bounds the bodies read concurrently; further requests get 503
	// with Retry-After. Default 4.
	MaxInflight int
	// ReadTimeout bounds how long one body may take to arrive; default
	// DefaultReadTimeout, negative disables. See Guard.ReadTimeout.
	ReadTimeout time.Duration
	// DedupeTTL is how long a handled delivery is remembered and repeats dropped
	// (answered 202, not handled again). Default 1h; negative disables. A delivery is
	// a repeat when its X-GitHub-Delivery id was already handled. This table is in memory; the app also claims the id in the Store.
	DedupeTTL time.Duration
	// DedupeBySignature also treats a body whose signature was already handled as a
	// repeat. The id is an unsigned header, so without this a captured body replayed
	// under a new id is handled again; with it, so is a genuine redelivery of an
	// identical body dropped, which is why it is off by default.
	DedupeBySignature bool
	// Now is the clock; default time.Now.
	Now func() time.Time

	once   sync.Once
	guard  Guard
	dedupe Dedupe
}

func (h *Handler) init() {
	h.once.Do(func() {
		if h.MaxBody <= 0 || h.MaxBody > maxBody {
			h.MaxBody = maxBody
		}
		if h.Now == nil {
			h.Now = time.Now
		}
		h.guard.MaxInflight, h.guard.ReadTimeout = h.MaxInflight, h.ReadTimeout
		h.dedupe.TTL = h.DedupeTTL
	})
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
	want, _ := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	body, res := h.guard.ReadVerified(w, r, h.MaxBody, h.Secret, nil, func(got []byte) bool { return hmac.Equal(got, want) })
	switch res {
	case ReadAnswered:
		return
	case ReadForged:
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
	ev.Delivery = delivery
	keys := []string{delivery}
	if h.DedupeBySignature {
		keys = append(keys, sig)
	}
	if !h.dedupe.Claim(h.Now(), keys...) {
		h.Log.Info("webhook duplicate delivery dropped", "delivery", delivery)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := h.Sink.Handle(r.Context(), ev); err != nil {
		h.dedupe.Release(keys...)
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
				ID            int64  `json:"id"`
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
			Ref: p.Ref, DefaultBranch: p.Repository.DefaultBranch, Files: files, RepoID: p.Repository.ID}, nil
	case "repository":
		return parseRepository(body)
	}
	return Event{}, errIgnored
}

type login struct {
	Login string `json:"login"`
}

// parseRepository maps the repository event (docs/vendor/github). Deleting forgets the
// repo, archiving pauses it and unarchiving resumes it, a rename or transfer
// moves the stored identity, and a changed default branch re-scans against it.
func parseRepository(body []byte) (Event, error) {
	var p struct {
		Action       string             `json:"action"`
		Installation struct{ ID int64 } `json:"installation"`
		Repository   struct {
			Name          string `json:"name"`
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
		} `json:"repository"`
		Changes struct {
			Repository struct {
				Name struct{ From string } `json:"name"`
			} `json:"repository"`
			Owner struct {
				From struct {
					User         login `json:"user"`
					Organization login `json:"organization"`
				} `json:"from"`
			} `json:"owner"`
			DefaultBranch *struct{ From string } `json:"default_branch"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("bad repository payload: %w", err)
	}
	if p.Repository.FullName == "" {
		return Event{}, errors.New("repository payload without full_name")
	}
	ev := Event{InstallationID: p.Installation.ID, Repos: []string{p.Repository.FullName}, DefaultBranch: p.Repository.DefaultBranch}
	owner, _, _ := strings.Cut(p.Repository.FullName, "/")
	switch p.Action {
	case "deleted":
		ev.Kind = "repos_removed"
	case "archived":
		ev.Kind = "repo_archived"
	case "unarchived":
		ev.Kind = "repo_unarchived"
	case "renamed":
		from := p.Changes.Repository.Name.From
		if from == "" {
			return Event{}, errors.New("renamed payload without changes.repository.name.from")
		}
		ev.Kind, ev.Repos, ev.NewRepo = "repo_renamed", []string{owner + "/" + from}, p.Repository.FullName
	case "transferred":
		from := p.Changes.Owner.From.User.Login
		if from == "" {
			from = p.Changes.Owner.From.Organization.Login
		}
		if from == "" || p.Repository.Name == "" {
			// The old name is unknown: watch the new one and let the old fall away.
			ev.Kind = "repos_added"
			break
		}
		ev.Kind, ev.Repos, ev.NewRepo = "repo_renamed", []string{from + "/" + p.Repository.Name}, p.Repository.FullName
	case "edited":
		if p.Changes.DefaultBranch == nil || p.Repository.DefaultBranch == "" {
			return Event{}, errIgnored
		}
		ev.Kind = "default_branch_changed"
	default:
		return Event{}, errIgnored
	}
	return ev, nil
}
