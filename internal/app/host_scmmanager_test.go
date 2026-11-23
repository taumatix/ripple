package app

import (
	"context"
	"testing"
)

func TestBuildSCMManager(t *testing.T) {
	env := func(m map[string]string) HostDeps { return HostDeps{Getenv: func(k string) string { return m[k] }} }
	if h, err := buildSCMManager(context.Background(), env(nil)); h != nil || err != nil {
		t.Fatalf("unconfigured: host %v err %v, want nil, nil", h, err)
	}
	for name, m := range map[string]map[string]string{
		"no url":    {"RIPPLE_SCMMANAGER_TOKEN": "t", "RIPPLE_SCMMANAGER_WEBHOOK_SECRET": "s"},
		"no secret": {"RIPPLE_SCMMANAGER_TOKEN": "t", "RIPPLE_SCMMANAGER_URL": "https://scm.example.com/scm"},
	} {
		if h, err := buildSCMManager(context.Background(), env(m)); err == nil || h != nil {
			t.Errorf("%s: host %v err %v, want an error", name, h, err)
		}
	}
	h, err := buildSCMManager(context.Background(), env(map[string]string{"RIPPLE_SCMMANAGER_TOKEN": "t", "RIPPLE_SCMMANAGER_WEBHOOK_SECRET": "s", "RIPPLE_SCMMANAGER_URL": "https://scm.example.com/scm"}))
	if err != nil || h == nil || h.Name != "scm-manager" || h.Receiver == nil {
		t.Fatalf("configured: %v %v", h, err)
	}
}

func TestSCMManagerIsRegistered(t *testing.T) {
	for _, n := range RegisteredHosts() {
		if n == "scm-manager" {
			return
		}
	}
	t.Fatal("scm-manager host not registered")
}
