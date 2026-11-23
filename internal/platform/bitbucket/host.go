package bitbucket

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is Bitbucket Cloud's name in the host registry: its repos are stored as
// "bitbucket:workspace/slug" and its receiver is served at /webhook/bitbucket.
const HostName = "bitbucket"

// NewHost is Bitbucket Cloud as a platform.Host: the client, and the repo:push
// receiver authenticated with secret, which asks c for the files a push changed.
func NewHost(c *Client, secret []byte, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			return &Receiver{Secret: secret, Sink: sink, Files: c, Log: log}
		},
	}
}
