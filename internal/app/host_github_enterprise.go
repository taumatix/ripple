package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
)

// EnterpriseHostName is the registry name of the second GitHub: a GitHub Enterprise
// Server served beside the default GitHub host (github.com, or another GHES).
const EnterpriseHostName = "ghes"

func init() { RegisterHost(EnterpriseHostName, buildGitHubEnterprise) }

// buildGitHubEnterprise is the second GitHub host, a GHES with its own App, webhook
// secret and webhook path (/webhook/ghes); its repos are stored as "ghes:owner/repo".
// It is configured by RIPPLE_GHES_URL (the root, no path), RIPPLE_GHES_APP_ID,
// RIPPLE_GHES_KEY_FILE and RIPPLE_GHES_WEBHOOK_SECRET, and optionally
// RIPPLE_GHES_API_VERSION. Without the URL the host is off.
func buildGitHubEnterprise(ctx context.Context, d HostDeps) (*platform.Host, error) {
	root := strings.TrimRight(d.Getenv("RIPPLE_GHES_URL"), "/")
	if root == "" {
		return nil, nil
	}
	if err := config.CheckURL("RIPPLE_GHES_URL", root); err != nil {
		return nil, err
	}
	appID, keyFile, secret := d.Getenv("RIPPLE_GHES_APP_ID"), d.Getenv("RIPPLE_GHES_KEY_FILE"), d.Getenv("RIPPLE_GHES_WEBHOOK_SECRET")
	if appID == "" || keyFile == "" || secret == "" {
		return nil, fmt.Errorf("RIPPLE_GHES_URL is set, so RIPPLE_GHES_APP_ID, RIPPLE_GHES_KEY_FILE and RIPPLE_GHES_WEBHOOK_SECRET are required")
	}
	version := d.Getenv("RIPPLE_GHES_API_VERSION")
	if version != "" {
		if _, err := time.Parse("2006-01-02", version); err != nil {
			return nil, fmt.Errorf("RIPPLE_GHES_API_VERSION: %q is not a YYYY-MM-DD version", version)
		}
	}
	c, err := newGitHubClient(d, appID, keyFile, root+"/api/v3", root, root+"/api/graphql", version)
	if err != nil {
		return nil, fmt.Errorf("RIPPLE_GHES_KEY_FILE: %w", err)
	}
	c.LogRelease(ctx)
	return github.NewNamedHost(EnterpriseHostName, c, []byte(secret), d.Log), nil
}
