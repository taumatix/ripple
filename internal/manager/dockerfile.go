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
// Also read: `COPY --from=image:tag`, `ARG V=1.2` (before the first FROM) used as the
// whole tag of exactly one image (`FROM image:${V}`; the ARG line moves, and an ARG
// shared by two different images is left alone), and the `image:` key of Docker
// Compose files (docker-compose*.yml, compose*.yml, *.compose.yml) and of Kubernetes
// manifests (a .yml/.yaml file below a k8s, kubernetes, kube, manifests, deploy,
// deployment or deployments directory that has apiVersion and kind). Those are
// edited line by line as well, keeping quotes, indentation and comments;
// `image: ${IMG:-nginx:1.25}` is not tracked.
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
	return isDockerfilePath(p) || isComposeFile(p) || isManifestPath(p)
}

func isDockerfilePath(p string) bool {
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

func isYAML(p string) bool {
	e := path.Ext(p)
	return e == ".yml" || e == ".yaml"
}

func isComposeFile(p string) bool {
	if !isYAML(p) {
		return false
	}
	b := strings.TrimSuffix(strings.TrimSuffix(path.Base(p), ".yaml"), ".yml")
	return strings.HasPrefix(b, "docker-compose") || strings.HasPrefix(b, "compose") || strings.HasSuffix(b, ".compose")
}

func isManifestPath(p string) bool {
	if !isYAML(p) {
		return false
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "k8s", "kubernetes", "kube", "manifests", "deploy", "deployment", "deployments":
			return true
		}
	}
	return false
}

// fromRE captures: 1 FROM and its flags, 2 the image token, 3 the rest of the line.
var fromRE = regexp.MustCompile(`(?i)^(\s*FROM\s+(?:--[A-Za-z-]+(?:=\S*)?\s+)*)(\S+)(.*)$`)

// imageRef is one parsed FROM image token.
type imageRef struct {
	name, tag, digest string
	pkg               string
}

// parseImage reads "name:tag[@digest]". It reports false for anything not tracked.
func parseImage(tok string) (imageRef, bool) { return parseImageOpt(tok, false) }

// parseImageOpt is parseImage; golang images are returned when keepGolang (a compose
// or manifest file, which the Go version follow code does not edit).
func parseImageOpt(tok string, keepGolang bool) (imageRef, bool) {
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
	if r.pkg == "docker.io/library/golang" && !keepGolang {
		return imageRef{}, false
	}
	return r, true
}

// copyFromRE captures: 1 COPY, its flags and "--from=", 2 the source, 3 the rest.
var copyFromRE = regexp.MustCompile(`(?i)^(\s*COPY\s+(?:--[A-Za-z-]+(?:=\S*)?\s+)*?--from=)(\S+)(.*)$`)

// argRE captures: 1 the prefix up to the value, 2 the name, 3 the value, 4 the rest.
var argRE = regexp.MustCompile(`(?i)^(\s*ARG\s+([A-Za-z_][A-Za-z0-9_]*)=)([^\s"']+)(\s*(?:#.*)?)$`)

// varTagRE splits "name:${V}" and "name:$V".
var varTagRE = regexp.MustCompile(`^([^$@:]+(?::[0-9]+)?(?:/[^$@:]+)*):\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$`)

// yamlImageRE captures: 1 the key, 2 an opening quote, 3 the image, 4 the closing quote, 5 the rest.
var yamlImageRE = regexp.MustCompile(`^(\s*(?:-\s+)?image:\s*)(["']?)([^\s"'#]+)(["']?)(\s*(?:#.*)?)$`)

// occurrence is one tracked image reference in a file.
type occurrence struct {
	line int
	ref  imageRef
	// How a literal line is rebuilt around the image token.
	prefix, quoteOpen, quoteClose, rest string
	arg                                 string // the ARG the tag comes from, "" for a literal
}

func isManifest(content string) bool {
	return strings.Contains(content, "apiVersion:") && strings.Contains(content, "kind:")
}

// scan finds the tracked references of a file. golang images are kept when keepGolang.
func scan(p string, lines []string) []occurrence {
	if isYAML(p) {
		if !isComposeFile(p) && !isManifest(strings.Join(lines, "\n")) {
			return nil
		}
		var out []occurrence
		for i, line := range lines {
			if m := yamlImageRE.FindStringSubmatch(line); m != nil {
				if r, ok := parseImageOpt(m[3], true); ok {
					out = append(out, occurrence{line: i, ref: r, prefix: m[1], quoteOpen: m[2], quoteClose: m[4], rest: m[5]})
				}
			}
		}
		return out
	}
	args := map[string]string{} // global ARG defaults, before the first FROM
	argLine := map[string]int{}
	var out []occurrence
	seenFrom := false
	for i, line := range lines {
		if m := fromRE.FindStringSubmatch(line); m != nil {
			seenFrom = true
			out = appendRef(out, i, m, args, argLine)
			continue
		}
		if m := copyFromRE.FindStringSubmatch(line); m != nil {
			out = appendRef(out, i, m, args, argLine)
			continue
		}
		if !seenFrom {
			if m := argRE.FindStringSubmatch(line); m != nil {
				args[m[2]], argLine[m[2]] = m[3], i
			}
		}
	}
	// An ARG feeding two different images cannot be moved for one of them.
	users := map[string]map[string]bool{}
	for _, o := range out {
		if o.arg != "" {
			if users[o.arg] == nil {
				users[o.arg] = map[string]bool{}
			}
			users[o.arg][o.ref.pkg] = true
		}
	}
	kept := out[:0]
	for _, o := range out {
		if o.arg == "" || len(users[o.arg]) == 1 {
			kept = append(kept, o)
		}
	}
	return kept
}

func appendRef(out []occurrence, i int, m []string, args map[string]string, argLine map[string]int) []occurrence {
	tok := m[2]
	if v := varTagRE.FindStringSubmatch(tok); v != nil {
		val, ok := args[v[2]]
		if !ok {
			return out
		}
		r, ok := parseImage(v[1] + ":" + val)
		if !ok {
			return out
		}
		return append(out, occurrence{line: argLine[v[2]], ref: r, arg: v[2]})
	}
	if r, ok := parseImage(tok); ok {
		return append(out, occurrence{line: i, ref: r, prefix: m[1], rest: m[3]})
	}
	return out
}

func (d Docker) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, o := range scan(p, strings.Split(string(content), "\n")) {
		dep := model.Dependency{File: p, Manager: d.Name(), Datasource: model.DatasourceDocker, Package: o.ref.pkg, Version: o.ref.tag}
		if !seen[dep] {
			seen[dep] = true
			out = append(out, dep)
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
	for _, o := range scan(dep.File, lines) {
		if o.ref.pkg != dep.Package || o.ref.tag != dep.Version {
			continue
		}
		if o.arg != "" {
			m := argRE.FindStringSubmatch(lines[o.line])
			lines[o.line] = m[1] + tag + m[4]
			changed = true
			continue
		}
		tok := o.ref.name + ":" + tag
		if o.ref.digest != "" {
			if !haveDigest {
				return nil, fmt.Errorf("docker: %s:%s is pinned to a digest and none was resolved for %s", dep.Package, dep.Version, tag)
			}
			tok += "@" + digest
		}
		lines[o.line] = o.prefix + o.quoteOpen + tok + o.quoteClose + o.rest
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("docker: %s:%s not found", dep.Package, dep.Version)
	}
	return []byte(strings.Join(lines, "\n")), nil
}
