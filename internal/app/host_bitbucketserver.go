package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucketserver"
)

func init() { RegisterHost(bitbucketserver.HostName, buildBitbucketServer) }

// buildBitbucketServer is the Bitbucket Server / Data Center host (not Bitbucket
// Cloud). It is configured by RIPPLE_BITBUCKET_SERVER_URL (the instance including any
// context path, e.g. https://git.example.org/bitbucket), RIPPLE_BITBUCKET_SERVER_TOKEN
// (an HTTP access token), RIPPLE_BITBUCKET_SERVER_WEBHOOK_SECRET and optionally
// RIPPLE_BITBUCKET_SERVER_USER (the user sent with the token: a personal token's
// owner; "x-token-auth" for a project or repository token; default "ripple").
// Without the URL the host is off.
func buildBitbucketServer(_ context.Context, d HostDeps) (*platform.Host, error) {
	base := strings.TrimRight(d.Getenv("RIPPLE_BITBUCKET_SERVER_URL"), "/")
	if base == "" {
		return nil, nil
	}
	token, secret := d.Getenv("RIPPLE_BITBUCKET_SERVER_TOKEN"), d.Getenv("RIPPLE_BITBUCKET_SERVER_WEBHOOK_SECRET")
	if token == "" || secret == "" {
		return nil, fmt.Errorf("RIPPLE_BITBUCKET_SERVER_URL is set, so RIPPLE_BITBUCKET_SERVER_TOKEN and RIPPLE_BITBUCKET_SERVER_WEBHOOK_SECRET are required")
	}
	user := d.Getenv("RIPPLE_BITBUCKET_SERVER_USER")
	if user == "" {
		user = "ripple"
	}
	g := d.Git
	c := &bitbucketserver.Client{
		APIBase: base, GitBase: base, HTTP: d.HTTP, Log: d.Log,
		Credentials: platform.StaticCredential{Username: user, Secret: token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@noreply.invalid",
	}
	return bitbucketserver.NewHost(c, []byte(secret), d.Log), nil
}
