package app

import (
	"context"
	"errors"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
)

func init() { RegisterHost(gerrit.HostName, buildGerrit) }

// buildGerrit is Gerrit from RIPPLE_GERRIT_*; see docs/platform-gerrit.md. It is
// nil, nil unless a URL is set. The webhook secret is required: the webhooks
// plugin does not sign deliveries, so the secret in the hook URL is the only gate.
func buildGerrit(_ context.Context, d HostDeps) (*platform.Host, error) {
	url, user, tok := d.Getenv("RIPPLE_GERRIT_URL"), d.Getenv("RIPPLE_GERRIT_USER"), d.Getenv("RIPPLE_GERRIT_TOKEN")
	if url == "" && tok == "" {
		return nil, nil
	}
	if url == "" || tok == "" || user == "" {
		return nil, errors.New("RIPPLE_GERRIT_URL, RIPPLE_GERRIT_USER and RIPPLE_GERRIT_TOKEN must be set together")
	}
	secret := d.Getenv("RIPPLE_GERRIT_WEBHOOK_SECRET")
	if secret == "" {
		return nil, errors.New("RIPPLE_GERRIT_WEBHOOK_SECRET is required: Gerrit webhooks are unsigned, so the secret in the hook URL is the only authentication")
	}
	g := d.Git
	c := &gerrit.Client{
		BaseURL: strings.TrimRight(url, "/"), ProjectPrefix: d.Getenv("RIPPLE_GERRIT_PROJECT_PREFIX"),
		Credentials: platform.StaticCredential{Username: user, Secret: tok}, HTTP: d.HTTP, Log: d.Log,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.invalid",
	}
	return gerrit.NewHost(c, []byte(secret), d.Log), nil
}
