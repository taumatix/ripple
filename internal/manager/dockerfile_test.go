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
		"docker-compose.yml":     false,
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
