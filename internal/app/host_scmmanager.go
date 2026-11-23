package app

import (
	"context"
	"errors"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

func init() { RegisterHost(scmmanager.HostName, buildSCMManager) }

// buildSCMManager is SCM-Manager from RIPPLE_SCMMANAGER_*: URL (the instance's context
// path, e.g. https://scm.example.com/scm), TOKEN (an API key) and WEBHOOK_SECRET (the
// value of the webhook's X-Ripple-Webhook-Secret header). It returns nil, nil when TOKEN
// is unset.
func buildSCMManager(_ context.Context, d HostDeps) (*platform.Host, error) {
	token := d.Getenv("RIPPLE_SCMMANAGER_TOKEN")
	if token == "" {
		return nil, nil
	}
	base := d.Getenv("RIPPLE_SCMMANAGER_URL")
	if base == "" {
		return nil, errors.New("RIPPLE_SCMMANAGER_URL is required: SCM-Manager is self-hosted, e.g. https://scm.example.com/scm")
	}
	secret := d.Getenv("RIPPLE_SCMMANAGER_WEBHOOK_SECRET")
	if secret == "" {
		return nil, errors.New("RIPPLE_SCMMANAGER_WEBHOOK_SECRET is required: the webhook plugin cannot sign deliveries, so ripple authenticates them with a secret header")
	}
	g := d.Git
	c := &scmmanager.Client{
		BaseURL: base, HTTP: d.HTTP, Log: d.Log,
		Credentials: platform.StaticCredential{Username: scmmanager.GitUsername, Secret: token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.scm-manager.invalid",
	}
	return scmmanager.NewHost(c, []byte(secret), d.Log), nil
}
