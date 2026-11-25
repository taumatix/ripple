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
// Also read: `COPY --from=image:tag` and `RUN --mount=...,from=image:tag`, `ARG V=1.2` (before the first
// FROM, or inside the stage that uses it in a COPY --from or --mount) used as the
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
	return isDockerfilePath(p) || isComposeFile(p) || isManifestPath(p) || isKustomization(p) || isHelmValues(p)
}

// isKustomization reports a kustomization.yaml, kustomization.yml or Kustomization file.
func isKustomization(p string) bool {
	switch path.Base(p) {
	case "kustomization.yaml", "kustomization.yml", "Kustomization":
		return true
	}
	return false
}

// isHelmValues reports a Helm values file: values.yaml, values-prod.yaml, values.prod.yml,
// prod-values.yaml or prod.values.yaml.
func isHelmValues(p string) bool {
	if !isYAML(p) {
		return false
	}
	b := strings.TrimSuffix(strings.TrimSuffix(path.Base(p), ".yaml"), ".yml")
	return b == "values" || strings.HasPrefix(b, "values-") || strings.HasPrefix(b, "values.") ||
		strings.HasSuffix(b, "-values") || strings.HasSuffix(b, ".values")
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

// mountFromRE captures: 1 the RUN line up to "from=" of its first --mount that has one, 2 the
// source, 3 the rest. The source is an image only when it has a literal tag.
var mountFromRE = regexp.MustCompile(`(?i)^(\s*RUN\s+(?:\S+\s+)*?--mount=(?:[^\s,]+,)*?from=)([^\s,]+)(.*)$`)

// argRedeclareRE captures the name of a bare `ARG NAME` (no default).
var argRedeclareRE = regexp.MustCompile(`(?i)^\s*ARG\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:#.*)?$`)

// argRE captures: 1 the prefix up to the value, 2 the name, 3 the value, 4 the rest.
var argRE = regexp.MustCompile(`(?i)^(\s*ARG\s+([A-Za-z_][A-Za-z0-9_]*)=)([^\s"']+)(\s*(?:#.*)?)$`)

// varTagRE splits "name:${V}" and "name:$V", and "name:${V}-alpine" (group 3 is the literal
// text after the variable).
var varTagRE = regexp.MustCompile(`^([^$@:]+(?::[0-9]+)?(?:/[^$@:]+)*):\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?([^$@:/{}\s]*)$`)

// yamlImageRE captures: 1 the key, 2 an opening quote, 3 the image, 4 the closing quote, 5 the rest.
var yamlImageRE = regexp.MustCompile(`^(\s*(?:-\s+)?image:\s*)(["']?)([^\s"'#]+)(["']?)(\s*(?:#.*)?)$`)

// occurrence is one tracked image reference in a file.
type occurrence struct {
	line int
	ref  imageRef
	// How a literal line is rebuilt around the image token.
	prefix, quoteOpen, quoteClose, rest string
	arg                                 string // the ARG the tag comes from, "" for a literal
	// tagOnly: the edited token is only the tag (a Kustomize newTag, a Helm values tag), and
	// the image name lives on another line.
	tagOnly bool
	// dig is the `newDigest:` / `digest:` line of a tag-only reference pinned by tag and
	// digest together; nil when there is none. The digest moves with the tag.
	dig *digestLine
	// argSuffix is the literal text after `${V}` in a tag (`-alpine` in `image:${V}-alpine`);
	// the ARG holds the tag without it.
	argSuffix string
}

// digestLine is a YAML line holding the digest that goes with a tag on another line.
type digestLine struct {
	line                                int
	prefix, quoteOpen, quoteClose, rest string
}

func newDigestLine(i int, key string, m []string) *digestLine {
	return &digestLine{line: i, prefix: m[1] + key + ": ", quoteOpen: m[3], quoteClose: m[5], rest: m[6]}
}

func isManifest(content string) bool {
	return strings.Contains(content, "apiVersion:") && strings.Contains(content, "kind:")
}

// scan finds the tracked references of a file. golang images are kept when keepGolang.
func scan(p string, lines []string) []occurrence {
	if isKustomization(p) {
		return scanKustomize(lines)
	}
	if isHelmValues(p) && !isComposeFile(p) {
		return scanHelmValues(lines)
	}
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
	global := map[string]argDef{} // ARG defaults before the first FROM
	var stage map[string]argDef   // ARG defaults declared since the last FROM
	var out []occurrence
	seenFrom := false
	for i, line := range lines {
		if m := fromRE.FindStringSubmatch(line); m != nil {
			seenFrom = true
			stage = map[string]argDef{}
			out = appendRef(out, i, m, global)
			continue
		}
		if m := copyFromRE.FindStringSubmatch(line); m != nil {
			out = appendRef(out, i, m, stage, global)
			continue
		}
		if m := mountFromRE.FindStringSubmatch(line); m != nil {
			out = appendRef(out, i, m, stage, global)
			continue
		}
		if m := argRE.FindStringSubmatch(line); m != nil {
			if !seenFrom {
				global[m[2]] = argDef{m[3], i}
			} else {
				stage[m[2]] = argDef{m[3], i}
			}
			continue
		}
		// `ARG V` inside a stage re-declares the global V with its default.
		if m := argRedeclareRE.FindStringSubmatch(line); m != nil && seenFrom {
			if g, ok := global[m[1]]; ok {
				stage[m[1]] = g
			}
		}
	}
	// An ARG feeding two different images cannot be moved for one of them.
	users := map[int]map[string]bool{} // ARG line -> images
	for _, o := range out {
		if o.arg != "" {
			if users[o.line] == nil {
				users[o.line] = map[string]bool{}
			}
			users[o.line][o.ref.pkg] = true
		}
	}
	kept := out[:0]
	for _, o := range out {
		if o.arg == "" || len(users[o.line]) == 1 {
			kept = append(kept, o)
		}
	}
	return kept
}

// argDef is an ARG default and the line that declares it.
type argDef struct {
	val  string
	line int
}

// appendRef records the image of one FROM, COPY --from or RUN --mount line. A tag taken
// from an ARG is looked up in the scopes given, innermost first.
func appendRef(out []occurrence, i int, m []string, scopes ...map[string]argDef) []occurrence {
	tok := m[2]
	if v := varTagRE.FindStringSubmatch(tok); v != nil {
		for _, sc := range scopes {
			if def, ok := sc[v[2]]; ok {
				r, ok := parseImage(v[1] + ":" + def.val + v[3])
				if !ok {
					return out
				}
				return append(out, occurrence{line: def.line, ref: r, arg: v[2], argSuffix: v[3]})
			}
		}
		return out
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
			if !strings.HasSuffix(tag, o.argSuffix) {
				return nil, fmt.Errorf("%w: %s does not end in %q, which the Dockerfile adds after ${%s}", ErrUnsupported, tag, o.argSuffix, o.arg)
			}
			m := argRE.FindStringSubmatch(lines[o.line])
			lines[o.line] = m[1] + strings.TrimSuffix(tag, o.argSuffix) + m[4]
			changed = true
			continue
		}
		if o.tagOnly {
			if o.dig != nil {
				if !haveDigest {
					return nil, fmt.Errorf("docker: %s:%s is pinned to a digest and none was resolved for %s", dep.Package, dep.Version, tag)
				}
				lines[o.dig.line] = o.dig.prefix + o.dig.quoteOpen + digest + o.dig.quoteClose + o.dig.rest
			}
			lines[o.line] = o.prefix + o.quoteOpen + tag + o.quoteClose + o.rest
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

// yamlKeyRE captures: 1 indent (with a list dash), 2 the key, 3 an opening quote, 4 the value,
// 5 the closing quote, 6 the rest of the line.
var yamlKeyRE = regexp.MustCompile(`^(\s*(?:-\s+)?)([A-Za-z][A-Za-z0-9_.-]*):\s*(["']?)([^\s"'#]*)(["']?)(\s*(?:#.*)?)$`)

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func blankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// scanKustomize reads the `images:` list of a kustomization: `name` (or `newName`) with
// `newTag`. An entry without a `newTag` is left alone; one with a `newDigest` as well is
// moved as a pair, the digest line following the tag.
func scanKustomize(lines []string) []occurrence {
	var out []occurrence
	inImages := false
	var name, newName string
	var dig []string // the yamlKeyRE match of a non-empty newDigest
	var digLine int
	var tag []string // the yamlKeyRE match of newTag
	var tagLine int
	flush := func() {
		if tag != nil {
			img := name
			if newName != "" {
				img = newName
			}
			if r, ok := parseImageOpt(img+":"+tag[4], true); ok {
				o := occurrence{line: tagLine, ref: r, tagOnly: true,
					prefix: tag[1] + "newTag: ", quoteOpen: tag[3], quoteClose: tag[5], rest: tag[6]}
				switch {
				case dig == nil:
					out = append(out, o)
				case strings.HasPrefix(dig[4], "sha256:"):
					o.dig = newDigestLine(digLine, "newDigest", dig)
					out = append(out, o)
				}
			}
		}
		name, newName, dig, tag = "", "", nil, nil
	}
	for i, line := range lines {
		if blankOrComment(line) {
			continue
		}
		if indentOf(line) == 0 && !strings.HasPrefix(line, "-") {
			flush()
			inImages = strings.HasPrefix(line, "images:")
			continue
		}
		if !inImages {
			continue
		}
		m := yamlKeyRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.Contains(m[1], "-") {
			flush() // a new list item
		}
		switch m[2] {
		case "name":
			name = m[4]
		case "newName":
			newName = m[4]
		case "newDigest":
			if m[4] != "" {
				dig, digLine = m, i
			}
		case "newTag":
			tag, tagLine = m, i
		}
	}
	flush()
	return out
}

// scanHelmValues reads, besides a single-string `image: repo:tag` under any key containing
// "image", `repository:` and `tag:` pairs (with an optional `registry:`) that
// are siblings under a key whose name contains "image" (image, initImage, ...). A pair with
// a `digest:`, a templated value or a repository that is a URL is left alone.
func scanHelmValues(lines []string) []occurrence {
	var out []occurrence
	for i, line := range lines {
		m := yamlKeyRE.FindStringSubmatch(line)
		if m != nil && m[2] == "image" {
			// ingress-nginx writes `registry:`, `image: path/name` and `tag:` under `image:`,
			// with no `repository:`; when the tag sibling is there it is that pair.
			if o, ok := helmPair(lines, i, m); ok {
				out = append(out, o)
				continue
			}
		}
		if m != nil && m[2] != "repository" && strings.Contains(strings.ToLower(m[2]), "image") && m[4] != "" && !strings.ContainsAny(m[4], "{}") && !strings.Contains(m[4], "://") {
			// `image: repo:tag` (or `initImage: ...`): the whole reference on one line.
			if r, ok := parseImageOpt(m[4], true); ok {
				out = append(out, occurrence{line: i, ref: r, prefix: line[:len(line)-len(m[3]+m[4]+m[5]+m[6])],
					quoteOpen: m[3], quoteClose: m[5], rest: m[6]})
			}
			continue
		}
		if m == nil || m[2] != "repository" {
			continue
		}
		if o, ok := helmPair(lines, i, m); ok {
			out = append(out, o)
		}
	}
	return out
}

// helmPair reads the `registry:`, repository-key and `tag:` (and `digest:`) siblings
// around the line i that holds the repository (m is its parsed key line): the key is
// `repository`, or `image` under an image key when the chart has no `repository:`.
func helmPair(lines []string, i int, m []string) (occurrence, bool) {
	line := lines[i]
	if m == nil || m[4] == "" || strings.Contains(m[1], "-") {
		return occurrence{}, false
	}
	n := indentOf(line)
	parent := -1
	for j := i - 1; j >= 0; j-- {
		if blankOrComment(lines[j]) {
			continue
		}
		if indentOf(lines[j]) < n {
			parent = j
			break
		}
	}
	if parent < 0 {
		return occurrence{}, false
	}
	pm := yamlKeyRE.FindStringSubmatch(lines[parent])
	if pm == nil || pm[4] != "" || !strings.Contains(strings.ToLower(pm[2]), "image") {
		return occurrence{}, false
	}
	var registryHost string
	var tag []string
	tagLine := 0
	var dig []string
	digLine := 0
	for j := parent + 1; j < len(lines); j++ {
		if blankOrComment(lines[j]) {
			continue
		}
		if indentOf(lines[j]) < n {
			break
		}
		if indentOf(lines[j]) != n {
			continue
		}
		km := yamlKeyRE.FindStringSubmatch(lines[j])
		if km == nil {
			continue
		}
		switch km[2] {
		case "registry":
			registryHost = km[4]
		case "digest":
			if km[4] != "" {
				dig, digLine = km, j
			}
		case "tag":
			tag, tagLine = km, j
		}
	}
	repo := m[4]
	if registryHost != "" {
		repo = registryHost + "/" + repo
	}
	if tag == nil || strings.ContainsAny(repo+tag[4], "{}$") || strings.Contains(repo, "://") {
		return occurrence{}, false
	}
	if dig != nil && !strings.HasPrefix(dig[4], "sha256:") {
		return occurrence{}, false
	}
	if r, ok := parseImageOpt(repo+":"+tag[4], true); ok {
		o := occurrence{line: tagLine, ref: r, tagOnly: true,
			prefix: tag[1] + "tag: ", quoteOpen: tag[3], quoteClose: tag[5], rest: tag[6]}
		if dig != nil {
			o.dig = newDigestLine(digLine, "digest", dig)
		}
		return o, true
	}
	return occurrence{}, false
}
