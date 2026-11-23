package sandbox

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func wrapContainer(t *testing.T, fl Flavour, o ContainerOptions, p Policy, argv ...string) []string {
	t.Helper()
	o.Image = "img:1"
	b, err := Container(fl, "/usr/bin/rt", o)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Wrap(p, argv)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// has reports whether got contains the consecutive run of words.
func has(got []string, words ...string) bool {
	for i := 0; i+len(words) <= len(got); i++ {
		if strings.Join(got[i:i+len(words)], "\x00") == strings.Join(words, "\x00") {
			return true
		}
	}
	return false
}

var jobPolicy = Policy{Workdir: "/w/job/repo", Writable: []string{"/w/job", "/w/cache"},
	ReadOnly: []string{"/opt/go"}, Env: []string{"PATH=/host/bin", "HOME=/w/job", "GOPROXY=http://p", "TOKEN"}}

func TestContainerAppleArgv(t *testing.T) {
	got := wrapContainer(t, FlavourApple, ContainerOptions{}, jobPolicy, "/opt/go/bin/go", "build", "./...")
	uid := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	for _, want := range [][]string{
		{"run", "--rm", "--name"}, {"--progress", "none"}, {"--network", "none"}, {"--read-only"},
		{"--tmpfs", "/tmp"}, {"--cap-drop", "ALL"}, {"--user", uid}, {"-c", "2", "-m", "2G"},
		{"-v", "/w/job:/w/job"}, {"-v", "/w/cache:/w/cache"}, {"-w", "/w/job/repo"},
		{"-e", "HOME=/w/job"}, {"-e", "GOPROXY"}, {"img:1", "go", "build", "./..."},
	} {
		if !has(got, want...) {
			t.Errorf("argv lacks %v: %v", want, got)
		}
	}
	for _, bad := range []string{"/w/job/repo:/w/job/repo", "/opt/go:/opt/go", "--pull", "--pids-limit", "--security-opt"} {
		if has(got, bad) || strings.Contains(strings.Join(got, " "), bad) {
			t.Errorf("argv has %q: %v", bad, got)
		}
	}
	if has(got, "-e", "PATH") || strings.Contains(strings.Join(got, " "), "host/bin") {
		t.Errorf("host PATH leaked into the container: %v", got)
	}
}

func TestContainerDockerArgv(t *testing.T) {
	got := wrapContainer(t, FlavourDocker, ContainerOptions{CPUs: "1.5", Memory: "512M", Pull: "always", PIDs: 64}, jobPolicy, "git", "status")
	for _, want := range [][]string{
		{"--pull", "always"}, {"--pids-limit", "64"}, {"--security-opt", "no-new-privileges"},
		{"--cpus", "1.5", "--memory", "512M"}, {"--network", "none"}, {"img:1", "git", "status"},
	} {
		if !has(got, want...) {
			t.Errorf("argv lacks %v: %v", want, got)
		}
	}
	if has(got, "--progress", "none") || has(got, "-c", "1.5") {
		t.Errorf("Apple-only option on a docker argv: %v", got)
	}
}

func TestContainerNetworkOnlyWithAProxy(t *testing.T) {
	p := jobPolicy
	p.Proxy = "https://proxy.golang.org"
	if got := wrapContainer(t, FlavourDocker, ContainerOptions{}, p, "go", "build"); has(got, "--network", "none") || strings.Contains(strings.Join(got, " "), "--network") {
		t.Errorf("proxy policy still isolated or named a network by default: %v", got)
	}
	if got := wrapContainer(t, FlavourPodman, ContainerOptions{Network: "ripple-internal"}, p, "go", "build"); !has(got, "--network", "ripple-internal") {
		t.Errorf("configured network not used: %v", got)
	}
}

func TestContainerRefusesLoopbackGateDeniedPathsAndBadSettings(t *testing.T) {
	b, _ := Container(FlavourDocker, "rt", ContainerOptions{Image: "i"})
	if _, err := b.Wrap(Policy{Workdir: "/w", LoopbackPorts: []int{1}}, []string{"go"}); !errors.Is(err, ErrNetUnsupported) {
		t.Errorf("loopback ports: %v", err)
	}
	if _, err := b.Wrap(Policy{Workdir: "/w", Deny: []string{"/w/key.pem"}}, []string{"go"}); err == nil {
		t.Error("a denied path inside the mounted workdir was accepted")
	}
	if _, err := b.Wrap(Policy{Workdir: "relative"}, []string{"go"}); err == nil {
		t.Error("relative workdir accepted")
	}
	if _, err := Container(FlavourDocker, "rt", ContainerOptions{}); err == nil {
		t.Error("no image accepted")
	}
	if _, err := Container(FlavourApple, "rt", ContainerOptions{Image: "i", Pull: "always"}); err == nil {
		t.Error("apple accepted a pull policy its CLI lacks")
	}
	if _, err := Container(FlavourDocker, "rt", ContainerOptions{Image: "i", Pull: "sometimes"}); err == nil {
		t.Error("unknown pull policy accepted")
	}
}

func TestContainerNamesAreUnique(t *testing.T) {
	a := wrapContainer(t, FlavourDocker, ContainerOptions{}, jobPolicy, "go")
	b := wrapContainer(t, FlavourDocker, ContainerOptions{}, jobPolicy, "go")
	name := func(argv []string) string {
		for i, x := range argv {
			if x == "--name" {
				return argv[i+1]
			}
		}
		return ""
	}
	if name(a) == "" || name(a) == name(b) {
		t.Errorf("names %q %q", name(a), name(b))
	}
}

func TestDetectContainerNeedsBinaryAndImage(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	found := func(s string) (string, error) { return "/bin/" + s, nil }
	if _, err := DetectWith("docker", missing, ContainerOptions{Image: "i"}); err == nil {
		t.Error("missing binary accepted")
	}
	if _, err := DetectWith("podman", found, ContainerOptions{}); err == nil {
		t.Error("missing image accepted")
	}
	b, err := DetectWith("container", found, ContainerOptions{Image: "i"})
	if err != nil || b.Name() != "container" {
		t.Fatalf("%v %v", b, err)
	}
	if no, ok := b.(NetworkOwner); !ok || !no.OwnsNetwork() {
		t.Error("container backend does not own its network")
	}
}

// Rootless Podman maps the caller to root in the container, so --user uid:gid could not write the
// bind mounts: keep-id keeps the caller's own ids. Docker has no such option.
func TestContainerPodmanKeepsTheCallersIDs(t *testing.T) {
	if got := wrapContainer(t, FlavourPodman, ContainerOptions{}, jobPolicy, "go", "build"); !has(got, "--userns", "keep-id") {
		t.Errorf("podman argv lacks --userns keep-id: %v", got)
	}
	if got := wrapContainer(t, FlavourDocker, ContainerOptions{}, jobPolicy, "go", "build"); has(got, "--userns", "keep-id") {
		t.Errorf("docker argv has --userns keep-id: %v", got)
	}
}

// The job's HOME and TMPDIR are paths in the container, not the client's: they travel as
// explicit values (not secret), so the client can keep its own HOME and rootless Podman
// does not put its image storage in the job directory.
func TestContainerPassesHomeAndTmpdirExplicitly(t *testing.T) {
	p := jobPolicy
	p.Env = append([]string{"TMPDIR=/w/job/tmp"}, p.Env...)
	got := wrapContainer(t, FlavourPodman, ContainerOptions{}, p, "go", "build")
	for _, want := range [][]string{{"-e", "HOME=/w/job"}, {"-e", "TMPDIR=/w/job/tmp"}, {"-e", "GOPROXY"}} {
		if !has(got, want...) {
			t.Errorf("argv lacks %v: %v", want, got)
		}
	}
	if has(got, "-e", "HOME") || has(got, "-e", "TMPDIR") {
		t.Errorf("HOME or TMPDIR taken from the client's environment: %v", got)
	}
}

func clientEnvOf(t *testing.T, o ContainerOptions, job []string) map[string]string {
	t.Helper()
	o.Image = "img:1"
	b, err := Container(FlavourPodman, "/usr/bin/rt", o)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range b.(ClientEnver).ClientEnv(job) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return env
}

func TestContainerClientEnvKeepsTheClientsOwnHome(t *testing.T) {
	t.Setenv("HOME", "/home/server")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("DOCKER_HOST", "unix:///run/user/1000/docker.sock")
	t.Setenv("RIPPLE_GITHUB_KEY_FILE", "/secrets/app.pem")
	t.Setenv("GOPROXY", "http://host-env")
	env := clientEnvOf(t, ContainerOptions{}, []string{"PATH=/host/bin", "HOME=/w/job", "TMPDIR=/w/job", "GOPROXY=http://p", "GOFLAGS=-mod=mod"})
	if env["HOME"] != "/home/server" {
		t.Errorf("client HOME = %q, want the server's own", env["HOME"])
	}
	if env["TMPDIR"] == "/w/job" {
		t.Errorf("client TMPDIR is the job directory")
	}
	if env["XDG_RUNTIME_DIR"] != "/run/user/1000" || env["DOCKER_HOST"] == "" {
		t.Errorf("the client lost its runtime location: %v", env)
	}
	if env["GOPROXY"] != "http://p" || env["GOFLAGS"] != "-mod=mod" {
		t.Errorf("job variables missing: %v", env)
	}
	if _, ok := env["RIPPLE_GITHUB_KEY_FILE"]; ok {
		t.Errorf("an unrelated server variable reached the client: %v", env)
	}
}

func TestContainerClientEnvPointsGoproxyAtTheSidecar(t *testing.T) {
	env := clientEnvOf(t, ContainerOptions{ProxyURL: "http://side:3128"}, []string{"HOME=/w/job", "GOPROXY=https://user:tok@proxy.example"})
	if env["GOPROXY"] != "http://side:3128" {
		t.Errorf("GOPROXY = %q, want the sidecar and never the job's credentialed URL", env["GOPROXY"])
	}
}

// Policy.Image replaces the configured image for that one command, and an image that
// would be read as an option is refused.
func TestContainerPolicyImageOverridesTheDefault(t *testing.T) {
	p := jobPolicy
	p.Image = "runner:1.26"
	got := wrapContainer(t, FlavourDocker, ContainerOptions{}, p, "/opt/go/bin/go", "build")
	if !has(got, "runner:1.26", "go", "build") || has(got, "img:1") {
		t.Errorf("argv should run runner:1.26 only: %v", got)
	}
	b, err := Container(FlavourDocker, "/usr/bin/rt", ContainerOptions{Image: "img:1"})
	if err != nil {
		t.Fatal(err)
	}
	p.Image = "--privileged"
	if _, err := b.Wrap(p, []string{"go", "build"}); err == nil {
		t.Error("an option-shaped image was accepted")
	}
}
