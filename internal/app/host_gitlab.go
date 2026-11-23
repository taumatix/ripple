package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
)

func init() { RegisterHost(gitlab.HostName, buildGitLab) }

// buildGitLab is the GitLab host from RIPPLE_GITLAB_*; see docs/platform-gitlab.md.
// It is off unless a token is set. A token with no way to authenticate webhooks is an
// error rather than a host whose receiver refuses everything.
func buildGitLab(_ context.Context, d HostDeps) (*platform.Host, error) {
	get := d.Getenv
	if get == nil {
		return nil, nil
	}
	tokens := gitlab.Tokens{Default: get("RIPPLE_GITLAB_TOKEN")}
	scoped, err := parseScopedTokens(get("RIPPLE_GITLAB_TOKENS"))
	if err != nil {
		return nil, fmt.Errorf("RIPPLE_GITLAB_TOKENS: %w", err)
	}
	tokens.Scoped = scoped
	if tokens.Default == "" && len(scoped) == 0 {
		return nil, nil
	}
	base := strings.TrimRight(get("RIPPLE_GITLAB_URL"), "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("RIPPLE_GITLAB_URL must be an http(s) URL such as https://gitlab.example.com")
	}
	r := gitlab.Receiver{SecretToken: get("RIPPLE_GITLAB_WEBHOOK_SECRET")}
	if s := get("RIPPLE_GITLAB_WEBHOOK_SIGNING_TOKEN"); s != "" {
		if r.SigningKey, err = gitlab.ParseSigningToken(s); err != nil {
			return nil, fmt.Errorf("RIPPLE_GITLAB_WEBHOOK_SIGNING_TOKEN: %w", err)
		}
	}
	if r.SecretToken == "" && len(r.SigningKey) == 0 {
		return nil, errors.New("GitLab is configured but neither RIPPLE_GITLAB_WEBHOOK_SECRET nor RIPPLE_GITLAB_WEBHOOK_SIGNING_TOKEN is set")
	}
	g := d.Git
	c := &gitlab.Client{BaseURL: base, GitBase: get("RIPPLE_GITLAB_GIT_URL"), Tokens: tokens, HTTP: d.HTTP, Log: d.Log,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.gitlab.invalid"}
	return gitlab.NewHost(c, r, d.Log), nil
}

// parseScopedTokens reads "group/sub=token,group/app=token": the token for a group
// or project is used for everything under it.
func parseScopedTokens(s string) (map[string]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, item := range strings.Split(s, ",") {
		scope, tok, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok || scope == "" || tok == "" {
			return nil, fmt.Errorf("want scope=token, got an entry without both")
		}
		if err := gitlab.CheckPath(scope + "/x"); err != nil {
			return nil, fmt.Errorf("scope %q is not a group or project path", scope)
		}
		if _, dup := out[scope]; dup {
			return nil, fmt.Errorf("scope %q given twice", scope)
		}
		out[scope] = tok
	}
	return out, nil
}
