package azuredevops

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is Azure DevOps's name in the host registry: repos are stored as
// "azuredevops:org/project/repo" and the receiver is served at /webhook/azuredevops.
const HostName = "azuredevops"

// NewHost is Azure DevOps as a platform.Host. The receiver authenticates every
// delivery with the Basic credentials configured on the service hook.
func NewHost(c *Client, auth BasicAuth, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			return &Receiver{Org: c.Org, Auth: auth, Sink: sink, Log: log}
		},
	}
}
