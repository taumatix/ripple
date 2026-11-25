//go:build realtool

// The real-tool tests apply ripple's edit to a small, genuine project and then run that
// project's own toolchain on the result, so an edit is judged by the tool that will
// consume it and not only against text. They need the ecosystem's tool and the public
// registry, and they fail, never skip, when the tool is missing. See docs/realtool.md.
package manager

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bump is one dependency edit: the package as the manager names it, the text of the old
// version as it stands in the file, and the new version handed to Apply.
type bump struct{ pkg, from, to string }

// realtool describes one fixture: a directory under testdata/realtool, the manager that
// owns its manifest, the edits to make, and the commands that must pass before (on the
// untouched fixture, proving it is a genuine project) and after.
type realtool struct {
	fixture  string
	manifest string // path of the file ripple edits, relative to the fixture root
	manager  Manager
	bumps    []bump
	env      []string   // extra KEY=VALUE for the commands
	steps    [][]string // run in order, in the project directory
	// after runs once the steps pass on the updated copy.
	after func(t *testing.T, dir string)
}

func (r realtool) run(t *testing.T) {
	t.Helper()
	for _, s := range r.steps {
		if _, err := exec.LookPath(s[0]); err != nil {
			t.Fatalf("%s is not installed: the real-tool tests need it (CI installs it); not skipping", s[0])
		}
	}
	src := filepath.Join("testdata", "realtool", r.fixture)
	before, after := t.TempDir(), t.TempDir()
	copyTree(t, src, before)
	copyTree(t, src, after)

	r.steps0(t, before, "fixture as committed")

	manifest := filepath.Join(after, r.manifest)
	old, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !r.manager.Matches(r.manifest) {
		t.Fatalf("%s does not match %s", r.manager.Name(), r.manifest)
	}
	deps, err := r.manager.Extract(r.manifest, old)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	content := old
	for _, b := range r.bumps {
		found := false
		for _, d := range deps {
			if d.Package != b.pkg {
				continue
			}
			found = true
			if content, err = r.manager.Apply(content, d, d.Package, b.to); err != nil {
				t.Fatalf("apply %s -> %s: %v", b.pkg, b.to, err)
			}
		}
		if !found {
			t.Fatalf("%s not extracted from %s; got %+v", b.pkg, r.manifest, deps)
		}
	}
	assertOnlyBumped(t, string(old), string(content), r.bumps)
	if err := os.WriteFile(manifest, content, 0o644); err != nil {
		t.Fatal(err)
	}
	r.steps0(t, after, "fixture after ripple's edit")
	if r.after != nil {
		r.after(t, after)
	}
}

func (r realtool) steps0(t *testing.T, dir, what string) {
	t.Helper()
	for _, s := range r.steps {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		cmd := exec.CommandContext(ctx, s[0], s[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), r.env...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		cancel()
		if err != nil {
			t.Fatalf("%s: `%s` failed: %v\n%s", what, strings.Join(s, " "), err, out.String())
		}
		t.Logf("%s: `%s` ok", what, strings.Join(s, " "))
	}
}

// assertOnlyBumped checks the edit is offset-keeping: every line is as it was, except
// the lines that carried a bumped version, which differ by that version only.
func assertOnlyBumped(t *testing.T, old, got string, bumps []bump) {
	t.Helper()
	ol, gl := strings.Split(old, "\n"), strings.Split(got, "\n")
	if len(ol) != len(gl) {
		t.Fatalf("line count changed %d -> %d:\n%s", len(ol), len(gl), got)
	}
	changed := 0
	for i := range ol {
		if ol[i] == gl[i] {
			continue
		}
		changed++
		ok := false
		for _, b := range bumps {
			if strings.Contains(ol[i], b.from) && gl[i] == strings.Replace(ol[i], b.from, b.to, 1) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("line %d changed beyond a version bump:\n- %s\n+ %s", i+1, ol[i], gl[i])
		}
	}
	if changed != len(bumps) {
		t.Errorf("%d lines changed, want %d (one per bump)\n%s", changed, len(bumps), got)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustContain(t *testing.T, dir, file, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("%s does not contain %q:\n%s", file, want, b)
	}
}

func TestRealtoolGoMod(t *testing.T) {
	realtool{
		fixture: "gomod", manifest: "go.mod", manager: GoMod{Dir: "/"},
		bumps: []bump{{"github.com/google/uuid", "v1.3.0", "v1.6.0"}},
		env:   []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod"},
		// go.sum is not ripple's edit to make here (the gitws step runs go mod tidy), so
		// the tool refreshes it, then builds and verifies the module cache.
		steps: [][]string{{"go", "mod", "tidy"}, {"go", "build", "./..."}, {"go", "mod", "verify"}},
		after: func(t *testing.T, dir string) { mustContain(t, dir, "go.sum", "github.com/google/uuid v1.6.0 h1:") },
	}.run(t)
}

func TestRealtoolCargo(t *testing.T) {
	realtool{
		fixture: "cargo", manifest: "Cargo.toml", manager: Cargo{Dir: "/"},
		bumps: []bump{{"itoa", "1.0.1", "1.0.15"}},
		// No Cargo.lock is committed: ripple never writes one, so the tool's own resolve
		// (metadata, then a real compile) is what accepts the new requirement.
		steps: [][]string{{"cargo", "metadata", "--format-version", "1"}, {"cargo", "check"}},
		// "1.0.15" is a caret requirement: the lock resolves to the newest compatible release, so
		// only its presence is asserted, and the compile above proves it satisfies the manifest.
		after: func(t *testing.T, dir string) { mustContain(t, dir, "Cargo.lock", `name = "itoa"`) },
	}.run(t)
}

func TestRealtoolNpm(t *testing.T) {
	realtool{
		fixture: "npm", manifest: "package.json", manager: Npm{Dir: "/"},
		bumps: []bump{{"ms", "2.1.2", "2.1.3"}},
		steps: [][]string{{"npm", "install", "--package-lock-only", "--ignore-scripts"}, {"npm", "ls", "--package-lock-only", "ms"}},
		after: func(t *testing.T, dir string) { mustContain(t, dir, "package-lock.json", `"version": "2.1.3"`) },
	}.run(t)
}

func TestRealtoolPip(t *testing.T) {
	realtool{
		fixture: "pip", manifest: "requirements.txt", manager: Pip{Dir: "/"},
		bumps: []bump{{"six", "1.15.0", "1.16.0"}},
		// download resolves the pin against the index and fetches the distribution without
		// installing, so it works under a PEP 668 system Python too.
		steps: [][]string{{"python3", "-m", "pip", "download", "--no-deps", "--dest", "dl", "-r", "requirements.txt"}},
		after: func(t *testing.T, dir string) {
			if m, _ := filepath.Glob(filepath.Join(dir, "dl", "six-1.16.0*")); len(m) == 0 {
				t.Errorf("pip did not fetch six 1.16.0")
			}
		},
	}.run(t)
}

func TestRealtoolHelm(t *testing.T) {
	realtool{
		fixture: "helm", manifest: "Chart.yaml", manager: Helm{Dir: "/"},
		bumps: []bump{{"https://kubernetes-sigs.github.io/metrics-server/metrics-server", "3.11.0", "3.12.0"}},
		// dependency update fetches the subchart at the edited version and rewrites Chart.lock,
		// which ripple leaves stale; lint then reads the chart with its dependency in place.
		steps: [][]string{{"helm", "dependency", "update"}, {"helm", "dependency", "list"}, {"helm", "lint", "."}},
		after: func(t *testing.T, dir string) { mustContain(t, dir, "Chart.lock", "version: 3.12.0") },
	}.run(t)
}

func TestRealtoolBundler(t *testing.T) {
	realtool{
		fixture: "bundler", manifest: "Gemfile", manager: Bundler{Dir: "/"},
		bumps: []bump{{"rake", "13.0.6", "13.2.1"}},
		// No Gemfile.lock is committed; bundle lock resolves the edited Gemfile against rubygems.org.
		steps: [][]string{{"bundle", "lock"}},
		after: func(t *testing.T, dir string) { mustContain(t, dir, "Gemfile.lock", "rake (13.2.1)") },
	}.run(t)
}

func TestRealtoolComposer(t *testing.T) {
	realtool{
		fixture: "composer", manifest: "composer.json", manager: Composer{Dir: "/"},
		bumps: []bump{{"monolog/monolog", "3.0.0", "3.5.0"}},
		steps: [][]string{
			{"composer", "validate", "--no-check-lock", "--no-interaction"},
			{"composer", "update", "--dry-run", "--no-scripts", "--no-plugins", "--no-interaction"},
			{"composer", "update", "--no-install", "--no-scripts", "--no-plugins", "--no-interaction"},
		},
		after: func(t *testing.T, dir string) { mustContain(t, dir, "composer.lock", `"version": "3.5.0"`) },
	}.run(t)
}

func TestRealtoolNuget(t *testing.T) {
	realtool{
		fixture: "nuget", manifest: "realtool.csproj", manager: Nuget{Dir: "/"},
		bumps: []bump{{"Newtonsoft.Json", "12.0.3", "13.0.3"}},
		env:   []string{"DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1"},
		steps: [][]string{{"dotnet", "restore"}, {"dotnet", "build", "--no-restore"}},
		after: func(t *testing.T, dir string) {
			mustContain(t, dir, "obj/project.assets.json", "Newtonsoft.Json/13.0.3")
		},
	}.run(t)
}

func TestRealtoolMaven(t *testing.T) {
	realtool{
		fixture: "maven", manifest: "pom.xml", manager: Maven{Dir: "/"},
		bumps: []bump{{"org.apache.commons:commons-lang3", "3.12.0", "3.14.0"}},
		steps: [][]string{{"mvn", "-B", "-q", "validate"}, {"mvn", "-B", "-q", "dependency:resolve"}, {"mvn", "-B", "-q", "compile"}},
	}.run(t)
}

func TestRealtoolTerraform(t *testing.T) {
	realtool{
		fixture: "terraform", manifest: "main.tf", manager: Terraform{Dir: "/"},
		bumps: []bump{
			{"providers/hashicorp/random", "3.5.0", "3.6.0"},
			{"modules/cloudposse/label/null", "0.24.1", "0.25.0"},
		},
		// init downloads the provider and module at the edited constraints and writes
		// .terraform.lock.hcl, which ripple reports stale and does not rewrite.
		steps: [][]string{{"terraform", "init", "-backend=false", "-input=false"}, {"terraform", "validate"}},
		after: func(t *testing.T, dir string) { mustContain(t, dir, ".terraform.lock.hcl", `version     = "3.6.`) },
	}.run(t)
}

func TestRealtoolGitHubActions(t *testing.T) {
	realtool{
		fixture: "actions", manifest: ".github/workflows/ci.yml", manager: GitHubActions{Dir: "/"},
		bumps: []bump{{"actions/checkout", "v4.1.0", "v4.2.2"}},
		steps: [][]string{{"actionlint", ".github/workflows/ci.yml"}},
		after: func(t *testing.T, dir string) {
			// actionlint reads the file, not the registry: confirm the tag ripple wrote exists.
			out, err := exec.Command("git", "ls-remote", "--exit-code", "--tags", "https://github.com/actions/checkout", "v4.2.2").CombinedOutput()
			if err != nil {
				t.Errorf("tag actions/checkout@v4.2.2 does not exist: %v\n%s", err, out)
			}
		},
	}.run(t)
}

func TestRealtoolDocker(t *testing.T) {
	realtool{
		fixture: "docker", manifest: "Dockerfile", manager: Docker{Dir: "/"},
		bumps: []bump{{"docker.io/library/alpine", "3.18.4", "3.19.1"}},
		// No daemon: hadolint lints the file, and the registry is asked whether the tag exists.
		steps: [][]string{{"hadolint", "Dockerfile"}},
		after: func(t *testing.T, dir string) {
			resp, err := http.Get("https://hub.docker.com/v2/repositories/library/alpine/tags/3.19.1")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("alpine:3.19.1 does not exist on Docker Hub: HTTP %d", resp.StatusCode)
			}
		},
	}.run(t)
}
