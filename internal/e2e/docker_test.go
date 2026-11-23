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
