package manager

import (
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
	deps, _ := Docker{Dir: "/"}.Extract("deploy/values.yaml", []byte("image: nginx:1.25\n"))
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
	// SHARED feeds two different images, MISSING has no default, OTHER is not the whole tag.
	want := "docker.io/library/node@20.11 ghcr.io/org/tool@v1.2.3"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v", got)
	}
	out, err := Docker{}.Apply([]byte(argDockerfile), deps[0], "", "22.1")
	if err != nil || !strings.HasPrefix(string(out), "ARG NODE=22.1\nARG SHARED=3.19\n") || !strings.Contains(string(out), "FROM node:${NODE} AS build\n") {
		t.Errorf("the ARG line moves, the FROM is untouched: %v\n%s", err, out)
	}
	out, err = Docker{}.Apply([]byte(argDockerfile), deps[1], "", "v1.4.0")
	if err != nil || !strings.Contains(string(out), "COPY --chown=1:1 --from=ghcr.io/org/tool:v1.4.0 /t /t\n") {
		t.Errorf("%v\n%s", err, out)
	}
}
