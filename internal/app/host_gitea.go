package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
)

func init() { RegisterHost(gitea.HostName, buildGitea) }

// buildGitea is the Gitea / Forgejo host. It is configured by RIPPLE_GITEA_URL (the
// instance, e.g. https://codeberg.org), RIPPLE_GITEA_TOKEN, RIPPLE_GITEA_WEBHOOK_SECRET,
// and optionally RIPPLE_GITEA_USER (git basic-auth user, default "ripple") and
// RIPPLE_GITEA_FLAVOUR (auto, gitea or forgejo). Without the URL the host is off.
func buildGitea(_ context.Context, d HostDeps) (*platform.Host, error) {
	base := strings.TrimRight(d.Getenv("RIPPLE_GITEA_URL"), "/")
	if base == "" {
		return nil, nil
	}
	token, secret := d.Getenv("RIPPLE_GITEA_TOKEN"), d.Getenv("RIPPLE_GITEA_WEBHOOK_SECRET")
	if token == "" || secret == "" {
		return nil, fmt.Errorf("RIPPLE_GITEA_URL is set, so RIPPLE_GITEA_TOKEN and RIPPLE_GITEA_WEBHOOK_SECRET are required")
	}
	flavour, err := gitea.ParseFlavour(d.Getenv("RIPPLE_GITEA_FLAVOUR"))
	if err != nil {
		return nil, fmt.Errorf("RIPPLE_GITEA_FLAVOUR: %w", err)
	}
	user := d.Getenv("RIPPLE_GITEA_USER")
	if user == "" {
		user = "ripple"
	}
	g := d.Git
	c := &gitea.Client{
		APIBase: base, GitBase: base, HTTP: d.HTTP, Log: d.Log, Flavour: flavour,
		Credentials: platform.StaticCredential{Username: user, Secret: token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@noreply.invalid",
	}
	return gitea.NewHost(c, []byte(secret), d.Log), nil
}
