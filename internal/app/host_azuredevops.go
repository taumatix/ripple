package app

import (
	"context"
	"errors"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/azuredevops"
)

func init() { RegisterHost(azuredevops.HostName, buildAzureDevOps) }

// buildAzureDevOps is Azure DevOps Services from RIPPLE_AZUREDEVOPS_*; see
// docs/platform-azuredevops.md. It is nil, nil unless an organization is set.
func buildAzureDevOps(_ context.Context, d HostDeps) (*platform.Host, error) {
	org, pat := d.Getenv("RIPPLE_AZUREDEVOPS_ORG"), d.Getenv("RIPPLE_AZUREDEVOPS_PAT")
	if org == "" && pat == "" {
		return nil, nil
	}
	if org == "" || pat == "" {
		return nil, errors.New("RIPPLE_AZUREDEVOPS_ORG and RIPPLE_AZUREDEVOPS_PAT must be set together")
	}
	api := azureOrDefault(d.Getenv("RIPPLE_AZUREDEVOPS_API_URL"), "https://dev.azure.com")
	git := azureOrDefault(d.Getenv("RIPPLE_AZUREDEVOPS_GIT_URL"), api)
	g := d.Git
	c := &azuredevops.Client{
		APIBase: strings.TrimRight(api, "/"), GitBase: strings.TrimRight(git, "/"), Org: org,
		Credentials: platform.StaticCredential{Secret: pat}, HTTP: d.HTTP, Log: d.Log,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.invalid",
	}
	auth := azuredevops.BasicAuth{Username: azureOrDefault(d.Getenv("RIPPLE_AZUREDEVOPS_WEBHOOK_USER"), "ripple"), Password: d.Getenv("RIPPLE_AZUREDEVOPS_WEBHOOK_PASSWORD")}
	if auth.Password == "" {
		d.Log.Warn("RIPPLE_AZUREDEVOPS_WEBHOOK_PASSWORD is unset: every Azure DevOps webhook will be refused; repositories are found by listing only")
	}
	return azuredevops.NewHost(c, auth, d.Log), nil
}

func azureOrDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
