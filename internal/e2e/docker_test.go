package e2e

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/registry"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const dockerConfig = "updates:\n  - ecosystem: docker\n"

func dockerEnv(t *testing.T, st store.Store) (*env, *testutil.Registry) {
	reg := testutil.NewRegistry(t)
	return newEnvOpts(t, st, defaultLimits(), opts{registry: reg}), reg
}

func dockerEnvOpts(t *testing.T, st store.Store, hubNewest bool) (*env, *testutil.Registry) {
	reg := testutil.NewRegistry(t)
	return newEnvOpts(t, st, defaultLimits(), opts{registry: reg, hubNewest: hubNewest}), reg
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

func TestDockerPrivateImageMovesWithALoginFromAConfigFile(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		reg := testutil.NewRegistry(t)
		reg.AddTag("library/redis", "7.0")
		reg.AddTag("corp/secret", "1.0")
		reg.RequireLogin("corp/secret", "bot", "s3cret-pass")
		reg.AddTag("corp/other", "1.0")
		reg.RequireLogin("corp/other", "someone", "else") // a private image with no login configured

		cfgPath := filepath.Join(t.TempDir(), "config.json")
		raw := base64.StdEncoding.EncodeToString([]byte("bot:s3cret-pass"))
		if err := os.WriteFile(cfgPath, []byte(`{"auths":{"ghcr.io":{"auth":"`+raw+`"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		creds, err := registry.LoadDockerConfig(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		e := newEnvOpts(t, st, defaultLimits(), opts{registry: reg, registryAuth: creds})
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig,
			"Dockerfile": "FROM redis:7.0\nFROM ghcr.io/corp/secret:1.0\nFROM ghcr.io/corp/other:1.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("library/redis", "7.2")
		reg.AddTag("corp/secret", "1.1")
		reg.AddTag("corp/other", "1.1")
		e.tick()
		e.tick()

		prs := e.gh.PRs("acme/app")
		moved := map[string]bool{}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "Dockerfile")
			moved["redis"] = moved["redis"] || strings.Contains(got, "redis:7.2")
			moved["secret"] = moved["secret"] || strings.Contains(got, "ghcr.io/corp/secret:1.1")
			moved["other"] = moved["other"] || strings.Contains(got, "corp/other:1.1")
			if strings.Contains(got+pr.Body+pr.Title, "s3cret-pass") || strings.Contains(got+pr.Body+pr.Title, raw) {
				t.Errorf("a credential reached a PR: %+v", pr)
			}
		}
		if !moved["redis"] || !moved["secret"] || moved["other"] {
			t.Errorf("public and logged-in images move, the one without a login stays: %v", moved)
		}
		for _, r := range reg.TokenRequests() {
			if strings.Contains(r, "library/redis") && strings.HasSuffix(r, "basic=true") {
				t.Errorf("the ghcr.io login was sent for a Docker Hub image: %s", r)
			}
		}
	})
}

func TestDockerKustomizeHelmStageArgAndMountMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		for _, r := range [][2]string{{"library/nginx", "1.25"}, {"corp/api", "v1.0.0"}, {"library/redis", "7.0"},
			{"org/tool", "v1.0.0"}, {"org/cache", "2.0"}} {
			reg.AddTag(r[0], r[1])
		}
		kust := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nimages:\n  - name: web\n    newName: nginx\n    newTag: \"1.25\" # pinned\n"
		values := "image:\n  registry: ghcr.io\n  repository: corp/api\n  tag: v1.0.0\nredis:\n  image:\n    repository: redis\n    tag: '7.0'\n"
		df := "FROM alpine:3.19\nARG TOOL=v1.0.0\nCOPY --from=ghcr.io/org/tool:${TOOL} /t /t\nRUN --mount=type=bind,from=ghcr.io/org/cache:2.0,target=/c true\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig,
			"deploy/kustomization.yaml": kust, "chart/values.yaml": values, "Dockerfile": df})
		reg.AddTag("library/alpine", "3.19")
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("library/nginx", "1.26")
		reg.AddTag("corp/api", "v1.1.0")
		reg.AddTag("library/redis", "7.2")
		reg.AddTag("org/tool", "v1.1.0")
		reg.AddTag("org/cache", "2.1")
		e.tick()
		e.tick()

		want := map[string]string{
			"kustomize":  strings.Replace(kust, `"1.25"`, `"1.26"`, 1),
			"helm-api":   strings.Replace(values, "v1.0.0", "v1.1.0", 1),
			"helm-redis": strings.Replace(values, "'7.0'", "'7.2'", 1),
			"stage-arg":  strings.Replace(df, "ARG TOOL=v1.0.0", "ARG TOOL=v1.1.0", 1),
			"mount":      strings.Replace(df, "cache:2.0", "cache:2.1", 1),
		}
		file := map[string]string{"kustomize": "deploy/kustomization.yaml", "helm-api": "chart/values.yaml",
			"helm-redis": "chart/values.yaml", "stage-arg": "Dockerfile", "mount": "Dockerfile"}
		seen := map[string]bool{}
		prs := e.gh.PRs("acme/app")
		for _, pr := range prs {
			for k, w := range want {
				if got, _ := e.gh.FileAt("acme/app", pr.Head, file[k]); got == w {
					seen[k] = true
				}
			}
		}
		for k := range want {
			if !seen[k] {
				t.Errorf("no PR made exactly the %s edit (%d PRs)", k, len(prs))
			}
		}
	})
}

func TestDockerPrivateImageMovesWithAnIdentityTokenFromAConfigFile(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		reg := testutil.NewRegistry(t)
		reg.AddTag("corp/secret", "1.0")
		reg.RequireIdentityToken("corp/secret", "refresh-s3cret")
		reg.AddTag("corp/other", "1.0")
		reg.RequireIdentityToken("corp/other", "someone-elses")

		cfgPath := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(cfgPath, []byte(`{"auths":{"ghcr.io":{"identitytoken":"refresh-s3cret"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		creds, err := registry.LoadDockerConfig(cfgPath)
		if err != nil || len(creds.Warnings) != 0 {
			t.Fatalf("%v %v", err, creds.Warnings)
		}
		e := newEnvOpts(t, st, defaultLimits(), opts{registry: reg, registryAuth: creds})
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig,
			"Dockerfile": "FROM ghcr.io/corp/secret:1.0\nFROM ghcr.io/corp/other:1.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("corp/secret", "1.1")
		reg.AddTag("corp/other", "1.1")
		e.tick()
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 {
			t.Fatalf("only the image the token opens moves, got %d PRs", len(prs))
		}
		got, _ := e.gh.FileAt("acme/app", prs[0].Head, "Dockerfile")
		if got != "FROM ghcr.io/corp/secret:1.1\nFROM ghcr.io/corp/other:1.0\n" {
			t.Errorf("%s", got)
		}
		if strings.Contains(got+prs[0].Body+prs[0].Title, "refresh-s3cret") {
			t.Errorf("the token reached a PR: %+v", prs[0])
		}
	})
}

// A Kustomize newTag/newDigest pair and a Helm tag/digest pair move together to the digest the
// registry reports for the new tag; a Helm `image: repo:tag` single string and an ARG that is
// only the front of a tag (`${V}-slim`) move through a real run.
func TestDockerDigestPairsSingleStringsAndArgSuffixMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		oldK := reg.AddTag("library/nginx", "1.25")
		oldH := reg.AddTag("corp/api", "v1.0.0")
		reg.AddTag("org/side", "v1.4.0")
		reg.AddTag("library/alpine", "3.19-slim")
		kust := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nimages:\n  - name: nginx\n    newTag: \"1.25\"\n    newDigest: " + oldK + "\n"
		values := "image:\n  repository: corp/api\n  registry: ghcr.io\n  tag: v1.0.0\n  digest: " + oldH + "\nsidecar:\n  image: ghcr.io/org/side:v1.4.0 # pin\n"
		df := "ARG V=3.19\nFROM alpine:${V}-slim\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig,
			"deploy/kustomization.yaml": kust, "chart/values.yaml": values, "Dockerfile": df})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		newK := reg.AddTag("library/nginx", "1.26")
		newH := reg.AddTag("corp/api", "v1.1.0")
		reg.AddTag("org/side", "v1.5.0")
		reg.AddTag("library/alpine", "3.20-slim")
		e.tick()
		e.tick()

		values2 := strings.Replace(strings.Replace(values, "v1.0.0", "v1.1.0", 1), oldH, newH, 1)
		want := map[string][2]string{
			"kustomize": {"deploy/kustomization.yaml", strings.Replace(strings.Replace(kust, `"1.25"`, `"1.26"`, 1), oldK, newK, 1)},
			"helm-pair": {"chart/values.yaml", values2},
			"helm-side": {"chart/values.yaml", strings.Replace(values, "side:v1.4.0", "side:v1.5.0", 1)},
			"arg":       {"Dockerfile", strings.Replace(df, "V=3.19", "V=3.20", 1)},
		}
		seen := map[string]bool{}
		for _, pr := range e.gh.PRs("acme/app") {
			for k, w := range want {
				if got, _ := e.gh.FileAt("acme/app", pr.Head, w[0]); got == w[1] {
					seen[k] = true
				}
			}
		}
		for k := range want {
			if !seen[k] {
				t.Errorf("no PR made exactly the %s edit", k)
			}
		}
	})
}

// ingress-nginx writes `registry:`, `image: path/name` and `tag:` under `image:` (no
// `repository:`), and Ubuntu tags a release by zero-padded month: both move.
func TestDockerIngressStyleValuesAndCalendarTagMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		reg.AddTag("org/controller", "v1.10.1")
		reg.AddTag("library/ubuntu", "22.04")
		values := "controller:\n  image:\n    registry: ghcr.io\n    image: org/controller\n    tag: \"v1.10.1\"\n"
		df := "FROM ubuntu:22.04 AS base\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "chart/values.yaml": values, "Dockerfile": df})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("org/controller", "v1.11.0")
		reg.AddTag("library/ubuntu", "22.10")
		reg.AddTag("library/ubuntu", "24.04")
		e.tick()
		e.tick()
		want := map[string][2]string{
			"chart/values.yaml": {"chart/values.yaml", strings.Replace(values, "v1.10.1", "v1.11.0", 1)},
			"Dockerfile":        {"Dockerfile", "FROM ubuntu:24.04 AS base\n"},
		}
		seen := map[string]bool{}
		for _, pr := range e.gh.PRs("acme/app") {
			for k, w := range want {
				if got, _ := e.gh.FileAt("acme/app", pr.Head, w[0]); got == w[1] {
					seen[k] = true
				}
			}
		}
		for k := range want {
			if !seen[k] {
				t.Errorf("no PR made the %s edit", k)
			}
		}
	})
}
