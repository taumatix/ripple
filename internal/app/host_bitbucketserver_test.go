package app

import (
	"context"
	"testing"
)

func TestBuildBitbucketServerFromEnv(t *testing.T) {
	const u = "RIPPLE_BITBUCKET_SERVER_URL"
	build := func(env map[string]string) error {
		h, err := buildBitbucketServer(context.Background(), HostDeps{Getenv: func(k string) string { return env[k] }})
		if err == nil && env[u] != "" && (h == nil || h.Name != "bitbucket-server") {
			t.Errorf("host %+v", h)
		}
		if err == nil && env[u] == "" && h != nil {
			t.Error("unconfigured host built")
		}
		return err
	}
	if err := build(nil); err != nil {
		t.Errorf("unset: %v", err)
	}
	if err := build(map[string]string{u: "https://git.example/bitbucket"}); err == nil {
		t.Error("missing token and secret accepted")
	}
	if err := build(map[string]string{u: "https://git.example", "RIPPLE_BITBUCKET_SERVER_TOKEN": "t"}); err == nil {
		t.Error("missing secret accepted")
	}
	if err := build(map[string]string{u: "https://git.example", "RIPPLE_BITBUCKET_SERVER_TOKEN": "t", "RIPPLE_BITBUCKET_SERVER_WEBHOOK_SECRET": "s"}); err != nil {
		t.Error(err)
	}
}
