package github

import (
	"net/url"
	"strings"
	"sync"
)

// FallbackAPIVersion is what GitHub and GHES apply to a request that names no
// version, and the oldest version ripple falls back to (docs/vendor/ghes).
const FallbackAPIVersion = "2022-11-28"

// GraphQLURL derives the GraphQL endpoint from a REST base. github.com serves it
// at api.github.com/graphql; GHES serves REST at HOST/api/v3 and GraphQL at
// HOST/api/graphql, so a base ending in /api/v3 swaps that suffix. Anything else
// gets /graphql appended. A client's GraphQLURL overrides this.
func GraphQLURL(apiBase string) string {
	b := strings.TrimRight(apiBase, "/")
	if u, err := url.Parse(b); err == nil && strings.HasSuffix(u.Path, "/api/v3") {
		u.Path = strings.TrimSuffix(u.Path, "/v3") + "/graphql"
		return u.String()
	}
	return b + "/graphql"
}

// graphQLURL is the endpoint this client posts GraphQL to.
func (c *Client) graphQLURL() string {
	if c.GraphQLURL != "" {
		return c.GraphQLURL
	}
	return GraphQLURL(c.APIBase)
}

// Versions holds the X-GitHub-Api-Version a host is called with, shared by the
// client and its token source. Unless pinned, a 410 Gone (the answer for a
// version the server does not support, such as an older GHES asked for
// APIVersion) lowers it once to FallbackAPIVersion and the request is retried.
// The zero value starts at APIVersion, unpinned; a nil *Versions behaves so too.
type Versions struct {
	mu      sync.Mutex
	current string
	pinned  bool
}

// PinnedVersions sends v on every request and never falls back; "" is unpinned.
func PinnedVersions(v string) *Versions {
	if v == "" {
		return &Versions{}
	}
	return &Versions{current: v, pinned: true}
}

// Current is the version to send now.
func (v *Versions) Current() string {
	if v == nil {
		return APIVersion
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.current == "" {
		return APIVersion
	}
	return v.current
}

// Lower is called after a 410 for version sent; it reports whether a retry with
// a different version is worthwhile.
func (v *Versions) Lower(sent string) bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.pinned {
		return false
	}
	cur := v.current
	if cur == "" {
		cur = APIVersion
	}
	if cur != sent {
		return true // someone else already lowered it
	}
	if cur == FallbackAPIVersion {
		return false
	}
	v.current = FallbackAPIVersion
	return true
}
