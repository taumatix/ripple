package app

import (
	"context"
	"testing"
)

func TestBuildBitbucket(t *testing.T) {
	env := func(m map[string]string) HostDeps { return HostDeps{Getenv: func(k string) string { return m[k] }} }
	if h, err := buildBitbucket(context.Background(), env(nil)); h != nil || err != nil {
		t.Fatalf("unconfigured: host %v err %v, want nil, nil", h, err)
	}
	for name, m := range map[string]map[string]string{
		"no secret":     {"RIPPLE_BITBUCKET_TOKEN": "t", "RIPPLE_BITBUCKET_WORKSPACES": "acme"},
		"no workspaces": {"RIPPLE_BITBUCKET_TOKEN": "t", "RIPPLE_BITBUCKET_WEBHOOK_SECRET": "s"},
		"blank list":    {"RIPPLE_BITBUCKET_TOKEN": "t", "RIPPLE_BITBUCKET_WEBHOOK_SECRET": "s", "RIPPLE_BITBUCKET_WORKSPACES": " , "},
	} {
		if h, err := buildBitbucket(context.Background(), env(m)); err == nil || h != nil {
			t.Errorf("%s: host %v err %v, want an error", name, h, err)
		}
	}
	h, err := buildBitbucket(context.Background(), env(map[string]string{"RIPPLE_BITBUCKET_TOKEN": "t", "RIPPLE_BITBUCKET_WEBHOOK_SECRET": "s", "RIPPLE_BITBUCKET_WORKSPACES": "acme, other"}))
	if err != nil || h == nil || h.Name != "bitbucket" || h.Receiver == nil {
		t.Fatalf("configured: %v %v", h, err)
	}
}

func TestBitbucketIsRegistered(t *testing.T) {
	for _, n := range RegisteredHosts() {
		if n == "bitbucket" {
			return
		}
	}
	t.Fatal("bitbucket host not registered")
}
