package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// TestDependencyUpdateWithContainerRunner drives the webhook-to-PR path with go and local
// git going through a container CLI. The CLI is a fake that records its argv and runs the
// command on the host, so this proves the engine, workspace and argv wiring end to end;
// it does not prove a real Docker or Podman accepts that argv (the Apple container path
// is exercised for real in internal/sandbox, when that CLI is installed).
func TestDependencyUpdateWithContainerRunner(t *testing.T) {
	for _, name := range []string{"container", "docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			rt := testutil.NewFakeRuntime(t)
			sb, err := sandbox.DetectWith(name, func(string) (string, error) { return rt.Bin, nil },
				sandbox.ContainerOptions{Image: "golang:test"})
			if err != nil {
				t.Fatal(err)
			}
			e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{sandbox: sb})
			baseRepos(e)
			start(t, e, "acme/uses-lib")

			e.clock.Advance(time.Minute)
			e.publish("example.com/lib", "v1.1.0")
			e.tick()

			prs := e.gh.PRs("acme/uses-lib")
			if len(prs) != 1 {
				t.Fatalf("want 1 PR, got %d\nlogs:\n%s", len(prs), e.logs.String())
			}
			got, _ := e.gh.FileAt("acme/uses-lib", prs[0].Head, "go.mod")
			if !strings.Contains(got, "example.com/lib v1.1.0") {
				t.Errorf("go.mod lacks the bump:\n%s", got)
			}

			var goSteps, gitSteps int
			for _, run := range rt.Runs() {
				joined := strings.Join(run, " ")
				if !strings.Contains(joined, " golang:test ") {
					t.Errorf("run without the configured image: %v", run)
				}
				switch {
				case strings.Contains(joined, " golang:test go "):
					goSteps++
					if strings.Contains(joined, "--network none") {
						t.Errorf("go needs the module proxy but got no network: %v", run)
					}
				case strings.Contains(joined, " golang:test git "):
					gitSteps++
					if !strings.Contains(joined, "--network none") {
						t.Errorf("local git got a network: %v", run)
					}
					for _, net := range []string{" clone ", " fetch ", " push "} {
						if strings.Contains(joined, net) {
							t.Errorf("network git ran in a container, where it would hold no token: %v", run)
						}
					}
				}
				if strings.Contains(joined, "Authorization") || strings.Contains(joined, "ghs_") {
					t.Errorf("a credential reached the container argv: %v", run)
				}
			}
			if goSteps < 3 || gitSteps == 0 {
				t.Errorf("go steps %d (want tidy, build, vet), git steps %d", goSteps, gitSteps)
			}
		})
	}
}
