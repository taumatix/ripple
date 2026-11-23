package gerrit

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is Gerrit's name in the host registry: its projects are stored as
// "gerrit:project" and its receiver is served at /webhook/gerrit.
const HostName = "gerrit"

// NewHost is Gerrit as a platform.Host: the client, and the webhooks-plugin receiver
// authenticated with secret, which asks c for the files a ref update changed.
func NewHost(c *Client, secret []byte, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			return &Receiver{Secret: secret, Sink: sink, Files: c, Log: log}
		},
	}
}
