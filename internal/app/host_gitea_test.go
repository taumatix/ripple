package app

import (
	"context"
	"testing"
)

func TestBuildGiteaFromEnv(t *testing.T) {
	build := func(env map[string]string) error {
		h, err := buildGitea(context.Background(), HostDeps{Getenv: func(k string) string { return env[k] }})
		if err == nil && env["RIPPLE_GITEA_URL"] != "" && (h == nil || h.Name != "gitea") {
			t.Errorf("host %+v", h)
		}
		if err == nil && env["RIPPLE_GITEA_URL"] == "" && h != nil {
			t.Error("unconfigured host built")
		}
		return err
	}
	if err := build(nil); err != nil {
		t.Errorf("unset: %v", err)
	}
	if err := build(map[string]string{"RIPPLE_GITEA_URL": "https://git.example"}); err == nil {
		t.Error("missing token and secret accepted")
	}
	if err := build(map[string]string{"RIPPLE_GITEA_URL": "https://git.example", "RIPPLE_GITEA_TOKEN": "t", "RIPPLE_GITEA_WEBHOOK_SECRET": "s", "RIPPLE_GITEA_FLAVOUR": "bogus"}); err == nil {
		t.Error("bad flavour accepted")
	}
	if err := build(map[string]string{"RIPPLE_GITEA_URL": "https://git.example", "RIPPLE_GITEA_TOKEN": "t", "RIPPLE_GITEA_WEBHOOK_SECRET": "s", "RIPPLE_GITEA_FLAVOUR": "forgejo"}); err != nil {
		t.Error(err)
	}
}
