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
func buildGitHub(ctx context.Context, d HostDeps) (*platform.Host, error) {
	s := d.Server
	c, err := newGitHubClient(d, s.GitHubAppID, s.GitHubKeyFile, s.GitHubAPIURL, s.GitHubGitURL, s.GitHubGraphQLURL, s.GitHubAPIVersion)
	if err != nil {
		return nil, err
	}
	// A GHES release is logged once; nothing waits on it beyond its own short timeout.
	c.LogRelease(ctx)
	return github.NewHost(c, []byte(s.WebhookSecret), d.Log), nil
}

// newGitHubClient is a GitHub App client for one GitHub (github.com or a GHES): the
// App's id and key, the API, git and GraphQL bases, and an optional API version pin.
func newGitHubClient(d HostDeps, appID, keyFile, api, gitURL, graphQL, apiVersion string) (*github.Client, error) {
	s := d.Server
	pem, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read github key: %w", err)
	}
	key, err := github.ParsePrivateKey(pem)
	if err != nil {
		return nil, err
	}
	g := d.Git
	v := github.PinnedVersions(apiVersion)
	return &github.Client{
		APIBase: api, GitBase: gitURL, GraphQLURL: graphQL, UpstreamAPIBase: s.ActionsUpstreamURL, UpstreamToken: s.ActionsUpstreamToken, Versions: v, HTTP: d.HTTP, Log: d.Log,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		Tokens:     &github.AppAuth{AppID: appID, Key: key, APIBase: api, HTTP: d.HTTP, Versions: v},
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.github.com",
	}, nil
}
