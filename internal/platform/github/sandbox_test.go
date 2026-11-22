package github_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/testutil"
)

// recorder is a Backend that confines nothing and remembers what it was asked to wrap.
type recorder struct {
	mu    sync.Mutex
	calls []call
}

type call struct {
	p    sandbox.Policy
	argv []string
}

func (*recorder) Name() string { return "recorder" }
func (r *recorder) Wrap(p sandbox.Policy, argv []string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{p, argv})
	return argv, nil
}

func (r *recorder) find(sub string) []call {
	var out []call
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c.argv, " "), sub) {
			out = append(out, c)
		}
	}
	return out
}

func TestSandboxConfinesGoAndLocalGitButNotNetworkGit(t *testing.T) {
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/r", map[string]string{"go.mod": "module x\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() {}\n"})
	proxy := testutil.NewGoProxy(t)
	rec := &recorder{}
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir(),
		GoProxy: proxy.URL, GoSumDB: "off", Sandbox: rec}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if _, err := ws.RunGo(context.Background(), ".", "build", "./..."); err != nil {
		t.Fatal(err)
	}

	if n := len(rec.find(" clone ")); n != 0 {
		t.Errorf("network git was wrapped %d times; it holds the token and runs no repo code", n)
	}
	if err := ws.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rec.find(" checkout ")) != 1 {
		t.Errorf("local git not wrapped: %+v", rec.calls)
	}
	goCalls := rec.find(" build ./...")
	if len(goCalls) != 1 {
		t.Fatalf("go build wrapped %d times, want 1", len(goCalls))
	}
	if ports := goCalls[0].p.LoopbackPorts; len(ports) != 1 || ports[0] == 0 {
		t.Errorf("go policy ports = %v, want exactly the proxy gate", ports)
	}
	if strings.Contains(strings.Join(goCalls[0].p.ReadOnly, " "), "/Users") || strings.Contains(strings.Join(goCalls[0].p.ReadOnly, " "), "/home/") {
		t.Errorf("home directory readable: %v", goCalls[0].p.ReadOnly)
	}
}
