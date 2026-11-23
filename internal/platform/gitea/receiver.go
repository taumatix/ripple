package gitea

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/platform"
)

const (
	maxBody     = 8 << 20
	maxInflight = 4
	dedupeTTL   = time.Hour
	maxSeen     = 100_000
)

// Sign is the X-Gitea-Signature / X-Forgejo-Signature value for body: the
// lowercase hex HMAC-SHA256, no prefix (docs/vendor/gitea/webhooks.md).
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// signature finds the delivery's signature as lowercase hex, whichever header
// carries it. Gitea sends X-Gitea-Signature and X-Hub-Signature-256 (sha256=...);
// Forgejo's documented example reads X-Forgejo-Signature. Whether Forgejo also
// sends the other two is unverified, so all three are accepted: each is an HMAC of
// the same body under the same secret, so any one that verifies authenticates it.
func signature(h http.Header) (string, bool) {
	for _, name := range []string{"X-Forgejo-Signature", "X-Gitea-Signature", "X-Hub-Signature-256"} {
		v := h.Get(name)
		if name == "X-Hub-Signature-256" {
			v = strings.TrimPrefix(v, "sha256=")
		}
		if len(v) == sha256.Size*2 {
			if _, err := hex.DecodeString(v); err == nil {
				return strings.ToLower(v), true
			}
		}
	}
	return "", false
}

func header(h http.Header, names ...string) string {
	for _, n := range names {
		if v := h.Get(n); v != "" {
			return v
		}
	}
	return ""
}

// Receiver is the webhook http.Handler for Gitea and Forgejo. It authenticates a
// delivery from its headers before reading the body, bounds the body and the number
// held at once, and drops a repeated delivery id.
type Receiver struct {
	Secret []byte
	Sink   platform.Sink
	Log    *slog.Logger
	Now    func() time.Time

	once sync.Once
	sem  chan struct{}
	mu   sync.Mutex
	seen map[string]time.Time
}

func (h *Receiver) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Receiver) claim(id string) bool {
	if id == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if exp, ok := h.seen[id]; ok && now.Before(exp) {
		return false
	}
	if len(h.seen) >= maxSeen {
		for k, exp := range h.seen {
			if !now.Before(exp) {
				delete(h.seen, k)
			}
		}
	}
	if len(h.seen) < maxSeen {
		h.seen[id] = now.Add(dedupeTTL)
	}
	return true
}

func (h *Receiver) release(id string) {
	h.mu.Lock()
	delete(h.seen, id)
	h.mu.Unlock()
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.once.Do(func() { h.sem = make(chan struct{}, maxInflight); h.seen = map[string]time.Time{} })
	delivery := header(r.Header, "X-Forgejo-Delivery", "X-Gitea-Delivery", "X-GitHub-Delivery")
	sig, ok := signature(r.Header)
	if len(h.Secret) == 0 || !ok {
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if r.ContentLength > maxBody {
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	<-h.sem
	if err != nil {
		http.Error(w, "payload too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	want, _ := hex.DecodeString(sig)
	m := hmac.New(sha256.New, h.Secret)
	m.Write(body)
	if !hmac.Equal(want, m.Sum(nil)) {
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	ev, ok, err := parse(header(r.Header, "X-Forgejo-Event", "X-Gitea-Event", "X-GitHub-Event"), body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	ev.Delivery = delivery
	if !h.claim(delivery) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
		h.release(delivery)
		h.Log.Error("webhook sink", "event", ev.Kind, "err", err)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// parse turns a delivery into an Event; ok=false for events ripple ignores.
func parse(name string, body []byte) (platform.Event, bool, error) {
	switch name {
	case "push":
		var p struct {
			Ref          string `json:"ref"`
			TotalCommits *int   `json:"total_commits"`
			Repository   struct {
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
			} `json:"repository"`
			Commits []struct {
				Added, Modified, Removed []string
			} `json:"commits"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return platform.Event{}, false, err
		}
		var files []string
		// A payload that lists fewer commits than were pushed is truncated, and
		// then its file lists are incomplete: leave Files empty to force a re-scan.
		if p.TotalCommits == nil || *p.TotalCommits <= len(p.Commits) {
			seen := map[string]bool{}
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
		}
		return platform.Event{Kind: platform.Push, Repos: []string{p.Repository.FullName}, Ref: p.Ref,
			DefaultBranch: p.Repository.DefaultBranch, Files: files}, true, nil
	case "repository":
		var p struct {
			Action     string `json:"action"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return platform.Event{}, false, err
		}
		switch p.Action {
		case "created":
			return platform.Event{Kind: platform.ReposAdded, Repos: []string{p.Repository.FullName}}, true, nil
		case "deleted":
			return platform.Event{Kind: platform.ReposRemoved, Repos: []string{p.Repository.FullName}}, true, nil
		}
	}
	return platform.Event{}, false, nil
}
