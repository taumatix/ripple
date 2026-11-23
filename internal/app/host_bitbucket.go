package app

import (
	"context"
	"errors"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucket"
)

func init() { RegisterHost(bitbucket.HostName, buildBitbucket) }

// buildBitbucket is Bitbucket Cloud from RIPPLE_BITBUCKET_*: TOKEN (an access token
// or API token), WORKSPACES (comma-separated slugs to discover), WEBHOOK_SECRET, and
// optionally API_URL and GIT_URL. It returns nil, nil when TOKEN is unset.
func buildBitbucket(_ context.Context, d HostDeps) (*platform.Host, error) {
	token := d.Getenv("RIPPLE_BITBUCKET_TOKEN")
	if token == "" {
		return nil, nil
	}
	secret := d.Getenv("RIPPLE_BITBUCKET_WEBHOOK_SECRET")
	if secret == "" {
		return nil, errors.New("RIPPLE_BITBUCKET_WEBHOOK_SECRET is required: Bitbucket's webhook secret is optional on its side, ripple's is not")
	}
	var workspaces []string
	for _, w := range strings.Split(d.Getenv("RIPPLE_BITBUCKET_WORKSPACES"), ",") {
		if w = strings.TrimSpace(w); w != "" {
			workspaces = append(workspaces, w)
		}
	}
	if len(workspaces) == 0 {
		return nil, errors.New("RIPPLE_BITBUCKET_WORKSPACES is required: Bitbucket sends no event for a new repository, so ripple lists these workspaces")
	}
	api, git := d.Getenv("RIPPLE_BITBUCKET_API_URL"), d.Getenv("RIPPLE_BITBUCKET_GIT_URL")
	if api == "" {
		api = "https://api.bitbucket.org/2.0"
	}
	if git == "" {
		git = "https://bitbucket.org"
	}
	g := d.Git
	c := &bitbucket.Client{
		APIBase: api, GitBase: git, HTTP: d.HTTP, Log: d.Log, Workspaces: workspaces,
		Credentials: platform.StaticCredential{Username: bitbucket.GitUsername, Secret: token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.bitbucket.org",
	}
	return bitbucket.NewHost(c, []byte(secret), d.Log), nil
}
