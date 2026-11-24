package registry

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
)

// Credentials are per-registry logins for private images, read from a Docker
// `config.json` (the file `docker login` writes and Kubernetes pull secrets carry as
// `.dockerconfigjson`). A login is sent only to the registry (and the token endpoint
// it names) it was saved for, never to another host a Dockerfile happens to mention.
//
// Read: `auths` entries with `auth` (base64 "user:password"), `username` and `password`,
// or `identitytoken` (an OAuth2 refresh token, presented only to a Bearer token endpoint).
// Not read: `credsStore` and `credHelpers` (external helper programs); a registry that only
// has those stays anonymous, and Warnings says so. The value never prints: String, GoString and
// LogValue are all redacted.
type Credentials struct {
	byHost map[string]login
	// Warnings lists what the file held that was not used, as plain sentences with no
	// secret in them.
	Warnings []string
}

type login struct{ user, pass, identity string }

func (*Credentials) String() string   { return "registry.Credentials{redacted}" }
func (*Credentials) GoString() string { return "registry.Credentials{redacted}" }

// LogValue keeps the logins out of structured logs.
func (*Credentials) LogValue() slog.Value { return slog.StringValue("redacted") }

// Hosts lists the registries a login is held for, sorted.
func (c *Credentials) Hosts() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.byHost))
	for h := range c.byHost {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// For returns the login for a registry host in the form Ref.Registry has it
// ("docker.io", "ghcr.io", "registry.example.com:5000").
func (c *Credentials) For(host string) (user, pass string, ok bool) {
	if c == nil {
		return "", "", false
	}
	l, ok := c.byHost[host]
	if l.user == "" && l.pass == "" {
		return "", "", false
	}
	return l.user, l.pass, ok
}

// IdentityToken returns the OAuth2 refresh token saved for a registry host (an
// `identitytoken` entry), which only a Bearer token endpoint can be given. A registry with a
// user and password as well uses the password.
func (c *Credentials) IdentityToken(host string) (string, bool) {
	if c == nil {
		return "", false
	}
	l, ok := c.byHost[host]
	if !ok || l.identity == "" || l.user != "" || l.pass != "" {
		return "", false
	}
	return l.identity, true
}

// LoadDockerConfig reads a Docker config.json from path.
func LoadDockerConfig(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	return ParseDockerConfig(data)
}

// ParseDockerConfig parses the content of a Docker config.json.
func ParseDockerConfig(data []byte) (*Credentials, error) {
	var f struct {
		Auths map[string]struct {
			Auth          string `json:"auth"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			IdentityToken string `json:"identitytoken"`
		} `json:"auths"`
		CredsStore  string            `json:"credsStore"`
		CredHelpers map[string]string `json:"credHelpers"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("registry: not a Docker config.json: %w", err)
	}
	c := &Credentials{byHost: map[string]login{}}
	for key, e := range f.Auths {
		host := configHost(key)
		l := login{user: e.Username, pass: e.Password}
		if e.Auth != "" {
			raw, err := base64.StdEncoding.DecodeString(e.Auth)
			user, pass, ok := strings.Cut(string(raw), ":")
			if err != nil || !ok {
				c.Warnings = append(c.Warnings, fmt.Sprintf("the auth entry for %s is not base64 user:password and was ignored", host))
				continue
			}
			l = login{user: user, pass: pass}
		}
		l.identity = e.IdentityToken
		if l.user == "" && l.pass == "" && l.identity == "" {
			continue
		}
		c.byHost[host] = l
	}
	if f.CredsStore != "" || len(f.CredHelpers) > 0 {
		c.Warnings = append(c.Warnings, "credsStore and credHelpers are not supported; only inline auths are read")
	}
	sort.Strings(c.Warnings)
	return c, nil
}

// configHost turns a config.json key ("https://index.docker.io/v1/", "ghcr.io") into a
// Ref.Registry host.
func configHost(key string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.TrimPrefix(strings.TrimPrefix(k, "https://"), "http://")
	if i := strings.IndexByte(k, '/'); i >= 0 {
		k = k[:i]
	}
	switch k {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return DockerHub
	}
	return k
}
