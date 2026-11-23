package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/platform/gitlab"
)

func gitlabEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestGitLabIsRegisteredAndOffUntilConfigured(t *testing.T) {
	found := false
	for _, n := range RegisteredHosts() {
		found = found || n == gitlab.HostName
	}
	if !found {
		t.Fatal("gitlab host not registered")
	}
	h, err := buildGitLab(context.Background(), HostDeps{Getenv: gitlabEnv(nil)})
	if h != nil || err != nil {
		t.Errorf("unconfigured: host %v, err %v", h, err)
	}
}

func TestBuildGitLab(t *testing.T) {
	good := map[string]string{"RIPPLE_GITLAB_TOKEN": "glpat-a", "RIPPLE_GITLAB_WEBHOOK_SECRET": "s",
		"RIPPLE_GITLAB_TOKENS": "team/sub=glpat-b, team/app=glpat-c"}
	h, err := buildGitLab(context.Background(), HostDeps{Getenv: gitlabEnv(good)})
	if err != nil || h == nil || h.Name != "gitlab" || h.Receiver == nil {
		t.Fatalf("host %v, err %v", h, err)
	}
	// The receiver it built is authenticated with the secret.
	rec := httptest.NewRecorder()
	h.Receiver(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated delivery: %d", rec.Code)
	}

	scopedOnly := map[string]string{"RIPPLE_GITLAB_TOKENS": "g=glpat-x", "RIPPLE_GITLAB_WEBHOOK_SIGNING_TOKEN": "whsec_YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU="}
	if h, err := buildGitLab(context.Background(), HostDeps{Getenv: gitlabEnv(scopedOnly)}); err != nil || h == nil {
		t.Errorf("scoped tokens and a signing token: host %v, err %v", h, err)
	}

	for name, env := range map[string]map[string]string{
		"no webhook auth":   {"RIPPLE_GITLAB_TOKEN": "t"},
		"bad url":           {"RIPPLE_GITLAB_TOKEN": "t", "RIPPLE_GITLAB_WEBHOOK_SECRET": "s", "RIPPLE_GITLAB_URL": "ftp://x"},
		"bad signing token": {"RIPPLE_GITLAB_TOKEN": "t", "RIPPLE_GITLAB_WEBHOOK_SIGNING_TOKEN": "nope"},
		"malformed scoped":  {"RIPPLE_GITLAB_TOKENS": "nonsense", "RIPPLE_GITLAB_WEBHOOK_SECRET": "s"},
		"unsafe scope":      {"RIPPLE_GITLAB_TOKENS": "../x=t", "RIPPLE_GITLAB_WEBHOOK_SECRET": "s"},
		"duplicate scope":   {"RIPPLE_GITLAB_TOKENS": "g=a,g=b", "RIPPLE_GITLAB_WEBHOOK_SECRET": "s"},
	} {
		if h, err := buildGitLab(context.Background(), HostDeps{Getenv: gitlabEnv(env)}); err == nil {
			t.Errorf("%s: accepted (host %v)", name, h)
		}
	}
}
