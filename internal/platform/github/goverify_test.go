package github_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/testutil"
)

const tok = "tok-unit-9c1e-secret"

func goCheckout(t *testing.T, proxy string, sb sandbox.Backend) platform.Workspace {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/r", map[string]string{"go.mod": "module x\n\ngo 1.22\n", "main.go": "package main\n\nfunc main() {}\n"})
	remote := gh.ServeGit()
	c := &github.Client{APIBase: gh.APIURL, GitBase: remote.URL, Tokens: gh.Auth, WorkDir: t.TempDir(), GoSumDB: "off", GoProxy: proxy, Sandbox: sb}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// Whatever the step, the go process's own environment never holds the proxy credential
// when the gate carries it; build and vet are not even offered it.
func TestProxyCredentialNeverInGoEnvironment(t *testing.T) {
	px := testutil.NewGoProxy(t)
	px.RequireAuth("ripple", tok)
	goEnv := shim(t, "go")
	ws := goCheckout(t, px.URL, nil)
	ctx := platform.WithGoVerify(context.Background(), platform.GoVerify{Username: "ripple", Token: tok, Credentialed: true, Private: []string{"example.com/*"}})
	for _, a := range [][]string{{"mod", "tidy"}, {"build", "./..."}, {"vet", "./..."}} {
		if out, err := ws.RunGo(ctx, ".", a...); err != nil {
			t.Fatalf("go %v: %v\n%s", a, err, out)
		}
	}
	b, _ := os.ReadFile(goEnv)
	if strings.Contains(string(b), tok) {
		t.Errorf("credential in go's environment or argv:\n%s", b)
	}
	if !strings.Contains(string(b), "GONOSUMDB=example.com/*") {
		t.Errorf("private modules not excluded from the checksum database:\n%s", b)
	}
	if !strings.Contains(string(b), "CGO_ENABLED=0") {
		t.Errorf("cgo must stay off by default:\n%s", b)
	}
}

type ownsNet struct{}

func (ownsNet) Name() string                                        { return "container" }
func (ownsNet) Wrap(_ sandbox.Policy, a []string) ([]string, error) { return a, nil }
func (ownsNet) OwnsNetwork() bool                                   { return true }

// A container has to authenticate itself with GOPROXY userinfo, which go sends only
// over https: a plain-http proxy is refused before anything runs, and the error does
// not repeat the secret.
func TestContainerFlavourRefusesCredentialOverPlainHTTP(t *testing.T) {
	goEnv := shim(t, "go")
	ws := goCheckout(t, "http://proxy.corp.example.com", ownsNet{})
	ctx := platform.WithGoVerify(context.Background(), platform.GoVerify{Username: "ripple", Token: tok, Credentialed: true})
	out, err := ws.RunGo(ctx, ".", "mod", "tidy")
	if err == nil {
		t.Fatal("credential sent over plain http")
	}
	if strings.Contains(err.Error()+out, tok) {
		t.Errorf("secret in error: %v %s", err, out)
	}
	if b, _ := os.ReadFile(goEnv); strings.Contains(string(b), "mod tidy") {
		t.Errorf("go ran despite the refusal:\n%s", b)
	}
}

func TestCgoWithoutCompilerIsErrNoCompiler(t *testing.T) {
	ws := goCheckout(t, "off", nil)
	ctx := platform.WithGoVerify(context.Background(), platform.GoVerify{CGO: true, CC: "ripple-no-such-cc"})
	_, err := ws.RunGo(ctx, ".", "build", "./...")
	if !errors.Is(err, platform.ErrNoCompiler) {
		t.Fatalf("err = %v", err)
	}
}
