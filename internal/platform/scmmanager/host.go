package scmmanager

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is SCM-Manager's name in the host registry: its repos are stored as
// "scm-manager:namespace/name" and its receiver is served at /webhook/scm-manager.
const HostName = "scm-manager"

// NewHost is SCM-Manager as a platform.Host: the client, and the webhook receiver
// authenticated with secret, which asks c for the files a push changed.
func NewHost(c *Client, secret []byte, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			return &Receiver{Secret: secret, Sink: sink, Files: c, Log: log}
		},
	}
}
