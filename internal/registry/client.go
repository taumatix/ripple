package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrNotFound is a repository or tag the registry does not have.
	ErrNotFound = errors.New("registry: not found")
	// ErrDenied is a repository the registry will not serve anonymously.
	ErrDenied = errors.New("registry: anonymous access denied")
)

const (
	maxPages     = 50
	maxTags      = 100000
	maxBody      = 8 << 20
	tagsPageSize = 1000
)

// Client reads from registries anonymously: it never sends a credential, so a private
// image is ErrDenied rather than fetched (credentials are not supported).
type Client struct {
	// HTTP is the transport. Nil means a client that refuses to connect to a
	// loopback, private, link-local or otherwise non-public address, because the
	// registry host is whatever a repository's Dockerfile names.
	HTTP *http.Client
	// BaseURL maps a registry host to the URL its API is served at. Nil means
	// https://host, with Docker Hub at registry-1.docker.io.
	BaseURL func(host string) string
	// Insecure allows http registries and token realms; for tests against a fake.
	Insecure bool
	// UserAgent is sent with every request when set.
	UserAgent string
}

var safeHTTP = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: refusePrivate}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	},
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" {
			return errors.New("redirect to a non-https URL")
		}
		return nil
	},
}

// refusePrivate runs after name resolution, on the address about to be dialled, so a
// name that resolves to an internal address or a redirect to one is refused too.
func refusePrivate(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	a := ap.Addr().Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return fmt.Errorf("registry: refusing to connect to non-public address %s", a)
	}
	return nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return safeHTTP
}

func (c *Client) base(host string) string {
	if c.BaseURL != nil {
		return c.BaseURL(host)
	}
	if host == DockerHub {
		host = "registry-1.docker.io"
	}
	return "https://" + host
}

// Tags returns every tag of pkg ("registry/repo"), following Link headers.
func (c *Client) Tags(ctx context.Context, pkg string) ([]string, error) {
	ref, ok := ParsePackage(pkg)
	if !ok {
		return nil, fmt.Errorf("registry: %q is not a registry/repository name", pkg)
	}
	next := c.base(ref.Registry) + "/v2/" + ref.Repo + "/tags/list?n=" + fmt.Sprint(tagsPageSize)
	var out []string
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("registry: %s has more than %d tag pages", pkg, maxPages)
		}
		resp, err := c.do(ctx, http.MethodGet, next, ref.Repo, "application/json")
		if err != nil {
			return nil, err
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("registry: tags of %s: %w", pkg, err)
		}
		out = append(out, body.Tags...)
		if len(out) > maxTags {
			return nil, fmt.Errorf("registry: %s has more than %d tags", pkg, maxTags)
		}
		if next, err = nextLink(next, link); err != nil {
			return nil, err
		}
	}
	return out, nil
}

var linkRE = regexp.MustCompile(`<([^>]*)>\s*;\s*rel="?next"?`)

// nextLink resolves a rel="next" Link against the request URL; it must stay on the host.
func nextLink(cur, header string) (string, error) {
	m := linkRE.FindStringSubmatch(header)
	if m == nil {
		return "", nil
	}
	base, err := url.Parse(cur)
	if err != nil {
		return "", err
	}
	u, err := base.Parse(m[1])
	if err != nil {
		return "", err
	}
	if u.Host != base.Host || u.Scheme != base.Scheme {
		return "", fmt.Errorf("registry: Link header leaves the registry (%s)", u.Host)
	}
	return u.String(), nil
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Digest returns the manifest digest of pkg:tag (of the index for a multi-platform
// image), the value a `@sha256:...` pin holds.
func (c *Client) Digest(ctx context.Context, pkg, tag string) (string, error) {
	ref, ok := ParsePackage(pkg)
	if !ok {
		return "", fmt.Errorf("registry: %q is not a registry/repository name", pkg)
	}
	resp, err := c.do(ctx, http.MethodHead, c.base(ref.Registry)+"/v2/"+ref.Repo+"/manifests/"+url.PathEscape(tag), ref.Repo,
		"application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	d := resp.Header.Get("Docker-Content-Digest")
	if !digestRE.MatchString(d) {
		return "", fmt.Errorf("registry: %s:%s: no usable Docker-Content-Digest (%q)", pkg, tag, d)
	}
	return d, nil
}

// do issues a request, answering one anonymous Bearer challenge. The caller closes the body.
func (c *Client) do(ctx context.Context, method, target, repo, accept string) (*http.Response, error) {
	if !c.Insecure && !strings.HasPrefix(target, "https://") {
		return nil, fmt.Errorf("registry: %s is not https", target)
	}
	token := ""
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.http().Do(req)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, nil
		case resp.StatusCode == http.StatusUnauthorized && attempt == 0:
			challenge := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			if token, err = c.token(ctx, challenge, repo); err != nil {
				return nil, err
			}
			continue
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, fmt.Errorf("%w: %s", ErrNotFound, target)
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, fmt.Errorf("%w: %s", ErrDenied, target)
		case http.StatusTooManyRequests:
			return nil, fmt.Errorf("registry: rate limited by %s (Retry-After %q)", target, resp.Header.Get("Retry-After"))
		}
		return nil, fmt.Errorf("registry: %s %s: %s", method, target, resp.Status)
	}
}

var paramRE = regexp.MustCompile(`(\w+)="([^"]*)"`)

// token answers a `Bearer realm=...,service=...,scope=...` challenge without credentials.
func (c *Client) token(ctx context.Context, challenge, repo string) (string, error) {
	scheme, params, _ := strings.Cut(strings.TrimSpace(challenge), " ")
	if !strings.EqualFold(scheme, "bearer") {
		return "", fmt.Errorf("%w: the registry asks for %q authentication", ErrDenied, scheme)
	}
	p := map[string]string{}
	for _, m := range paramRE.FindAllStringSubmatch(params, -1) {
		p[m[1]] = m[2]
	}
	realm, err := url.Parse(p["realm"])
	if err != nil || realm.Host == "" || (realm.Scheme != "https" && !(c.Insecure && realm.Scheme == "http")) {
		return "", fmt.Errorf("registry: token realm %q is not an https URL", p["realm"])
	}
	q := realm.Query()
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	scope := p["scope"]
	if scope == "" {
		scope = "repository:" + repo + ":pull"
	}
	q.Set("scope", scope)
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: token for %s", ErrDenied, repo)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry: token endpoint: %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return "", fmt.Errorf("registry: token response: %w", err)
	}
	if body.Token == "" {
		body.Token = body.AccessToken
	}
	if body.Token == "" {
		return "", errors.New("registry: token response carries no token")
	}
	return body.Token, nil
}
