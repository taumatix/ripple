package bitbucketserver

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is the registry name; repos are stored as "bitbucket-server:KEY/slug".
const HostName = "bitbucket-server"

// NewHost is the client plus the webhook receiver, authenticated with secret.
func NewHost(c *Client, secret []byte, log *slog.Logger) *platform.Host {
	return &platform.Host{Name: HostName, Platform: c, Receiver: func(sink platform.Sink) http.Handler {
		return &Receiver{Secret: secret, Sink: sink, Log: log, Lookup: c}
	}}
}
