package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const dsha = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const dsha2 = "sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestDockerMatches(t *testing.T) {
	m := Docker{Dir: "/"}
	for p, want := range map[string]bool{
		"Dockerfile":             true,
		"build/Dockerfile":       true,
		"Dockerfile.dev":         true,
		"api.Dockerfile":         true,
		"Containerfile":          true,
		"Dockerfile.md":          false,
		"dockerfile":             false,
		"docker-compose.yml":     true,
		"deploy/web.yaml":        true,
		"config/web.yaml":        false,
		"pkg/a/README.md":        false,
		"go.mod":                 false,
		"README.md":              false,
		"Dockerfiles/readme.txt": false,
	} {
		if got := m.Matches(p); got != want {
			t.Errorf("Matches(%q) = %v, want %v", p, got, want)
		}
	}
	if (Docker{Dir: "/sub"}).Matches("Dockerfile") {
		t.Error("a directory-scoped manager matched a file outside it")
	}
}

const dockerfile = `# syntax=docker/dockerfile:1
ARG BASE=1.2
FROM --platform=$BUILDPLATFORM node:20.11-alpine AS build
FROM nginx:1.25 AS web
from ghcr.io/org/img:v2.3.4@` + dsha + `
FROM golang:1.22 AS go
FROM registry.example.com:5000/team/app:3.1.0
FROM alpine:${ALPINE}
FROM scratch
FROM build
FROM ubuntu
FROM ubuntu:latest
FROM redis@` + dsha + `
FROM busybox:1.36   # trailing
`

func TestDockerExtract(t *testing.T) {
	deps, err := Docker{Dir: "/"}.Extract("Dockerfile", []byte(dockerfile))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceDocker || d.Manager != "docker" || d.File != "Dockerfile" {
			t.Errorf("fields: %+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := []string{
		"docker.io/library/node@20.11-alpine",
		"docker.io/library/nginx@1.25",
		"ghcr.io/org/img@v2.3.4",
		"registry.example.com:5000/team/app@3.1.0",
		"docker.io/library/busybox@1.36",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDockerApplyTag(t *testing.T) {
	d := model.Dependency{Package: "docker.io/library/nginx", Version: "1.25"}
	out, err := Docker{}.Apply([]byte(dockerfile), d, "", "1.26")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(dockerfile, "FROM nginx:1.25 AS web", "FROM nginx:1.26 AS web", 1)
	if string(out) != want {
		t.Errorf("only the nginx line moves:\n%s", out)
	}
}

func TestDockerApplyKeepsPlatformAliasAndSpelling(t *testing.T) {
	d := model.Dependency{Package: "docker.io/library/node", Version: "20.11-alpine"}
	out, _ := Docker{}.Apply([]byte(dockerfile), d, "", "20.12-alpine")
	if !strings.Contains(string(out), "FROM --platform=$BUILDPLATFORM node:20.12-alpine AS build\n") {
		t.Errorf("%s", out)
	}
	d = model.Dependency{Package: "docker.io/library/busybox", Version: "1.36"}
	out, _ = Docker{}.Apply([]byte(dockerfile), d, "", "1.37")
	if !strings.Contains(string(out), "FROM busybox:1.37   # trailing\n") {
		t.Errorf("%s", out)
	}
}

func TestDockerApplyDigestPinMovesTagAndDigest(t *testing.T) {
	d := model.Dependency{Package: "ghcr.io/org/img", Version: "v2.3.4"}
	out, err := Docker{}.Apply([]byte(dockerfile), d, "", "v2.4.0@"+dsha2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "from ghcr.io/org/img:v2.4.0@"+dsha2+"\n") {
		t.Errorf("%s", out)
	}
	if strings.Contains(string(out), dsha+"\nFROM golang") {
		t.Error("old digest left")
	}
}

func TestDockerApplyDigestPinWithoutDigestIsAnError(t *testing.T) {
	d := model.Dependency{Package: "ghcr.io/org/img", Version: "v2.3.4"}
	if _, err := (Docker{}).Apply([]byte(dockerfile), d, "", "v2.4.0"); err == nil {
		t.Fatal("a digest pin must never be left stale or guessed")
	}
}

func TestDockerApplyMissingAndRename(t *testing.T) {
	if _, err := (Docker{}).Apply([]byte(dockerfile), model.Dependency{Package: "docker.io/library/nginx", Version: "9"}, "", "10"); err == nil {
		t.Error("an absent dependency must be an error")
	}
	if _, err := (Docker{}).Apply([]byte(dockerfile), model.Dependency{Package: "docker.io/library/nginx", Version: "1.25"}, "other", "1.26"); err == nil {
		t.Error("an image cannot be renamed")
	}
}

func TestDockerLeavesGolangToTheGoVersionCode(t *testing.T) {
	deps, _ := Docker{Dir: "/"}.Extract("Dockerfile", []byte("FROM golang:1.22\nFROM docker.io/library/golang:1.22-alpine\nFROM --platform=linux/amd64 golang:1.22.3 AS b\n"))
	if len(deps) != 0 {
		t.Errorf("golang images belong to the go-toolchain follow code: %+v", deps)
	}
}

const composeFile = `services:
  web:
    image: "nginx:1.25"   # front
  db:
    image: postgres:16.1
  app:
    image: ${APP_IMAGE:-app:1.0}
  go:
    image: golang:1.22
  cache:
    image: redis
`

func TestDockerComposeExtractAndApply(t *testing.T) {
	deps, _ := Docker{Dir: "/"}.Extract("deploy/docker-compose.prod.yml", []byte(composeFile))
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "docker.io/library/nginx@1.25 docker.io/library/postgres@16.1 docker.io/library/golang@1.22"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v", got)
	}
	d := deps[0]
	out, err := Docker{}.Apply([]byte(composeFile), d, "", "1.26")
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.Replace(composeFile, `"nginx:1.25"   # front`, `"nginx:1.26"   # front`, 1); string(out) != w {
		t.Errorf("quotes, indentation and the comment are kept:\n%s", out)
	}
}

const k8sManifest = `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      initContainers:
        - image: busybox:1.36
      containers:
        - name: web
          image: ghcr.io/org/web:v1.2.3
`

func TestDockerKubernetesManifest(t *testing.T) {
	deps, _ := Docker{Dir: "/"}.Extract("k8s/web.yaml", []byte(k8sManifest))
	if len(deps) != 2 || deps[0].Package != "docker.io/library/busybox" || deps[1].Package != "ghcr.io/org/web" {
		t.Fatalf("%+v", deps)
	}
	out, err := Docker{}.Apply([]byte(k8sManifest), deps[1], "", "v1.3.0")
	if err != nil || !strings.Contains(string(out), "          image: ghcr.io/org/web:v1.3.0\n") || !strings.Contains(string(out), "- image: busybox:1.36") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestDockerYAMLWithoutKubernetesShapeIsLeftAlone(t *testing.T) {
	deps, _ := Docker{Dir: "/"}.Extract("deploy/config.yaml", []byte("image: nginx:1.25\n"))
	if len(deps) != 0 {
		t.Errorf("a yaml file that is not a manifest or compose file: %+v", deps)
	}
}

const argDockerfile = `ARG NODE=20.11
ARG SHARED=3.19
ARG OTHER=1.0
FROM node:${NODE} AS build
FROM alpine:$SHARED
FROM busybox:${SHARED}
FROM nginx:${MISSING}
FROM redis:${OTHER}-alpine
COPY --from=build /a /b
COPY --chown=1:1 --from=ghcr.io/org/tool:v1.2.3 /t /t
COPY --from=0 /x /x
`

func TestDockerArgTagsAndCopyFrom(t *testing.T) {
	deps, _ := Docker{Dir: "/"}.Extract("Dockerfile", []byte(argDockerfile))
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	// SHARED feeds two different images, MISSING has no default; OTHER is part of a tag.
	want := "docker.io/library/node@20.11 docker.io/library/redis@1.0-alpine ghcr.io/org/tool@v1.2.3"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v", got)
	}
	out, err := Docker{}.Apply([]byte(argDockerfile), deps[0], "", "22.1")
	if err != nil || !strings.HasPrefix(string(out), "ARG NODE=22.1\nARG SHARED=3.19\n") || !strings.Contains(string(out), "FROM node:${NODE} AS build\n") {
		t.Errorf("the ARG line moves, the FROM is untouched: %v\n%s", err, out)
	}
	out, err = Docker{}.Apply([]byte(argDockerfile), deps[2], "", "v1.4.0")
	if err != nil || !strings.Contains(string(out), "COPY --chown=1:1 --from=ghcr.io/org/tool:v1.4.0 /t /t\n") {
		t.Errorf("%v\n%s", err, out)
	}
}

func depList(t *testing.T, p, content string) []string {
	t.Helper()
	deps, err := Docker{Dir: "/"}.Extract(p, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	return got
}

func TestDockerKustomizeImages(t *testing.T) {
	for name, c := range map[string]struct {
		file, in string
		want     []string
	}{
		"newName and newTag":     {"kustomization.yaml", "images:\n- name: web\n  newName: ghcr.io/org/web\n  newTag: v1.2.3\n", []string{"ghcr.io/org/web@v1.2.3"}},
		"name only is the image": {"app/Kustomization", "images:\n  - newTag: '1.25'\n    name: nginx\n", []string{"docker.io/library/nginx@1.25"}},
		"no newTag":              {"kustomization.yml", "images:\n  - name: nginx\n    newName: other\n", nil},
		"digest pinned":          {"kustomization.yaml", "images:\n  - name: nginx\n    newTag: 1.25\n    newDigest: sha256:abc\n", []string{"docker.io/library/nginx@1.25"}},
		"digest only":            {"kustomization.yaml", "images:\n  - name: nginx\n    newDigest: sha256:abc\n", nil},
		"odd digest":             {"kustomization.yaml", "images:\n  - name: nginx\n    newTag: 1.25\n    newDigest: md5:abc\n", nil},
		"floating tag":           {"kustomization.yaml", "images:\n  - name: nginx\n    newTag: latest\n", nil},
		"other top level":        {"kustomization.yaml", "resources:\n  - name: nginx\n    newTag: 1.25\n", nil},
		"two entries":            {"kustomization.yaml", "images:\n  - name: a\n    newTag: 1.0\n  - name: b\n    newTag: 2.0\nnamePrefix: x-\n", []string{"docker.io/library/a@1.0", "docker.io/library/b@2.0"}},
	} {
		if got := depList(t, "k/"+c.file, c.in); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}

func TestDockerKustomizeApplyKeepsQuotesCommentAndLayout(t *testing.T) {
	in := "images:\n  - name: web\n    newName: nginx\n    newTag: \"1.25\"   # pinned\n    # note\n"
	deps, _ := Docker{Dir: "/"}.Extract("kustomization.yaml", []byte(in))
	out, err := Docker{}.Apply([]byte(in), deps[0], "", "1.26")
	if want := strings.Replace(in, `"1.25"`, `"1.26"`, 1); err != nil || string(out) != want {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestDockerHelmValues(t *testing.T) {
	for name, c := range map[string]struct {
		file, in string
		want     []string
	}{
		"pair":             {"values.yaml", "image:\n  repository: nginx\n  tag: 1.25\n", []string{"docker.io/library/nginx@1.25"}},
		"registry":         {"values-prod.yaml", "image:\n  registry: ghcr.io\n  repository: org/app\n  tag: \"v2.0.0\"\n", []string{"ghcr.io/org/app@v2.0.0"}},
		"nested":           {"prod.values.yml", "redis:\n  image:\n    repository: redis\n    tag: '7.0'\n", []string{"docker.io/library/redis@7.0"}},
		"init image":       {"values.yaml", "initImage:\n  repository: busybox\n  tag: 1.36\n", []string{"docker.io/library/busybox@1.36"}},
		"not an image":     {"values.yaml", "git:\n  repository: org/app\n  tag: 1.2.3\n", nil},
		"url":              {"values.yaml", "image:\n  repository: https://github.com/org/app\n  tag: 1.2.3\n", nil},
		"digest":           {"values.yaml", "image:\n  repository: nginx\n  tag: 1.25\n  digest: sha256:abc\n", []string{"docker.io/library/nginx@1.25"}},
		"odd digest":       {"values.yaml", "image:\n  repository: nginx\n  tag: 1.25\n  digest: md5:abc\n", nil},
		"single string":    {"values.yaml", "sidecar:\n  image: ghcr.io/org/side:v1.4.0\ninitImage: \"busybox:1.36\"\nname: x:1.2.3\n", []string{"ghcr.io/org/side@v1.4.0", "docker.io/library/busybox@1.36"}},
		"single templated": {"values.yaml", "image: \"{{ .Values.reg }}/app:1.2.3\"\nimage2: app\nimageName: app:latest\n", nil},
		"templated":        {"values.yaml", "image:\n  repository: nginx\n  tag: \"{{ .Chart.AppVersion }}\"\n", nil},
		"no tag":           {"values.yaml", "image:\n  repository: nginx\n", nil},
		"empty tag":        {"values.yaml", "image:\n  repository: nginx\n  tag: \"\"\n", nil},
		"other file":       {"config.yaml", "image:\n  repository: nginx\n  tag: 1.25\n", nil},
		"sibling blocks":   {"values.yaml", "a:\n  image:\n    repository: x\n  tag: 9.9\n", nil},
	} {
		if got := depList(t, "chart/"+c.file, c.in); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}

func TestDockerHelmApplyEditsOnlyTheTagLine(t *testing.T) {
	in := "# values\nimage:\n  repository: nginx # base\n  tag: '1.25' # pin\n  pullPolicy: Always\n"
	deps, _ := Docker{Dir: "/"}.Extract("values.yaml", []byte(in))
	out, err := Docker{}.Apply([]byte(in), deps[0], "", "1.26")
	if want := strings.Replace(in, "'1.25'", "'1.26'", 1); err != nil || string(out) != want {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestDockerStageArgsAndMounts(t *testing.T) {
	df := "ARG G=1.0\nFROM alpine:3.19 AS a\nARG TOOL=v1.0.0\nCOPY --from=org/tool:${TOOL} /t /t\n" +
		"FROM alpine:3.19 AS b\nARG TOOL=v2.0.0\nCOPY --from=org/tool2:${TOOL} /t /t\n" +
		"ARG G\nCOPY --from=org/g:${G} /g /g\n" +
		"RUN --mount=type=bind,from=org/m:3.1,target=/m true\nRUN --mount=from=a,target=/x true\n" +
		"FROM alpine:3.19 AS c\nCOPY --from=org/tool:${TOOL} /t /t\n"
	got := depList(t, "Dockerfile", df)
	for _, w := range []string{"org/tool@v1.0.0", "org/tool2@v2.0.0", "org/g@1.0", "org/m@3.1"} {
		if !strings.Contains(strings.Join(got, " "), w) {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	// A stage's ARG is not visible in a later stage (stage c has no TOOL), and a stage name is no image.
	if n := strings.Count(strings.Join(got, " "), "org/tool@"); n != 1 {
		t.Errorf("stage c must not inherit stage a's ARG: %v", got)
	}
	deps, _ := Docker{Dir: "/"}.Extract("Dockerfile", []byte(df))
	for _, d := range deps {
		if d.Package == "docker.io/org/tool2" {
			out, err := Docker{}.Apply([]byte(df), d, "", "v2.1.0")
			if want := strings.Replace(df, "ARG TOOL=v2.0.0", "ARG TOOL=v2.1.0", 1); err != nil || string(out) != want {
				t.Errorf("only stage b's ARG line moves: %v\n%s", err, out)
			}
		}
		if d.Package == "docker.io/org/m" {
			out, _ := Docker{}.Apply([]byte(df), d, "", "3.2")
			if want := strings.Replace(df, "from=org/m:3.1,target", "from=org/m:3.2,target", 1); string(out) != want {
				t.Errorf("mount edit:\n%s", out)
			}
		}
	}
}

func TestDockerArgThatIsPartOfATag(t *testing.T) {
	df := "ARG V=3.19\nFROM alpine:${V}-slim\nFROM alpine:3.18\n"
	if got := depList(t, "Dockerfile", df); strings.Join(got, " ") != "docker.io/library/alpine@3.19-slim docker.io/library/alpine@3.18" {
		t.Fatalf("got %v", got)
	}
	deps, _ := Docker{Dir: "/"}.Extract("Dockerfile", []byte(df))
	out, err := Docker{}.Apply([]byte(df), deps[0], "", "3.20-slim")
	if want := strings.Replace(df, "ARG V=3.19", "ARG V=3.20", 1); err != nil || string(out) != want {
		t.Errorf("%v\n%s", err, out)
	}
	if _, err := (Docker{}).Apply([]byte(df), deps[0], "", "3.20"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a tag without the suffix cannot be written into the ARG: %v", err)
	}
}

func TestDockerDigestPairsMoveTogether(t *testing.T) {
	for file, in := range map[string]string{
		"k/kustomization.yaml": "images:\n  - name: nginx\n    newTag: \"1.25\" # t\n    newDigest: sha256:old # d\n",
		"chart/values.yaml":    "image:\n  repository: nginx\n  tag: 1.25 # t\n  digest: \"sha256:old\" # d\n",
	} {
		deps, _ := Docker{Dir: "/"}.Extract(file, []byte(in))
		if len(deps) != 1 {
			t.Fatalf("%s: %v", file, deps)
		}
		if _, err := (Docker{}).Apply([]byte(in), deps[0], "", "1.26"); err == nil {
			t.Errorf("%s: a digest pair must never be left stale", file)
		}
		out, err := Docker{}.Apply([]byte(in), deps[0], "", "1.26@"+dsha)
		want := strings.Replace(strings.Replace(in, "1.25", "1.26", 1), "sha256:old", dsha, 1)
		if err != nil || string(out) != want {
			t.Errorf("%s: %v\n%s", file, err, out)
		}
	}
}

func TestDockerHelmSingleStringApplyKeepsTheLine(t *testing.T) {
	in := "sidecar:\n  image:   'ghcr.io/org/side:v1.4.0'   # pin\n"
	deps, _ := Docker{Dir: "/"}.Extract("values.yaml", []byte(in))
	out, err := Docker{}.Apply([]byte(in), deps[0], "", "v1.5.0")
	if want := strings.Replace(in, "v1.4.0", "v1.5.0", 1); err != nil || string(out) != want {
		t.Errorf("%v\n%s", err, out)
	}
}
