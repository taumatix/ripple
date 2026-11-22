package github_test

import (
	"context"
	"crypto/rsa"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

func checkout(t *testing.T) (*testutil.GitHub, platform.Workspace) {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/r", map[string]string{"go.mod": "module x\n", "sub/a.txt": "a\n"})
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir()}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return gh, ws
}

func TestCloneEditPush(t *testing.T) {
	gh, ws := checkout(t)
	b, err := ws.ReadFile("go.mod")
	if err != nil || string(b) != "module x\n" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	if err := ws.WriteFile("go.mod", []byte("module y\n")); err != nil {
		t.Fatal(err)
	}
	pushed, err := ws.CommitAndPush(context.Background(), "ripple/go/test", "msg")
	if err != nil || !pushed {
		t.Fatalf("CommitAndPush = %v, %v", pushed, err)
	}
	if got, _ := gh.FileAt("acme/r", "ripple/go/test", "go.mod"); got != "module y\n" {
		t.Errorf("pushed go.mod = %q", got)
	}
	if got, _ := gh.FileAt("acme/r", "main", "go.mod"); got != "module x\n" {
		t.Errorf("main was modified: %q", got)
	}
}

func TestNoChangeIsNotPushed(t *testing.T) {
	_, ws := checkout(t)
	pushed, err := ws.CommitAndPush(context.Background(), "ripple/go/none", "msg")
	if err != nil || pushed {
		t.Fatalf("pushed=%v err=%v, want false,nil", pushed, err)
	}
}

func TestPathTraversalRefused(t *testing.T) {
	_, ws := checkout(t)
	for _, p := range []string{"../x", "/etc/passwd", "a/../../x", ".git/config", ".git/hooks/pre-commit", "", "."} {
		if err := ws.WriteFile(p, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) succeeded", p)
		}
		if _, err := ws.ReadFile(p); err == nil {
			t.Errorf("ReadFile(%q) succeeded", p)
		}
	}
}

func TestSymlinkEscapeRefused(t *testing.T) {
	gh := testutil.NewGitHub(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("acme/r", map[string]string{"go.mod": "module x\n"})
	// Commit a symlink pointing outside the checkout, as a hostile repo could.
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("clone", "-q", strings.TrimPrefix(gh.GitBase, "file://")+"/acme/r.git", ".")
	if err := os.Symlink(outside, filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	run("add", "link")
	run("commit", "-q", "-m", "link")
	run("push", "-q", "origin", "HEAD:main")

	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir()}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if b, err := ws.ReadFile("link"); err == nil {
		t.Errorf("read through symlink escaped: %q", b)
	}
	if err := ws.WriteFile("link", []byte("pwned")); err == nil {
		t.Error("write through symlink succeeded")
	}
	if b, _ := os.ReadFile(outside); string(b) != "TOP SECRET" {
		t.Errorf("file outside the checkout changed: %q", b)
	}
}

func TestUnsafeBranchesRefused(t *testing.T) {
	_, ws := checkout(t)
	if err := ws.WriteFile("go.mod", []byte("module z\n")); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"main", "--delete", "-f", "ripple/../main", "ripple/x y", "ripple/x;rm", "refs/heads/main", "ripple/", "ripple/$(id)", "ripple/a..b"} {
		if _, err := ws.CommitAndPush(context.Background(), b, "m"); err == nil {
			t.Errorf("branch %q accepted", b)
		}
	}
}

func TestRepoNameValidated(t *testing.T) {
	gh := testutil.NewGitHub(t)
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir()}
	for _, n := range []string{"../etc/passwd", "a/b/c", "acme/--upload-pack=x", "acme/", "/r", "a b/c", "acme/r;id"} {
		if _, err := c.Checkout(context.Background(), model.Repo{FullName: n, InstallationID: 1, DefaultBranch: "main"}); err == nil {
			t.Errorf("repo %q accepted", n)
		}
	}
}

func TestJWTLiteralClaims(t *testing.T) {
	gh := testutil.NewGitHub(t)
	now := time.Unix(1_800_000_000, 0)
	a := &github.AppAuth{AppID: "1", Key: mustKey(t, gh), Now: func() time.Time { return now }}
	tok, err := a.JWT()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	// {"alg":"RS256","typ":"JWT"} base64url without padding.
	if parts[0] != "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9" {
		t.Errorf("header = %s", parts[0])
	}
}

func mustKey(t *testing.T, gh *testutil.GitHub) *rsa.PrivateKey {
	t.Helper()
	k, err := github.ParsePrivateKey(gh.KeyPEM())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func checkoutWith(t *testing.T, files map[string]string) platform.Workspace {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/r", files)
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir(), GoSumDB: "off", GoProxy: "off"}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func TestRunGoRefusesUnsafeCheckouts(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("UNTOUCHED"), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	const mod = "module x\n\ngo 1.22\n"
	for name, files := range map[string]map[string]string{
		"symlinked go.sum":               {"go.mod": mod, "go.sum": "symlink:" + outside},
		"symlinked go.mod":               {"go.mod": "symlink:" + outside},
		"symlinked go.work":              {"go.mod": mod, "go.work": "symlink:" + outside},
		"symlinked .go":                  {"go.mod": mod, "a.go": "symlink:" + outside},
		"nested symlinked":               {"go.mod": mod, "pkg/deep/b.go": "symlink:" + outside},
		"absolute replace":               {"go.mod": mod + "\nreplace example.com/y => " + outDir + "\n"},
		"parent replace":                 {"go.mod": mod + "\nreplace example.com/y => ../../..\n"},
		"nested module absolute replace": {"go.mod": mod, "sub/go.mod": mod + "\nreplace example.com/y => " + outDir + "\n"},
		"replace through symlink dir":    {"go.mod": mod + "\nreplace example.com/y => ./link\n", "link": "symlink:" + outDir},
	} {
		t.Run(name, func(t *testing.T) {
			ws := checkoutWith(t, files)
			for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}, {"vet", "./..."}} {
				out, err := ws.RunGo(context.Background(), ".", args...)
				if !errors.Is(err, platform.ErrUnsafeCheckout) {
					t.Fatalf("go %v: err = %v (out %q), want ErrUnsafeCheckout", args, err, out)
				}
			}
			if b, _ := os.ReadFile(outside); string(b) != "UNTOUCHED" {
				t.Errorf("file outside the checkout changed: %q", b)
			}
		})
	}
}

func TestRunGoAllowsLocalReplaceInsideCheckout(t *testing.T) {
	ws := checkoutWith(t, map[string]string{
		"go.mod":               "module x\n\ngo 1.22\n\nrequire example.com/y v0.0.0\n\nreplace example.com/y => ./third_party/y\n",
		"main.go":              "package main\n\nimport _ \"example.com/y\"\n\nfunc main() {}\n",
		"third_party/y/go.mod": "module example.com/y\n\ngo 1.22\n",
		"third_party/y/y.go":   "package y\n",
	})
	if out, err := ws.RunGo(context.Background(), ".", "build", "./..."); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
