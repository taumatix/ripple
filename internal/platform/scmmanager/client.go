// Package scmmanager implements platform.Platform for SCM-Manager 3.x: its REST API v2
// and the Review plugin's pull requests (Review plugin 3.11.0 or later). The vendored
// documentation and source excerpts it was written from are in docs/vendor/scm-manager;
// docs/platform-scmmanager.md says how to configure it and what is unverified.
package scmmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/sandbox"
)

// GitUsername is the basic-auth user git sends with the API key as the password.
// SCM-Manager ignores the username for API keys (docs/vendor/scm-manager/source-excerpts.md#auth).
const GitUsername = "ripple"

// Media types of the v2 resources ripple uses (source-excerpts.md#pr-create and siblings).
const (
	mtPR         = "application/vnd.scmm-pullRequest+json;v=2"
	mtPRs        = "application/vnd.scmm-pullRequestCollection+json;v=2"
	mtRepos      = "application/vnd.scmm-repositoryCollection+json;v=2"
	mtDefault    = "application/vnd.scmm-gitDefaultBranch+json;v=2"
	mtModifs     = "application/vnd.scmm-modifications+json;v=2"
	pageSize     = 100
	maxChangeset = 100 // changesets of one push asked for their files before a re-scan is cheaper
)

var (
	repoName   = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	changesetR = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// Client is the SCM-Manager platform: one instance, one API key.
type Client struct {
	// BaseURL is the instance's context path, e.g. https://scm.example.com/scm. The API
	// is BaseURL/api/v2 and the clone URL BaseURL/repo/<namespace>/<name>.
	BaseURL string
	// Credentials supplies the API key. Its Secret is sent as a Bearer token on the API
	// and as the git password under GitUsername.
	Credentials platform.Credentials
	HTTP        *http.Client
	Log         *slog.Logger
	// AuthorName and AuthorEmail identify commits.
	AuthorName, AuthorEmail string
	// WorkDir is where checkouts are created; empty means the OS temp dir.
	WorkDir string
	// GoProxy, GoSumDB, GoCacheDir and Sandbox are the shared checkout setup (gitws.Options).
	GoProxy, GoSumDB, GoCacheDir string
	Sandbox                      sandbox.Backend
	// Now is the clock a Retry-After is counted from; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	limited  time.Time
	defaults map[string]string // repo -> default branch
}

var (
	_ platform.Platform    = (*Client)(nil)
	_ platform.RateLimiter = (*Client)(nil)
	_ platform.Lister      = (*Client)(nil)
	_ Resolver             = (*Client)(nil)
)

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}

// checkName accepts "namespace/name" built from the characters SCM-Manager allows in
// both, with no dot-only element that could climb a path.
func checkName(full string) error {
	if !repoName.MatchString(full) {
		return fmt.Errorf("refusing unsafe repository name %q", full)
	}
	for _, e := range strings.Split(full, "/") {
		if e == "." || e == ".." {
			return fmt.Errorf("refusing unsafe repository name %q", full)
		}
	}
	return nil
}

func checkRepo(r model.Repo) error { return checkName(r.FullName) }

// RateLimitedUntil is when the last rate-limited answer asked us to retry. SCM-Manager
// documents no rate limit; this exists for a proxy in front of it that answers 429.
func (c *Client) RateLimitedUntil(model.Repo) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited
}

func (c *Client) noteLimit(until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until.After(c.limited) {
		c.limited = until
	}
}

// apiURL is BaseURL/api/v2/<elems>, each element path-escaped; a "namespace/name"
// element is passed as two.
func (c *Client) apiURL(elems ...string) string {
	u := strings.TrimRight(c.BaseURL, "/") + "/api/v2"
	for _, e := range elems {
		u += "/" + url.PathEscape(e)
	}
	return u
}

func split(full string) (ns, name string) {
	ns, name, _ = strings.Cut(full, "/")
	return
}

// webURL is the pull request's page in the UI (source-excerpts.md#pr-web-url).
func (c *Client) webURL(repo string, id int) string {
	ns, name := split(repo)
	return fmt.Sprintf("%s/repo/%s/%s/pull-request/%d/comments/", strings.TrimRight(c.BaseURL, "/"), url.PathEscape(ns), url.PathEscape(name), id)
}

// do sends one request. media is the media type asked for, and sent when in is not nil.
func (c *Client) do(ctx context.Context, method, rawURL, media string, in, out any) (http.Header, error) {
	cred, err := c.Credentials.Credential(ctx, model.Repo{})
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", media)
	req.Header.Set("User-Agent", "ripple")
	req.Header.Set("Authorization", "Bearer "+cred.Secret)
	if in != nil {
		req.Header.Set("Content-Type", media)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("scm-manager %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		until := retryAt(resp, c.now())
		c.noteLimit(until)
		return nil, &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("scm-manager %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("scm-manager %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return resp.Header, nil
}

// retryAt: SCM-Manager documents no rate limit and so no 429 answer. Retry-After is
// honoured when a proxy sends it, and a minute assumed when it does not.
func retryAt(r *http.Response, now time.Time) time.Time {
	if s, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil && s >= 0 {
		return now.Add(time.Duration(s) * time.Second)
	}
	return now.Add(time.Minute)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// page is the HAL envelope of a collection: a zero-based page, the number of pages,
// and the items under _embedded (source-excerpts.md#pr-list).
type page[T any] struct {
	PageTotal int            `json:"pageTotal"`
	Embedded  map[string][]T `json:"_embedded"`
}

// list reads every page of a collection, up to maxPages.
func list[T any](ctx context.Context, c *Client, rawURL, media, key string, query url.Values, maxPages int) ([]T, error) {
	var all []T
	for n := 0; n < maxPages; n++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(n))
		q.Set("pageSize", strconv.Itoa(pageSize))
		var p page[T]
		if _, err := c.do(ctx, http.MethodGet, rawURL+"?"+q.Encode(), media, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Embedded[key]...)
		if n+1 >= p.PageTotal {
			return all, nil
		}
	}
	return nil, fmt.Errorf("more than %d pages of %s", maxPages, rawURL)
}

type prJSON struct {
	ID     json.Number `json:"id"`
	Source string      `json:"source"`
}

func (c *Client) pr(repo string, p prJSON) (platform.PR, bool) {
	id, err := p.ID.Int64()
	if err != nil || id <= 0 {
		return platform.PR{}, false
	}
	return platform.PR{Number: int(id), URL: c.webURL(repo, int(id)), Head: p.Source}, true
}

// ListOpenPRs lists pull requests still in progress, drafts included, whose source
// branch starts with prefix.
func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	ns, name := split(repo.FullName)
	prs, err := list[prJSON](ctx, c, c.apiURL("pull-requests", ns, name), mtPRs, "pullRequests", url.Values{"status": {"IN_PROGRESS"}}, 100)
	if err != nil {
		return nil, err
	}
	var out []platform.PR
	for _, p := range prs {
		if !strings.HasPrefix(p.Source, prefix) {
			continue
		}
		if pr, ok := c.pr(repo.FullName, p); ok {
			out = append(out, pr)
		}
	}
	return out, nil
}

// CreatePR opens the pull request, as a draft when spec.Draft. The Review plugin's
// pull requests have no labels ripple can set (a repository configures which exist),
// so spec.Labels is ignored. The source branch is deleted when the PR is merged.
// A duplicate for the same branches answers 409 and is returned as an error.
func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return platform.PR{}, err
	}
	status := "OPEN"
	if s.Draft {
		status = "DRAFT"
	}
	ns, name := split(repo.FullName)
	payload := map[string]any{"source": s.Head, "target": s.Base, "title": s.Title, "description": s.Body,
		"status": status, "shouldDeleteSourceBranch": true}
	hdr, err := c.do(ctx, http.MethodPost, c.apiURL("pull-requests", ns, name), mtPR, payload, nil)
	if err != nil {
		return platform.PR{}, err
	}
	// 201 has no body; the new pull request's address is the Location header.
	if id, err := strconv.Atoi(path.Base(hdr.Get("Location"))); err == nil && id > 0 {
		return platform.PR{Number: id, URL: c.webURL(repo.FullName, id), Head: s.Head}, nil
	}
	// The pull request exists but its id was not in the answer; find it by its branch
	// rather than report a failure that would have the job try again into a 409.
	prs, err := c.ListOpenPRs(ctx, repo, s.Head)
	if err != nil {
		return platform.PR{}, err
	}
	for _, p := range prs {
		if p.Head == s.Head {
			return p, nil
		}
	}
	return platform.PR{}, errors.New("scm-manager created the pull request but returned no id for it")
}

type repoJSON struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Archived  bool   `json:"archived"`
}

type defaultBranchJSON struct {
	DefaultBranch string `json:"defaultBranch"`
}

// DefaultBranch is the repository's default branch, remembered once learned.
func (c *Client) DefaultBranch(ctx context.Context, repo string) (string, error) {
	if err := checkName(repo); err != nil {
		return "", err
	}
	c.mu.Lock()
	b, ok := c.defaults[repo]
	c.mu.Unlock()
	if ok {
		return b, nil
	}
	ns, name := split(repo)
	var d defaultBranchJSON
	if _, err := c.do(ctx, http.MethodGet, c.apiURL("config", "git", ns, name, "default-branch"), mtDefault, nil, &d); err != nil {
		return "", err
	}
	if d.DefaultBranch == "" {
		return "", fmt.Errorf("repository %s has no default branch", repo)
	}
	c.mu.Lock()
	if c.defaults == nil {
		c.defaults = map[string]string{}
	}
	c.defaults[repo] = d.DefaultBranch
	c.mu.Unlock()
	return d.DefaultBranch, nil
}

// ListRepos is every git repository the key can see that is not archived. The webhook
// plugin sends no event when a repository appears, so discovery is a periodic
// listing. The repository list does not carry the default branch, so it is asked
// once per repository and remembered; a repository without one (an empty repository)
// is left out until it has a branch.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	repos, err := list[repoJSON](ctx, c, c.apiURL("repositories"), mtRepos, "repositories", nil, 1000)
	if err != nil {
		return nil, err
	}
	var out []model.Repo
	for _, r := range repos {
		full := r.Namespace + "/" + r.Name
		if r.Type != "git" || r.Archived || checkName(full) != nil {
			continue
		}
		def, err := c.DefaultBranch(ctx, full)
		if err != nil {
			var rl *platform.RateLimitError
			if errors.As(err, &rl) {
				return nil, err
			}
			c.log().Info("scm-manager repository skipped: no default branch", "repo", full, "err", err)
			continue
		}
		out = append(out, model.Repo{FullName: full, DefaultBranch: def})
	}
	return out, nil
}

type modificationsJSON struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
	Renamed  []struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	} `json:"renamed"`
}

// PushFiles lists the paths the changesets of one push changed. The webhook payload
// carries no file list, so the receiver asks the modifications API, one call per
// changeset. It returns nil (a re-scan, which is always correct) for a push to a
// branch other than the default, which the app ignores anyway, and for a push of
// more changesets than are worth asking about.
func (c *Client) PushFiles(ctx context.Context, repo, branch string, ids []string) ([]string, error) {
	if err := checkName(repo); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !changesetR.MatchString(id) {
			return nil, fmt.Errorf("unexpected changeset id %q", id)
		}
	}
	def, err := c.DefaultBranch(ctx, repo)
	if err != nil {
		return nil, err
	}
	if def != branch || len(ids) > maxChangeset {
		return nil, nil
	}
	ns, name := split(repo)
	seen := map[string]bool{}
	var files []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	for _, id := range ids {
		var m modificationsJSON
		if _, err := c.do(ctx, http.MethodGet, c.apiURL("repositories", ns, name, "modifications", id), mtModifs, nil, &m); err != nil {
			return nil, err
		}
		for _, l := range [][]string{m.Added, m.Modified, m.Removed} {
			for _, p := range l {
				add(p)
			}
		}
		for _, r := range m.Renamed {
			add(r.OldPath)
			add(r.NewPath)
		}
	}
	return files, nil
}

// Checkout shallow-clones the repo's default branch (see gitws.Checkout), the API key
// sent per network git command as the basic-auth password.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	o := gitws.Options{GitBase: c.BaseURL, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	ns, name := split(repo.FullName)
	cloneURL := strings.TrimRight(c.BaseURL, "/") + "/repo/" + url.PathEscape(ns) + "/" + url.PathEscape(name)
	return gitws.Checkout(ctx, o, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		cred, err := c.Credentials.Credential(ctx, repo)
		cred.Username = GitUsername
		return cred, err
	})
}
