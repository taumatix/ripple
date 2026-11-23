package gitlab

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/platform"
)

const (
	// maxBody bounds a delivery. A push payload carries at most 20 commits
	// (user_project_integrations_webhook_events.md, "Push events"), far below this.
	maxBody = 10 << 20
	// maxInflight bounds how many request bodies are buffered at once.
	maxInflight = 4
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

	sem chan struct{} // set by NewHost
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

func (h *Receiver) verify(r *http.Request, body []byte) bool {
	if len(h.SigningKey) == 0 {
		return true
	}
	want := Signature(h.SigningKey, r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), body)
	for _, got := range strings.Fields(r.Header.Get("webhook-signature")) {
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

type pushPayload struct {
	ObjectKind string `json:"object_kind"`
	Ref        string `json:"ref"`
	Project    struct {
		Path          string `json:"path_with_namespace"`
		DefaultBranch string `json:"default_branch"`
	} `json:"project"`
	TotalCommits int `json:"total_commits_count"`
	Commits      []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
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
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad body", http.StatusRequestEntityTooLarge)
		return
	}
	if !h.verify(r, body) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Only push events matter; the hook may subscribe to more and they are not errors.
	if r.Header.Get("X-Gitlab-Event") != "Push Hook" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil || p.ObjectKind != "push" {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if err := CheckPath(p.Project.Path); err != nil {
		http.Error(w, "bad project path", http.StatusBadRequest)
		return
	}
	e := platform.Event{Kind: platform.Push, Delivery: r.Header.Get("X-Gitlab-Event-UUID"), Repos: []string{p.Project.Path},
		Ref: p.Ref, DefaultBranch: p.Project.DefaultBranch}
	// A push of more than 20 commits lists only the newest 20, so the changed files
	// are incomplete: leave Files empty and the repo is scanned in full.
	if len(p.Commits) > 0 && p.TotalCommits <= len(p.Commits) {
		for _, c := range p.Commits {
			e.Files = append(e.Files, c.Added...)
			e.Files = append(e.Files, c.Modified...)
			e.Files = append(e.Files, c.Removed...)
		}
	}
	if err := h.Sink.HandleEvent(r.Context(), e); err != nil {
		h.log().Error("gitlab webhook: sink", "repo", p.Project.Path, "err", err)
		http.Error(w, fmt.Sprintf("try again: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
