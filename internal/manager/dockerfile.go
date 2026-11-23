package manager

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/registry"
)

// Docker moves the base image tags of Dockerfiles and Containerfiles:
// `FROM [--flag=v]... image:tag [AS name]`, and the digest pin form
// `image:tag@sha256:...`, whose tag and digest move together.
//
// Only a reference ripple can read with certainty is tracked: a literal image name
// with a version-shaped tag (1.25, v2.3.4, 3.19-alpine). Untagged images, `latest`,
// named variants (bookworm), digest-only references, `scratch`, earlier stage names
// and anything built from a variable (`FROM node:${V}`, `FROM ${BASE}`) are left
// alone. `golang` images are the Go-version follow code's (see FollowGoVersion) and
// are never extracted here, so one line is never moved by two managers.
//
// Edits are exact-line: the platform flag, alias, spacing and the way the image is
// spelled (`nginx` or `docker.io/library/nginx`) are kept.
//
// The dependency's Package is "registry/repository" (docker.io/library/nginx) and
// its Version the tag. Apply takes the new tag, or "tag@sha256:..." when the caller
// has resolved the digest; a digest-pinned line without one is an error.
type Docker struct {
	Dir string
}

func (Docker) Name() string { return "docker" }

func (d Docker) Matches(p string) bool {
	if !config.InDirectory(d.Dir, p) {
		return false
	}
	b := path.Base(p)
	if b == "Dockerfile" || b == "Containerfile" {
		return true
	}
	ext := path.Ext(b)
	for _, doc := range []string{".md", ".txt", ".yml", ".yaml", ".json", ".toml"} {
		if ext == doc {
			return false
		}
	}
	return strings.HasPrefix(b, "Dockerfile.") || strings.HasPrefix(b, "Containerfile.") ||
		strings.HasSuffix(b, ".Dockerfile") || strings.HasSuffix(b, ".Containerfile")
}

// fromRE captures: 1 FROM and its flags, 2 the image token, 3 the rest of the line.
var fromRE = regexp.MustCompile(`(?i)^(\s*FROM\s+(?:--[A-Za-z-]+(?:=\S*)?\s+)*)(\S+)(.*)$`)

// imageRef is one parsed FROM image token.
type imageRef struct {
	name, tag, digest string
	pkg               string
}

// parseImage reads "name:tag[@digest]". It reports false for anything not tracked.
func parseImage(tok string) (imageRef, bool) {
	if strings.Contains(tok, "$") {
		return imageRef{}, false
	}
	var r imageRef
	rest, digest, hasDigest := strings.Cut(tok, "@")
	if hasDigest {
		r.digest = digest
		if !strings.HasPrefix(digest, "sha256:") {
			return imageRef{}, false
		}
	}
	slash := strings.LastIndex(rest, "/")
	colon := strings.LastIndex(rest, ":")
	if colon < 0 || colon < slash {
		return imageRef{}, false // untagged: floating by definition
	}
	r.name, r.tag = rest[:colon], rest[colon+1:]
	ref, ok := registry.ParseName(r.name)
	if !ok {
		return imageRef{}, false
	}
	if _, _, ok := registry.ParseTag(r.tag); !ok {
		return imageRef{}, false
	}
	r.pkg = ref.Package()
	if r.pkg == "docker.io/library/golang" {
		return imageRef{}, false
	}
	return r, true
}

func (d Docker) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		m := fromRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if r, ok := parseImage(m[2]); ok {
			dep := model.Dependency{File: p, Manager: d.Name(), Datasource: model.DatasourceDocker, Package: r.pkg, Version: r.tag}
			if !seen[dep] {
				seen[dep] = true
				out = append(out, dep)
			}
		}
	}
	return out, nil
}

func (d Docker) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a container image cannot be renamed", ErrUnsupported)
	}
	tag, digest, haveDigest := strings.Cut(newVersion, "@")
	lines := strings.Split(string(content), "\n")
	changed := false
	for i, line := range lines {
		m := fromRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		r, ok := parseImage(m[2])
		if !ok || r.pkg != dep.Package || r.tag != dep.Version {
			continue
		}
		tok := r.name + ":" + tag
		if r.digest != "" {
			if !haveDigest {
				return nil, fmt.Errorf("docker: %s:%s is pinned to a digest and none was resolved for %s", dep.Package, dep.Version, tag)
			}
			tok += "@" + digest
		}
		lines[i] = m[1] + tok + m[3]
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("docker: %s:%s not found", dep.Package, dep.Version)
	}
	return []byte(strings.Join(lines, "\n")), nil
}
