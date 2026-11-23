package github

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/webhook"
)

// HostName is GitHub's name in the host registry. GitHub is the default host:
// its repos are stored as plain owner/repo.
const HostName = "github"

// NewHost is GitHub as a platform.Host: the client, and the App webhook receiver
// (installation and installation_repositories register repos, push re-scans them),
// authenticated with secret.
func NewHost(c *Client, secret []byte, log *slog.Logger) *platform.Host {
	return &platform.Host{
		Name:     HostName,
		Platform: c,
		Receiver: func(sink platform.Sink) http.Handler {
			return &webhook.Handler{Secret: secret, Sink: Sink{sink}, Log: log}
		},
	}
}

// Sink adapts a platform.Sink to the GitHub receiver's webhook.Sink.
type Sink struct{ platform.Sink }

func (s Sink) Handle(ctx context.Context, e webhook.Event) error {
	return s.HandleEvent(ctx, platform.Event{Kind: platform.EventKind(e.Kind), Scope: e.InstallationID, Delivery: e.Delivery,
		Repos: e.Repos, Ref: e.Ref, DefaultBranch: e.DefaultBranch, Files: e.Files, NewRepo: e.NewRepo})
}
