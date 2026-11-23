package bitbucket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

const (
	// maxBody bounds a delivery. Bitbucket documents no payload cap; a repo:push
	// payload lists at most five commits per change, so this is generous.
	maxBody            = 5 << 20
	defaultMaxInflight = 4
	dedupeTTL          = time.Hour
	maxRemembered      = 100_000
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

	once sync.Once
	sem  chan struct{}
	mu   sync.Mutex
	seen map[string]time.Time
}

func (h *Receiver) init() {
	h.once.Do(func() {
		if h.MaxInflight <= 0 {
			h.MaxInflight = defaultMaxInflight
		}
		if h.Now == nil {
			h.Now = time.Now
		}
		if h.Log == nil {
			h.Log = slog.New(slog.DiscardHandler)
		}
		h.sem = make(chan struct{}, h.MaxInflight)
		h.seen = map[string]time.Time{}
	})
}

// claim reserves a request id; false means it was already handled. The id is an
// unsigned header, so this stops Bitbucket's own retries and naive replays only.
func (h *Receiver) claim(id string) bool {
	if id == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.Now()
	if exp, ok := h.seen[id]; ok && now.Before(exp) {
		return false
	}
	if len(h.seen) >= maxRemembered {
		for k, exp := range h.seen {
			if !now.Before(exp) {
				delete(h.seen, k)
			}
		}
		if len(h.seen) >= maxRemembered {
			return true // fail open rather than drop a delivery
		}
	}
	h.seen[id] = now.Add(dedupeTTL)
	return true
}

func (h *Receiver) release(id string) {
	h.mu.Lock()
	delete(h.seen, id)
	h.mu.Unlock()
}

type pushRef struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target struct {
		Hash string `json:"hash"`
	} `json:"target"`
}

type pushPayload struct {
	Push struct {
		Changes []struct {
			New       *pushRef `json:"new"`
			Old       *pushRef `json:"old"`
			Created   bool     `json:"created"`
			Forced    bool     `json:"forced"`
			Truncated bool     `json:"truncated"`
			Commits   []struct {
				Hash string `json:"hash"`
			} `json:"commits"`
		} `json:"changes"`
	} `json:"push"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
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
	if !verify(h.Secret, body, sig) {
		h.Log.Warn("bitbucket webhook signature rejected", "request", request)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("X-Event-Key") != "repo:push" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad push payload", http.StatusBadRequest)
		return
	}
	repo := p.Repository.FullName
	if checkRepo(model.Repo{FullName: repo}) != nil {
		http.Error(w, "bad repository name", http.StatusBadRequest)
		return
	}
	if !h.claim(request) {
		h.Log.Info("bitbucket webhook duplicate delivery dropped", "request", request)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	for i, ch := range p.Push.Changes {
		// Only branch updates matter; a deleted branch or a tag cannot move the default branch.
		if ch.New == nil || ch.New.Type != "branch" || ch.New.Name == "" {
			continue
		}
		var files []string
		// A new branch, a force push or a truncated commit list cannot be diffed from
		// the payload; leaving Files empty forces a re-scan, which is always correct.
		if h.Files != nil && !ch.Created && !ch.Forced && !ch.Truncated && len(ch.Commits) > 0 {
			var hashes []string
			for _, c := range ch.Commits {
				hashes = append(hashes, c.Hash)
			}
			var err error
			if files, err = h.Files.PushFiles(r.Context(), repo, ch.New.Name, hashes); err != nil {
				h.Log.Warn("bitbucket push files unavailable; re-scanning", "repo", repo, "err", err)
				files = nil
			}
		}
		delivery := ""
		if request != "" {
			delivery = fmt.Sprintf("%s-%d", request, i)
		}
		ev := platform.Event{Kind: platform.Push, Delivery: delivery, Repos: []string{repo},
			Ref: "refs/heads/" + ch.New.Name, Files: files}
		if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
			h.release(request)
			h.Log.Error("bitbucket webhook sink", "repo", repo, "err", err)
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}
