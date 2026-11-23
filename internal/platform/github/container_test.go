package github_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/testutil"
)

// A command killed by its context leaves the container running (its client dies, the
// container does not), so the workspace must ask the runtime to kill and remove it.
func TestKilledContainerCommandIsReaped(t *testing.T) {
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/r", map[string]string{"go.mod": "module x\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() {}\n"})
	proxy := testutil.NewGoProxy(t)
	rt := testutil.NewFakeRuntime(t)
	sb, err := sandbox.DetectWith("docker", func(string) (string, error) { return rt.Bin, nil }, sandbox.ContainerOptions{Image: "img"})
	if err != nil {
		t.Fatal(err)
	}
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir(),
		GoProxy: proxy.URL, GoSumDB: "off", Sandbox: sb}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	rt.Hang(t)

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if _, err := ws.RunGo(ctx, ".", "build", "./..."); err == nil {
		t.Fatal("hung command reported success")
	}

	var name string
	for _, r := range rt.Runs() {
		for i, a := range r {
			if a == "--name" {
				name = r[i+1]
			}
		}
	}
	if name == "" {
		t.Fatalf("no container was started: %v", rt.Calls())
	}
	var killed, removed bool
	for _, call := range rt.Calls() {
		j := strings.Join(call, " ")
		killed = killed || j == "kill "+name
		removed = removed || j == "rm -f "+name
	}
	if !killed || !removed {
		t.Errorf("container %s not reaped (kill %v, rm %v): %v", name, killed, removed, rt.Calls())
	}
}
