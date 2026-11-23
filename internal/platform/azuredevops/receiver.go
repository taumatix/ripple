package azuredevops

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
)

// BasicAuth is the credential configured as basicAuthCredentials on the service
// hook subscription (service-hooks-consumers.md, Webhooks). Azure DevOps service
// hooks have no HMAC signature; HTTP Basic or a custom header is all there is, so
// the endpoint must be served over HTTPS.
type BasicAuth struct{ Username, Password string }

const maxBody = 5 << 20

// Receiver turns git.push service-hook deliveries into platform.Events.
type Receiver struct {
	// Org is the configured organization. A payload names its project and repository
	// but its organization only inside URLs, which are not trusted, so the
	// organization is configuration.
	Org  string
	Auth BasicAuth
	Sink platform.Sink
	Log  *slog.Logger
}

func eq(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}

func (h *Receiver) authorized(r *http.Request) bool {
	u, p, ok := r.BasicAuth()
	if !ok || h.Auth.Password == "" {
		return false
	}
	// Both comparisons always run, so timing says nothing about which one failed.
	a, b := eq(u, h.Auth.Username), eq(p, h.Auth.Password)
	return a && b
}

type pushPayload struct {
	ID        string `json:"id"`
	EventType string `json:"eventType"`
	Resource  struct {
		RefUpdates []struct {
			Name string `json:"name"`
		} `json:"refUpdates"`
		Repository struct {
			Name    string `json:"name"`
			Project struct {
				Name string `json:"name"`
			} `json:"project"`
			DefaultBranch string `json:"defaultBranch"`
		} `json:"repository"`
	} `json:"resource"`
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Authenticate before reading the body.
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="ripple"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxBody)
	var p pushPayload
	if err := json.NewDecoder(body).Decode(&p); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if p.EventType != "git.push" {
		w.WriteHeader(http.StatusAccepted) // other events are not subscribed to; ignore, do not make the hook retry
		return
	}
	repo := p.Resource.Repository
	if _, _, _, err := split(h.Org + "/" + repo.Project.Name + "/" + repo.Name); err != nil {
		h.log().Warn("azure devops push for an unsupported repository name", "project", repo.Project.Name, "repo", repo.Name)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	full := h.Org + "/" + repo.Project.Name + "/" + repo.Name
	def := strings.TrimPrefix(repo.DefaultBranch, "refs/heads/")
	for _, u := range p.Resource.RefUpdates {
		if !strings.HasPrefix(u.Name, "refs/heads/") {
			continue
		}
		// The payload lists no changed files (service-hooks-events.md, git.push), so
		// Files stays empty and the engine re-scans the repository.
		e := platform.Event{Kind: platform.Push, Repos: []string{full}, Ref: u.Name, DefaultBranch: def}
		if p.ID != "" {
			e.Delivery = p.ID + "#" + u.Name
		}
		if err := h.Sink.HandleEvent(r.Context(), e); err != nil {
			h.log().Error("azure devops event not queued", "err", err)
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Receiver) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}
