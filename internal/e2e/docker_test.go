package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const dockerConfig = "updates:\n  - ecosystem: docker\n"

func dockerEnv(t *testing.T, st store.Store) (*env, *testutil.Registry) {
	reg := testutil.NewRegistry(t)
	return newEnvOpts(t, st, defaultLimits(), opts{registry: reg}), reg
}

func TestDockerTagBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		reg.AddTag("library/nginx", "1.25")
		reg.AddTag("library/nginx", "1.25.4")
		df := "FROM --platform=$BUILDPLATFORM golang:1.22 AS build\nFROM nginx:1.25 AS web\nFROM scratch\nFROM alpine\nFROM busybox:${V}\nRUN true\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": df})
		start(t, e, "acme/app")

		for _, d := range e.deps("acme/app") {
			if d.Package != "docker.io/library/nginx" || d.Datasource != model.DatasourceDocker {
				t.Errorf("only nginx is tracked (golang is the Go-version code's), got %+v", d)
			}
		}
		e.clock.Advance(time.Minute)
		reg.AddTag("library/nginx", "1.26")
		reg.AddTag("library/nginx", "1.26.0-alpine") // another shape: ignored
		e.tick()
		e.tick() // a replay must not open a second PR

		head, body := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "Dockerfile")
		if want := strings.Replace(df, "nginx:1.25 AS", "nginx:1.26 AS", 1); got != want {
			t.Errorf("only the nginx line should move:\n%s", got)
		}
		for _, w := range []string{"docker.io/library/nginx", "`1.25`", "`1.26`"} {
			if !strings.Contains(body, w) {
				t.Errorf("PR body lacks %q:\n%s", w, body)
			}
		}
		if strings.Contains(body, "module index") || !strings.Contains(body, "when a new tag appeared on the registry") {
			t.Errorf("the footer must say where a Docker tag is seen:\n%s", body)
		}
		if !strings.HasPrefix(head, "ripple/docker/") {
			t.Errorf("branch %q", head)
		}
		if r := strings.Join(reg.Requests(), " "); !strings.Contains(r, "GET /token") {
			t.Errorf("the anonymous token flow was not used: %s", r)
		}
	})
}

func TestDockerDigestPinMovesTagAndDigest(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		old := reg.AddTag("org/img", "v2.3.4")
		df := "FROM ghcr.io/org/img:v2.3.4@" + old + " AS base\nCOPY . .\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "build/Dockerfile.prod": df})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		fresh := reg.AddTag("org/img", "v2.4.0")
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "build/Dockerfile.prod")
		if want := "FROM ghcr.io/org/img:v2.4.0@" + fresh + " AS base\nCOPY . .\n"; got != want {
			t.Errorf("tag and digest must move together:\n%s\nwant:\n%s", got, want)
		}
	})
}

func TestDockerPrivateImageIsSkippedAndOthersStillMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		reg.AddTag("library/redis", "7.0")
		reg.AddTag("corp/secret", "1.0")
		reg.DenyToken("corp/secret")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": "FROM redis:7.0\nFROM corp/secret:1.0\n"})
		// Not opted in: never read.
		e.gh.AddRepo("acme/optout", map[string]string{"Dockerfile": "FROM redis:7.0\n"})
		start(t, e, "acme/app", "acme/optout")
		e.clock.Advance(time.Minute)
		reg.AddTag("library/redis", "7.2")
		reg.AddTag("corp/secret", "1.1")
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "Dockerfile")
		if got != "FROM redis:7.2\nFROM corp/secret:1.0\n" {
			t.Errorf("the public image moves, the private one stays:\n%s", got)
		}
		if n := len(e.gh.PRs("acme/optout")); n != 0 || len(e.deps("acme/optout")) != 0 {
			t.Errorf("a repo that did not opt in was touched")
		}
	})
}

func TestDockerUpToDateOpensNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		reg.AddTag("library/redis", "7.0")
		reg.AddTag("library/redis", "latest")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": "FROM redis:7.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("an up-to-date repo got %d PRs", n)
		}
	})
}

func TestDockerComposeManifestArgAndCopyFromMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		for _, r := range [][2]string{{"library/nginx", "1.25"}, {"library/node", "20.11"}, {"org/tool", "v1.0.0"}, {"library/redis", "7.0"}} {
			reg.AddTag(r[0], r[1])
		}
		compose := "services:\n  web:\n    image: nginx:1.25\n"
		manifest := "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n    - image: redis:7.0\n"
		df := "ARG NODE=20.11\nFROM node:${NODE}\nCOPY --from=ghcr.io/org/tool:v1.0.0 /t /t\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig,
			"docker-compose.yml": compose, "k8s/pod.yaml": manifest, "Dockerfile": df})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("library/nginx", "1.26")
		reg.AddTag("library/node", "22.1")
		reg.AddTag("org/tool", "v1.1.0")
		reg.AddTag("library/redis", "7.2")
		e.tick()
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 4 {
			t.Fatalf("one PR per image expected, got %d", len(prs))
		}
		want := map[string]string{
			"docker-compose.yml": strings.Replace(compose, "1.25", "1.26", 1),
			"k8s/pod.yaml":       strings.Replace(manifest, "7.0", "7.2", 1),
		}
		seen := map[string]bool{}
		for _, pr := range prs {
			for f, w := range want {
				if got, _ := e.gh.FileAt("acme/app", pr.Head, f); got == w {
					seen[f] = true
				}
			}
			if got, _ := e.gh.FileAt("acme/app", pr.Head, "Dockerfile"); got == "ARG NODE=22.1\nFROM node:${NODE}\nCOPY --from=ghcr.io/org/tool:v1.0.0 /t /t\n" {
				seen["arg"] = true
			} else if got == "ARG NODE=20.11\nFROM node:${NODE}\nCOPY --from=ghcr.io/org/tool:v1.1.0 /t /t\n" {
				seen["copy"] = true
			}
		}
		for _, k := range []string{"docker-compose.yml", "k8s/pod.yaml", "arg", "copy"} {
			if !seen[k] {
				t.Errorf("no PR moved %s", k)
			}
		}
	})
}

func TestDockerCooldownCountsFromTheImageCreationTime(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		reg.AddTag("library/nginx", "1.25")
		reg.AddTag("library/redis", "7.0")
		cfg := "updates:\n  - ecosystem: docker\n    cooldown: 3d\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cfg, "Dockerfile": "FROM nginx:1.25\nFROM redis:7.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		now := e.clock.Now()
		// First seen just now, but built long ago: past the cooldown at once.
		reg.AddTagCreated("library/nginx", "1.26", now.Add(-10*24*time.Hour))
		// Built an hour ago: still inside the cooldown.
		reg.AddTagCreated("library/redis", "7.2", now.Add(-time.Hour))
		e.tick()
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 {
			t.Fatalf("only the old image clears the cooldown, got %d PRs", len(prs))
		}
		if got, _ := e.gh.FileAt("acme/app", prs[0].Head, "Dockerfile"); got != "FROM nginx:1.26\nFROM redis:7.0\n" {
			t.Errorf("%s", got)
		}
		e.clock.Advance(3 * 24 * time.Hour)
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 2 {
			t.Errorf("the redis PR opens once its cooldown, counted from creation, passed: %d PRs", n)
		}
	})
}
