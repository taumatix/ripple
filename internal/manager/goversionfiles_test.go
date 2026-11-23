package manager

import "testing"

func TestFollowGoVersion(t *testing.T) {
	// old is the minor the go.mod line was at; target is the release it moves to.
	const old, target = "v1.24.1", "v1.24.7"
	cases := []struct {
		name, path, in, want string
	}{
		{"go-version file", ".go-version", "1.24.1\n", "1.24.7\n"},
		{"go-version file no newline", ".go-version", "1.24.2", "1.24.7"},
		{"go-version minor only floats", ".go-version", "1.24\n", "1.24\n"},
		{"go-version already current", ".go-version", "1.24.7\n", "1.24.7\n"},
		{"go-version newer left", ".go-version", "1.24.9\n", "1.24.9\n"},
		{"go-version other minor left", ".go-version", "1.23.4\n", "1.23.4\n"},
		{"go-version prerelease left", ".go-version", "1.24rc1\n", "1.24rc1\n"},
		{"go-version two lines left", ".go-version", "1.24.1\n1.23.0\n", "1.24.1\n1.23.0\n"},
		{"tool-versions", ".tool-versions", "nodejs 20.1.0\ngolang 1.24.1\npython 3.12.1\n", "nodejs 20.1.0\ngolang 1.24.7\npython 3.12.1\n"},
		{"tool-versions comment kept", ".tool-versions", "golang 1.24.1 # pinned\n", "golang 1.24.7 # pinned\n"},
		{"tool-versions other tool with same number", ".tool-versions", "nodejs 1.24.1\n", "nodejs 1.24.1\n"},
		{"tool-versions golang-ci not golang", ".tool-versions", "golangci-lint 1.24.1\n", "golangci-lint 1.24.1\n"},
		{"tool-versions several versions left", ".tool-versions", "golang 1.24.1 1.23.0\n", "golang 1.24.1 1.23.0\n"},
		{"tool-versions system left", ".tool-versions", "golang system\n", "golang system\n"},
		{"workflow plain", ".github/workflows/ci.yml", "      - uses: actions/setup-go@v5\n        with:\n          go-version: 1.24.1\n", "      - uses: actions/setup-go@v5\n        with:\n          go-version: 1.24.7\n"},
		{"workflow quoted", ".github/workflows/ci.yaml", "          go-version: '1.24.1' # lts\n", "          go-version: '1.24.7' # lts\n"},
		{"workflow double quoted list item", ".github/workflows/ci.yml", "      - go-version: \"1.24.1\"\n", "      - go-version: \"1.24.7\"\n"},
		{"workflow matrix expression", ".github/workflows/ci.yml", "          go-version: ${{ matrix.go }}\n", "          go-version: ${{ matrix.go }}\n"},
		{"workflow version file", ".github/workflows/ci.yml", "          go-version-file: go.mod\n", "          go-version-file: go.mod\n"},
		{"workflow inline list", ".github/workflows/ci.yml", "        go-version: [1.24.1, 1.23.0]\n", "        go-version: [1.24.1, 1.23.0]\n"},
		{"workflow stable alias", ".github/workflows/ci.yml", "          go-version: stable\n", "          go-version: stable\n"},
		{"workflow minor only", ".github/workflows/ci.yml", "          go-version: '1.24'\n", "          go-version: '1.24'\n"},
		{"workflow comment line", ".github/workflows/ci.yml", "          # go-version: 1.24.1\n", "          # go-version: 1.24.1\n"},
		{"workflow other key", ".github/workflows/ci.yml", "          node-go-version: 1.24.1\n", "          node-go-version: 1.24.1\n"},
		{"workflow outside workflows dir", "docs/ci.yml", "go-version: 1.24.1\n", "go-version: 1.24.1\n"},
		{"dockerfile plain", "Dockerfile", "FROM golang:1.24.1 AS build\nRUN go build\n", "FROM golang:1.24.7 AS build\nRUN go build\n"},
		{"dockerfile suffix", "Dockerfile", "FROM golang:1.24.1-alpine3.20\n", "FROM golang:1.24.7-alpine3.20\n"},
		{"dockerfile platform and case", "Dockerfile", "from --platform=$BUILDPLATFORM golang:1.24.1-alpine as b\n", "from --platform=$BUILDPLATFORM golang:1.24.7-alpine as b\n"},
		{"dockerfile digest left", "Dockerfile", "FROM golang:1.24.1@sha256:abc\n", "FROM golang:1.24.1@sha256:abc\n"},
		{"dockerfile other image", "Dockerfile", "FROM mygolang:1.24.1\n", "FROM mygolang:1.24.1\n"},
		{"dockerfile other registry", "Dockerfile", "FROM registry.example.com/golang:1.24.1\n", "FROM registry.example.com/golang:1.24.1\n"},
		{"dockerfile arg", "Dockerfile", "ARG V=1.24.1\nFROM golang:${V}\n", "ARG V=1.24.1\nFROM golang:${V}\n"},
		{"dockerfile run mentions", "Dockerfile", "RUN echo FROM golang:1.24.1\n", "RUN echo FROM golang:1.24.1\n"},
		{"dockerfile suffix name", "build.Dockerfile", "FROM golang:1.24.1\n", "FROM golang:1.24.7\n"},
		{"dockerfile variant name", "Dockerfile.dev", "FROM golang:1.24.1\n", "FROM golang:1.24.7\n"},
		{"dockerfile bare tag", "Dockerfile", "FROM golang:1.24\n", "FROM golang:1.24\n"},
		{"dockerfile latest", "Dockerfile", "FROM golang:latest\n", "FROM golang:latest\n"},
		{"dockerfile rc", "Dockerfile", "FROM golang:1.24.1rc1\n", "FROM golang:1.24.1rc1\n"},
		{"dockerfile windows endings", "Dockerfile", "FROM golang:1.24.1\r\nRUN x\r\n", "FROM golang:1.24.7\r\nRUN x\r\n"},
		{"mise tools", ".mise.toml", "[tools]\nnode = \"20.1.0\"\ngo = \"1.24.1\" # pin\n", "[tools]\nnode = \"20.1.0\"\ngo = \"1.24.7\" # pin\n"},
		{"mise.toml name", "mise.toml", "[tools]\ngo = '1.24.1'\n", "[tools]\ngo = '1.24.7'\n"},
		{"mise minor only floats", ".mise.toml", "[tools]\ngo = \"1.24\"\n", "[tools]\ngo = \"1.24\"\n"},
		{"mise other section left", ".mise.toml", "[env]\ngo = \"1.24.1\"\n", "[env]\ngo = \"1.24.1\"\n"},
		{"mise latest left", ".mise.toml", "[tools]\ngo = \"latest\"\n", "[tools]\ngo = \"latest\"\n"},
		{"mise other tool left", ".mise.toml", "[tools]\ngolangci-lint = \"1.24.1\"\n", "[tools]\ngolangci-lint = \"1.24.1\"\n"},
		{"mise section ends", ".mise.toml", "[tools]\nnode = \"20\"\n[settings]\ngo = \"1.24.1\"\n", "[tools]\nnode = \"20\"\n[settings]\ngo = \"1.24.1\"\n"},
		{"devcontainer image", ".devcontainer/devcontainer.json", "{\n  \"image\": \"mcr.microsoft.com/devcontainers/go:1.24.1-bookworm\",\n  \"name\": \"1.24.1\"\n}\n", "{\n  \"image\": \"mcr.microsoft.com/devcontainers/go:1.24.7-bookworm\",\n  \"name\": \"1.24.1\"\n}\n"},
		{"devcontainer major-minor tag floats", ".devcontainer/devcontainer.json", "  \"image\": \"mcr.microsoft.com/devcontainers/go:1-1.24-bookworm\",\n", "  \"image\": \"mcr.microsoft.com/devcontainers/go:1-1.24-bookworm\",\n"},
		{"devcontainer dotfile", ".devcontainer.json", "  \"image\": \"golang:1.24.1\"\n", "  \"image\": \"golang:1.24.7\"\n"},
		{"devcontainer nested", ".devcontainer/go/devcontainer.json", "  \"image\": \"golang:1.24.1\"\n", "  \"image\": \"golang:1.24.7\"\n"},
		{"devcontainer other image", ".devcontainer/devcontainer.json", "  \"image\": \"mcr.microsoft.com/devcontainers/python:1.24.1\"\n", "  \"image\": \"mcr.microsoft.com/devcontainers/python:1.24.1\"\n"},
		{"devcontainer comment", ".devcontainer/devcontainer.json", "  // \"image\": \"golang:1.24.1\"\n", "  // \"image\": \"golang:1.24.1\"\n"},
		{"go.work go line", "go.work", "go 1.24.1\n\nuse (\n\t./a\n)\n", "go 1.24.7\n\nuse (\n\t./a\n)\n"},
		{"go.work toolchain line", "go.work", "go 1.24.1\ntoolchain go1.24.1\n", "go 1.24.7\ntoolchain go1.24.7\n"},
		{"go.work minor only floats", "go.work", "go 1.24\n", "go 1.24\n"},
		{"go.work other minor left", "go.work", "go 1.23.0\ntoolchain go1.23.4\n", "go 1.23.0\ntoolchain go1.23.4\n"},
		{"go.work replace not a version", "go.work", "go 1.24.1\nreplace x => ./go 1.24.1\n", "go 1.24.7\nreplace x => ./go 1.24.1\n"},
		{"makefile variable", "Makefile", "GO_VERSION ?= 1.24.1\nall:\n\tgo build\n", "GO_VERSION ?= 1.24.7\nall:\n\tgo build\n"},
		{"makefile golang version colon", "Makefile", "export GOLANG_VERSION := 1.24.1 # ci\n", "export GOLANG_VERSION := 1.24.7 # ci\n"},
		{"makefile include name", "build.mk", "GO_VERSION=1.24.1\n", "GO_VERSION=1.24.7\n"},
		{"makefile other variable", "Makefile", "NODE_VERSION = 1.24.1\nGOLANGCI_VERSION = 1.24.1\n", "NODE_VERSION = 1.24.1\nGOLANGCI_VERSION = 1.24.1\n"},
		{"makefile recipe line", "Makefile", "all:\n\tGO_VERSION=1.24.1 go build\n", "all:\n\tGO_VERSION=1.24.1 go build\n"},
		{"makefile expression", "Makefile", "GO_VERSION ?= $(shell cat .go-version)\n", "GO_VERSION ?= $(shell cat .go-version)\n"},
		{"makefile minor only floats", "Makefile", "GO_VERSION = 1.24\n", "GO_VERSION = 1.24\n"},
		{"unrelated file", "README.md", "FROM golang:1.24.1\n", "FROM golang:1.24.1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := FollowGoVersion(c.path, []byte(c.in), old, target)
			if string(got) != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
			if ok != (c.in != c.want) {
				t.Errorf("changed = %v for %q -> %q", ok, c.in, got)
			}
		})
	}
}

func TestFollowGoVersionAcrossMinors(t *testing.T) {
	// A directive bump moves the minor; a file spelled without a patch keeps that spelling.
	got, ok := FollowGoVersion(".go-version", []byte("1.24\n"), "v1.24", "v1.25.3")
	if !ok || string(got) != "1.25\n" {
		t.Errorf("got %q %v", got, ok)
	}
	got, ok = FollowGoVersion(".go-version", []byte("1.24.2\n"), "v1.24.0", "v1.25.3")
	if !ok || string(got) != "1.25.3\n" {
		t.Errorf("got %q %v", got, ok)
	}
}

func TestFollowGoVersionRefusesNonIncrease(t *testing.T) {
	if _, ok := FollowGoVersion(".go-version", []byte("1.24.1\n"), "v1.24.1", "v1.24.1"); ok {
		t.Error("same version must not change")
	}
	if _, ok := FollowGoVersion(".go-version", []byte("1.24.1\n"), "v1.24.1", "garbage"); ok {
		t.Error("garbage target must not change")
	}
}
