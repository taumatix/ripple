// Package registry reads the tags and manifest digests of container images from a
// registry that speaks the Docker Registry HTTP API v2 (the OCI distribution spec),
// anonymously. See docs/vendor/registry for the excerpts it was written from.
package registry

import (
	"regexp"
	"strings"
)

// DockerHub is the registry an unqualified image name lives on.
const DockerHub = "docker.io"

// Ref is a repository on a registry: no tag, no digest.
type Ref struct {
	Registry string // host[:port], "docker.io" for Docker Hub
	Repo     string // path, "library/nginx" for a bare "nginx"
}

// Package is the identity ripple stores for the image: "registry/repo".
func (r Ref) Package() string { return r.Registry + "/" + r.Repo }

var repoRE = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
var hostRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]{1,5})?$`)

// ParseName parses an image name as written in a Dockerfile, without tag or digest.
// It reports false for anything that is not a plain name (a variable, an upper-case
// repository), so a reference ripple cannot be sure of is never tracked.
func ParseName(name string) (Ref, bool) {
	if name == "" {
		return Ref{}, false
	}
	host, rest := DockerHub, name
	if i := strings.IndexByte(name, '/'); i > 0 {
		first := name[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			if !hostRE.MatchString(first) {
				return Ref{}, false
			}
			host, rest = first, name[i+1:]
		}
	}
	switch host {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		host = DockerHub
	}
	if !repoRE.MatchString(rest) {
		return Ref{}, false
	}
	if host == DockerHub && !strings.Contains(rest, "/") {
		rest = "library/" + rest
	}
	return Ref{Registry: host, Repo: rest}, true
}

// ParsePackage is the inverse of Ref.Package.
func ParsePackage(pkg string) (Ref, bool) {
	i := strings.IndexByte(pkg, '/')
	if i <= 0 || i == len(pkg)-1 {
		return Ref{}, false
	}
	return Ref{Registry: pkg[:i], Repo: pkg[i+1:]}, true
}
