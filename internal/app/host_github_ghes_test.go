package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
	"github.com/taumatix/ripple/internal/webhook"
)

type sinkFunc func(platform.Event)

func (f sinkFunc) HandleEvent(_ context.Context, e platform.Event) error { f(e); return nil }

// buildGHES builds the GitHub host from the environment a GHES operator sets and
// serves a fake GHES that answers under /api/v3 and /api/graphql only.
func buildGHES(t *testing.T, h *testutil.GHES, extra map[string]string) *platform.Host {
	t.Helper()
	return buildGHESLog(t, h, extra, slog.New(slog.DiscardHandler))
}

func buildGHESLog(t *testing.T, h *testutil.GHES, extra map[string]string, log *slog.Logger) *platform.Host {
	t.Helper()
	key := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(key, h.KeyPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"RIPPLE_WEBHOOK_SECRET": "s3cret", "RIPPLE_GITHUB_APP_ID": testutil.AppID, "RIPPLE_GITHUB_KEY_FILE": key,
		"RIPPLE_GITHUB_ENTERPRISE_URL": h.URL}
	for k, v := range extra {
		env[k] = v
	}
	srv, err := config.LoadServer(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	host, err := buildGitHub(context.Background(), HostDeps{Server: srv, HTTP: http.DefaultClient, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	// the fake serves repositories from a local directory
	host.Platform.(*github.Client).GitBase = h.GitBase
	return host
}

func TestGitHubHostFromEnterpriseURLTalksOnlyToApiPrefixes(t *testing.T) {
	h := testutil.NewGHES(t)
	h.AddRepo("acme/r", map[string]string{"go.mod": "module x\n"})
	h.CommitOn("acme/r", "ripple/x", map[string]string{"x.txt": "x\n"})
	host := buildGHES(t, h, nil)
	ctx := context.Background()
	repo := model.Repo{FullName: "acme/r", InstallationID: 7, DefaultBranch: "main"}
	pr, err := host.Platform.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.RequestAutoMerge(ctx, host.Platform, repo, pr); err != nil {
		t.Fatal(err)
	}
	if prs := h.PRs("acme/r"); len(prs) != 1 || !prs[0].AutoMerge {
		t.Fatalf("PRs %+v", prs)
	}
	if len(h.Strays()) != 0 || h.GraphQLCalls() != 1 {
		t.Errorf("strays %v, graphql calls %d", h.Strays(), h.GraphQLCalls())
	}
}

func TestGitHubHostLowersTheVersionOnAnOlderGHES(t *testing.T) {
	h := testutil.NewGHES(t)
	h.Versions = []string{github.FallbackAPIVersion}
	h.AddRepo("acme/r", map[string]string{"go.mod": "module x\n"})
	h.CommitOn("acme/r", "ripple/x", map[string]string{"x.txt": "x\n"})
	host := buildGHES(t, h, nil)
	repo := model.Repo{FullName: "acme/r", InstallationID: 7, DefaultBranch: "main"}
	if _, err := host.Platform.CreatePR(context.Background(), repo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if h.Gone() != 1 {
		t.Errorf("410s = %d, want 1", h.Gone())
	}
	// pinning the older version means no 410 at all
	h2 := testutil.NewGHES(t)
	h2.Versions = []string{github.FallbackAPIVersion}
	h2.AddRepo("acme/r", map[string]string{"go.mod": "module x\n"})
	h2.CommitOn("acme/r", "ripple/x", map[string]string{"x.txt": "x\n"})
	host2 := buildGHES(t, h2, map[string]string{"RIPPLE_GITHUB_API_VERSION": github.FallbackAPIVersion})
	if _, err := host2.Platform.CreatePR(context.Background(), repo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"}); err != nil || h2.Gone() != 0 {
		t.Fatalf("pinned: err %v, 410s %d", err, h2.Gone())
	}
}

// A GHES delivery carries extra headers and an SHA-1 signature next to the SHA-256
// one; the receiver authenticates with SHA-256 and ignores the rest.
func TestGitHubHostReceivesAnEnterpriseDelivery(t *testing.T) {
	h := testutil.NewGHES(t)
	host := buildGHES(t, h, nil)
	var got []platform.Event
	srv := httptest.NewServer(host.Receiver(sinkFunc(func(e platform.Event) { got = append(got, e) })))
	defer srv.Close()
	body := []byte(`{"action":"created","installation":{"id":7},"repositories":[{"full_name":"acme/r"}]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "installation")
	req.Header.Set("X-GitHub-Delivery", "d-1")
	req.Header.Set("X-Hub-Signature-256", webhook.Sign([]byte("s3cret"), body))
	req.Header.Set("X-Hub-Signature", "sha1=0000000000000000000000000000000000000000")
	req.Header.Set("User-Agent", "GitHub-Hookshot/abc1234")
	req.Header.Set("X-GitHub-Enterprise-Version", "3.22.0")
	req.Header.Set("X-GitHub-Enterprise-Host", "ghes.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || len(got) != 1 || got[0].Scope != 7 || len(got[0].Repos) != 1 {
		t.Fatalf("status %d, events %+v", resp.StatusCode, got)
	}
}

// Starting against a GHES reads its release once and logs it; the release changes nothing else.
func TestGitHubHostLogsTheGHESReleaseOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		release string
		status  int
		level   string
		contain string
	}{
		"supported": {"3.14.2", 0, "level=INFO", "3.14.2"},
		"too old":   {"3.7.0", 0, "level=WARN", "minimum"},
		"meta down": {"", http.StatusInternalServerError, "level=WARN", "could not read"},
	} {
		t.Run(name, func(t *testing.T) {
			h := testutil.NewGHES(t)
			h.Release, h.MetaStatus = tc.release, tc.status
			var logs bytes.Buffer
			host := buildGHESLog(t, h, nil, slog.New(slog.NewTextHandler(&logs, nil)))
			if host == nil {
				t.Fatal("start was blocked")
			}
			if h.MetaCalls() != 1 {
				t.Errorf("meta requests = %d, want 1", h.MetaCalls())
			}
			if out := logs.String(); !bytes.Contains([]byte(out), []byte(tc.level)) || !bytes.Contains([]byte(out), []byte(tc.contain)) {
				t.Errorf("log %q lacks %s / %s", out, tc.level, tc.contain)
			}
			if len(h.Strays()) != 0 {
				t.Errorf("strays %v", h.Strays())
			}
		})
	}
}
