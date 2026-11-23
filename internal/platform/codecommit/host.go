package codecommit

import (
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
)

// HostName is CodeCommit's name in the host registry. Its repos are stored as
// "codecommit:<region>/<name>" and its SNS endpoint is served at /webhook/codecommit.
const HostName = "codecommit"

// NewHost is CodeCommit as a platform.Host: the client, and the SNS receiver r (Sink is
// filled in). New repositories are found by the client's listing, not by an event.
func NewHost(c *Client, r Receiver, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			r := r
			r.Sink, r.Log, r.sem, r.certs = sink, log, make(chan struct{}, maxInflight), &certCache{}
			if r.Files == nil {
				r.Files = c.ChangedFiles
			}
			if r.HTTP == nil {
				r.HTTP = c.HTTP
			}
			return &r
		},
	}
}
