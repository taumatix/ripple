package gitlab

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is GitLab's name in the host registry. Its repos are stored as
// "gitlab:group/sub/project" and its webhook is served at /webhook/gitlab.
const HostName = "gitlab"

// NewHost is GitLab as a platform.Host: the client, and a push-event receiver
// authenticated with the secret token and/or signing key of r (Sink is filled in).
// New repos are found by the client's membership listing, not by an event.
func NewHost(c *Client, r Receiver, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			r := r
			r.Sink, r.Log, r.sem = sink, log, make(chan struct{}, maxInflight)
			return &r
		},
	}
}
