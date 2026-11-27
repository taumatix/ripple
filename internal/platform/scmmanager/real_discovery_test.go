//go:build scmreal

package scmmanager_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

const mtRepo = "application/vnd.scmm-repository+json;v=2"

// gitReal runs git against the instance with the API key as the basic-auth password.
func (s *scm) gitReal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("ripple:"+s.apiKey(t)))
	full := append([]string{"-c", "http.extraHeader=" + auth, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.ReplaceAll(string(out), s.apiKey(t), "<token>"))
	}
	return string(out)
}

// TestRealDiscovery records what a bootstrapped instance answers about repositories, webhooks and
// pull requests, driving the client under test where it exists.
func TestRealDiscovery(t *testing.T) {
	s := realSCM(t)
	key := s.apiKey(t)

	code, _, body := s.call(t, http.MethodGet, s.base+"/api/v2/", mtIndex, "", nil)
	var idx struct {
		Links links `json:"_links"`
	}
	_ = json.Unmarshal(body, &idx)
	t.Logf("FACT index %d, link names after the plugins: %v", code, idx.Links.names())
	for _, k := range []string{"webHookConfig", "pullRequestConfig", "workflowConfig", "gitConfig"} {
		t.Logf("FACT link %s: %s", k, idx.Links[k])
	}
	if href := idx.Links.href("webHookConfig"); href != "" {
		for _, accept := range []string{"application/vnd.scmm-webHookConfig+json;v=2", "application/vnd.scmm-webhookConfig+json;v=2", "application/json", "*/*"} {
			code, h, body := s.call(t, http.MethodGet, href, accept, "", nil)
			t.Logf("FACT GET webHookConfig accept=%q: %d %q %s", accept, code, h.Get("Content-Type"), clip(body))
			if code == http.StatusOK {
				break
			}
		}
	}

	// A repository with its first commit, created the way the UI does.
	code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/repositories?initialize=true", "", mtRepo,
		map[string]any{"namespace": "acme", "name": "probe", "type": "git", "contextEntries": map[string]any{}})
	t.Logf("FACT create repository with initialize=true: %d location=%q %s", code, h.Get("Location"), clip(body))
	if code != http.StatusCreated {
		t.Fatalf("creating the repository: %d", code)
	}

	c := &scmmanager.Client{BaseURL: s.base, Credentials: platform.StaticCredential{Secret: key}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repos, err := c.ListRepos(ctx)
	t.Logf("FACT ListRepos: %v err=%v", repos, err)
	heads, err := c.ListRepoHeads(ctx)
	t.Logf("FACT ListRepoHeads: %v err=%v", heads, err)
	def, err := c.DefaultBranch(ctx, "acme/probe")
	t.Logf("FACT DefaultBranch: %q err=%v", def, err)

	// Push a branch with real git, then open a pull request through the client.
	dir := t.TempDir()
	s.gitReal(t, dir, "clone", "-q", s.base+"/repo/acme/probe", ".")
	t.Logf("FACT clone with the API key as basic password: ok; branches: %s", strings.TrimSpace(s.gitReal(t, dir, "branch", "-a")))
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.gitReal(t, dir, "checkout", "-q", "-b", "ripple/probe")
	s.gitReal(t, dir, "add", "-A")
	s.gitReal(t, dir, "commit", "-q", "-m", "probe")
	s.gitReal(t, dir, "push", "-q", "origin", "ripple/probe")
	pr, err := c.CreatePR(ctx, model.Repo{FullName: "acme/probe", DefaultBranch: def}, platform.PRSpec{Head: "ripple/probe", Base: def, Title: "probe", Body: "probe"})
	t.Logf("FACT CreatePR: %+v err=%v", pr, err)
	prs, err := c.ListOpenPRs(ctx, model.Repo{FullName: "acme/probe"}, "ripple/")
	t.Logf("FACT ListOpenPRs: %+v err=%v", prs, err)
	_, err = c.CreatePR(ctx, model.Repo{FullName: "acme/probe", DefaultBranch: def}, platform.PRSpec{Head: "ripple/probe", Base: def, Title: "again", Body: "x"})
	t.Logf("FACT CreatePR for the same branches again: err=%v", err)
	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/pull-requests/acme/probe", "application/vnd.scmm-pullRequestCollection+json;v=2", "", nil)
	t.Logf("FACT pull request list: %d %s", code, clip(body))
}
