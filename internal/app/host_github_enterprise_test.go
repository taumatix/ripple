package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/testutil"
)

func buildSecondGitHub(t *testing.T, env map[string]string) (name string, err error) {
	t.Helper()
	srv, err := config.LoadServer(func(k string) string {
		return map[string]string{"RIPPLE_WEBHOOK_SECRET": "s", "RIPPLE_GITHUB_APP_ID": "1", "RIPPLE_GITHUB_KEY_FILE": "/dev/null"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := buildGitHubEnterprise(context.Background(), HostDeps{Server: srv, Getenv: func(k string) string { return env[k] },
		HTTP: http.DefaultClient, Log: slog.New(slog.DiscardHandler)})
	if err != nil || h == nil {
		return "", err
	}
	return h.Name, nil
}

func TestSecondGitHubHostIsOffWithoutItsURL(t *testing.T) {
	if name, err := buildSecondGitHub(t, map[string]string{"RIPPLE_GHES_APP_ID": "9"}); name != "" || err != nil {
		t.Errorf("host %q, err %v; want none", name, err)
	}
}

func TestSecondGitHubHostNeedsItsOwnApp(t *testing.T) {
	_, err := buildSecondGitHub(t, map[string]string{"RIPPLE_GHES_URL": "https://ghes.example.com"})
	if err == nil || !strings.Contains(err.Error(), "RIPPLE_GHES_APP_ID") {
		t.Errorf("err %v; want it to name the missing settings", err)
	}
}

func TestSecondGitHubHostRejectsABadURLAndVersion(t *testing.T) {
	key := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(key, testutil.NewGHES(t).KeyPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"RIPPLE_GHES_URL": "https://ghes.example.com", "RIPPLE_GHES_APP_ID": "9", "RIPPLE_GHES_KEY_FILE": key, "RIPPLE_GHES_WEBHOOK_SECRET": "x"}
	if name, err := buildSecondGitHub(t, base); err != nil || name != EnterpriseHostName {
		t.Fatalf("host %q, err %v", name, err)
	}
	for k, v := range map[string]string{"RIPPLE_GHES_URL": "http://ghes.example.com", "RIPPLE_GHES_API_VERSION": "latest"} {
		env := map[string]string{}
		for a, b := range base {
			env[a] = b
		}
		env[k] = v
		if _, err := buildSecondGitHub(t, env); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%q: err %v; want it named", k, v, err)
		}
	}
}
