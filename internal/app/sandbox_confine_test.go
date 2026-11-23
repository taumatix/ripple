package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/testutil"
)

func confineServer(sandboxName string) *config.Server {
	return &config.Server{Sandbox: sandboxName, ContainerImage: "img:1", ContainerConfine: true, GoProxy: "https://proxy.golang.org"}
}

func calls(rt *testutil.FakeRuntime) []string {
	var out []string
	for _, c := range rt.Calls() {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func indexOf(cs []string, sub string) int {
	for i, c := range cs {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func newTestSandbox(t *testing.T, s *config.Server, rt *testutil.FakeRuntime) (sandbox.Backend, func(), error) {
	t.Helper()
	return newSandbox(context.Background(), s, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string) (string, error) { return rt.Bin, nil })
}

func TestNewSandboxConfinesTheContainerNetwork(t *testing.T) {
	rt := testutil.NewFakeRuntime(t)
	b, closeSB, err := newTestSandbox(t, confineServer("podman"), rt)
	if err != nil {
		t.Fatal(err)
	}
	cs := calls(rt)
	left, create := indexOf(cs, "network rm "+confineName), indexOf(cs, "network create --internal --label ripple.job=1 "+confineName)
	if left < 0 || create < 0 || left > create {
		t.Errorf("a leftover confinement is removed first, then the network is created: %v", cs)
	}
	if !strings.Contains(strings.Join(cs, "\n"), "proxy_pass https://proxy.golang.org;") {
		t.Errorf("the sidecar does not forward to RIPPLE_GOPROXY: %v", cs)
	}

	argv, err := b.Wrap(sandbox.Policy{Workdir: t.TempDir(), Env: []string{"GOPROXY=https://proxy.golang.org"}, Proxy: "https://proxy.golang.org"}, []string{"go", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "--network "+confineName+" ") {
		t.Errorf("commands that need the proxy do not join the confined network: %v", argv)
	}
	env := b.(sandbox.ClientEnver).ClientEnv([]string{"GOPROXY=https://proxy.golang.org"})
	if !contains(env, "GOPROXY=http://"+confineName+"-proxy:3128") {
		t.Errorf("GOPROXY in the container is not the sidecar: %v", env)
	}

	closeSB()
	closeSB() // idempotent
	if cs := calls(rt); indexOf(cs[len(cs)-3:], "rm -f "+confineName+"-proxy") < 0 || indexOf(cs[len(cs)-3:], "network rm "+confineName) < 0 {
		t.Errorf("the confinement is not removed on close: %v", cs)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestNewSandboxWithoutConfineLeavesTheRuntimeAlone(t *testing.T) {
	rt := testutil.NewFakeRuntime(t)
	s := confineServer("docker")
	s.ContainerConfine = false
	b, closeSB, err := newTestSandbox(t, s, rt)
	if err != nil {
		t.Fatal(err)
	}
	closeSB()
	if len(rt.Calls()) != 0 {
		t.Errorf("the runtime was called: %v", calls(rt))
	}
	if _, ok := b.(sandbox.ClientEnver); !ok {
		t.Error("container backend lost its client environment")
	}
}

func TestNewSandboxConfineFailureIsAnError(t *testing.T) {
	_, _, err := newSandbox(context.Background(), confineServer("docker"), slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(string) (string, error) { return "/nonexistent/runtime", nil })
	if err == nil || !strings.Contains(err.Error(), "RIPPLE_CONTAINER_CONFINE") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfineNameForAReplica(t *testing.T) {
	long := strings.Repeat("x", 64)
	for replica, want := range map[string]string{"": confineName, "a": confineName + "-a", "node-1.eu_west": confineName + "-node-1.eu_west"} {
		if got := confineNameFor(replica); got != want {
			t.Errorf("confineNameFor(%q) = %q, want %q", replica, got, want)
		}
	}
	seen := map[string]string{}
	for _, replica := range []string{long, long + "y", "a b", "a/b", "a:b", "é"} {
		got := confineNameFor(replica)
		if !strings.HasPrefix(got, confineName+"-") || len(got)+len("-proxy") > 63 || strings.ContainsAny(got, " /:é") {
			t.Errorf("confineNameFor(%q) = %q: not a runtime name", replica, got)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("replicas %q and %q share %q", replica, other, got)
		}
		seen[got] = replica
	}
}

// Two servers on one container runtime, each with its own RIPPLE_REPLICA, each confine and remove their own
// network: starting or stopping one never touches the other's.
func TestNewSandboxNamesTheNetworkPerReplica(t *testing.T) {
	rt := testutil.NewFakeRuntime(t)
	s := confineServer("podman")
	s.Replica = "east"
	_, closeEast, err := newTestSandbox(t, s, rt)
	if err != nil {
		t.Fatal(err)
	}
	s2 := confineServer("podman")
	s2.Replica = "west"
	_, closeWest, err := newTestSandbox(t, s2, rt)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(calls(rt), "\n")
	for _, want := range []string{"network create --internal --label ripple.job=1 " + confineName + "-east", "network create --internal --label ripple.job=1 " + confineName + "-west",
		"ps -aq --filter network=" + confineName + "-east"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in the runtime calls:\n%s", want, all)
		}
	}
	if strings.Contains(all, "network rm "+confineName+"\n") {
		t.Errorf("a replica's start removed the unnamed server's network:\n%s", all)
	}
	closeEast()
	closeWest()
}
