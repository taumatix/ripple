package app

import (
	"context"
	"fmt"
	"os"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
)

func init() { RegisterHost(github.HostName, buildGitHub) }

// buildGitHub is the GitHub App host from RIPPLE_GITHUB_* and RIPPLE_WEBHOOK_SECRET.
// It is the default host and always configured (config.Server requires it).
func buildGitHub(_ context.Context, d HostDeps) (*platform.Host, error) {
	s := d.Server
	pem, err := os.ReadFile(s.GitHubKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read github key: %w", err)
	}
	key, err := github.ParsePrivateKey(pem)
	if err != nil {
		return nil, err
	}
	g := d.Git
	c := &github.Client{
		APIBase: s.GitHubAPIURL, GitBase: s.GitHubGitURL, HTTP: d.HTTP, Log: d.Log,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		Tokens:     &github.AppAuth{AppID: s.GitHubAppID, Key: key, APIBase: s.GitHubAPIURL, HTTP: d.HTTP},
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.github.com",
	}
	return github.NewHost(c, []byte(s.WebhookSecret), d.Log), nil
}
