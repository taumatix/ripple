// Package bitbucketserver implements platform.Platform for Bitbucket Server, now
// Bitbucket Data Center: the self-hosted product with REST under <instance>/rest/api/1.0.
// It is not Bitbucket Cloud, which has a different API and different webhooks.
// Written from the documentation vendored in docs/vendor/bitbucket-server;
// docs/platform-bitbucketserver.md lists what no real instance has confirmed.
package bitbucketserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

const (
	pageSize = 100
	maxPages = 200
	branches = "refs/heads/"
)

// A project key is upper-case letters, digits and underscores, or ~USER for a
// personal project; a slug is lower-case letters, digits, dots, hyphens and underscores.
// Both are accepted here more loosely, but never anything that could climb a path.
var (
	keyRe  = regexp.MustCompile(`^~?[A-Za-z0-9][A-Za-z0-9._-]*$`)
	slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type Client struct {
	// APIBase is the instance URL including any context path, e.g.
	// https://git.example.org/bitbucket; /rest/api/1.0 is added.
	APIBase string
	// GitBase is the clone URL prefix, usually APIBase; clones are GitBase/scm/KEY/slug.git.
	GitBase string
	// Credentials supplies the HTTP access token (Secret) and the user name sent with it.
	Credentials platform.Credentials
	HTTP        *http.Client
	Log         *slog.Logger

	AuthorName, AuthorEmail string
	WorkDir                 string
	GoProxy, GoSumDB        string
	GoCacheDir              string
	Sandbox                 sandbox.Backend
	// Now is the clock a Retry-After is counted from; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	limited time.Time
}

var (
	_ platform.Platform    = (*Client)(nil)
	_ platform.AutoMerger  = (*Client)(nil)
	_ platform.RateLimiter = (*Client)(nil)
	_ platform.Lister      = (*Client)(nil)
)

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) api(path string) string {
	return strings.TrimRight(c.APIBase, "/") + "/rest/api/1.0" + path
}

// RateLimitedUntil is when the last rate-limited answer asked us to retry. The
// limit is per user, and one token serves the instance, so it is not per repository.
func (c *Client) RateLimitedUntil(model.Repo) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited
}

// split validates "KEY/slug".
func split(r model.Repo) (key, slug string, err error) {
	k, s, ok := strings.Cut(r.FullName, "/")
	if !ok || !keyRe.MatchString(k) || !slugRe.MatchString(s) || strings.HasSuffix(s, ".") {
		return "", "", fmt.Errorf("refusing unsafe repository name %q", r.FullName)
	}
	return k, s, nil
}

func repoPath(key, slug string) string {
	return "/projects/" + url.PathEscape(key) + "/repos/" + url.PathEscape(slug)
}

func (c *Client) do(ctx context.Context, repo model.Repo, method, rawURL string, in, out any) error {
	cred, err := c.Credentials.Credential(ctx, repo)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ripple")
	// Basic user:token; for a project or repository token the user is x-token-auth
	// (docs/vendor/bitbucket-server/http-access-tokens.md).
	req.SetBasicAuth(cred.Username, cred.Secret)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("bitbucket server %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		until := retryAt(resp.Header, c.now())
		c.mu.Lock()
		if until.After(c.limited) {
			c.limited = until
		}
		c.mu.Unlock()
		return &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return &statusError{method: method, path: req.URL.Path, status: resp.StatusCode, msg: errorMessage(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("bitbucket server %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return nil
}

type statusError struct {
	method, path, msg string
	status            int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("bitbucket server %s %s: status %d: %s", e.method, e.path, e.status, e.msg)
}

// errorMessage reads {"errors":[{"message":...}]}, else a snippet of the body.
func errorMessage(b []byte) string {
	var e struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(b, &e) == nil && len(e.Errors) > 0 && e.Errors[0].Message != "" {
		return e.Errors[0].Message
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// retryAt reads Retry-After in seconds. The rate-limiting page names only the
// status, so the header is assumed (docs/platform-bitbucketserver.md); without
// it the wait is a minute.
func retryAt(h http.Header, now time.Time) time.Time {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s >= 0 {
		return now.Add(time.Duration(s) * time.Second)
	}
	return now.Add(time.Minute)
}

// paged calls fn with each page of a listing, following nextPageStart until isLastPage.
func (c *Client) paged(ctx context.Context, repo model.Repo, path string, fn func(values json.RawMessage) error) error {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	start := 0
	for i := 0; i < maxPages; i++ {
		var page struct {
			Values        json.RawMessage `json:"values"`
			IsLastPage    bool            `json:"isLastPage"`
			NextPageStart int             `json:"nextPageStart"`
		}
		u := c.api(path) + sep + "limit=" + strconv.Itoa(pageSize) + "&start=" + strconv.Itoa(start)
		if err := c.do(ctx, repo, http.MethodGet, u, nil, &page); err != nil {
			return err
		}
		if err := fn(page.Values); err != nil {
			return err
		}
		if page.IsLastPage || page.NextPageStart <= start {
			return nil
		}
		start = page.NextPageStart
	}
	return nil
}

type refJSON struct {
	ID           string `json:"id"`
	DisplayID    string `json:"displayId"`
	LatestCommit string `json:"latestCommit"`
	Repository   struct {
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"repository"`
}

type prJSON struct {
	ID      int     `json:"id"`
	Version int     `json:"version"`
	FromRef refJSON `json:"fromRef"`
	Links   struct {
		Self []struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
}

func (c *Client) pr(key, slug string, p prJSON) platform.PR {
	u := ""
	if len(p.Links.Self) > 0 {
		u = p.Links.Self[0].Href
	}
	if u == "" {
		u = fmt.Sprintf("%s/projects/%s/repos/%s/pull-requests/%d", strings.TrimRight(c.APIBase, "/"), key, slug, p.ID)
	}
	return platform.PR{Number: p.ID, URL: u, Head: strings.TrimPrefix(p.FromRef.ID, branches), HeadSHA: p.FromRef.LatestCommit}
}

func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	key, slug, err := split(repo)
	if err != nil {
		return nil, err
	}
	var out []platform.PR
	err = c.paged(ctx, repo, repoPath(key, slug)+"/pull-requests?state=OPEN", func(raw json.RawMessage) error {
		var prs []prJSON
		if err := json.Unmarshal(raw, &prs); err != nil {
			return err
		}
		for _, p := range prs {
			// Only branches of this repository: a fork's branch of the same name is not ours.
			if !strings.EqualFold(p.FromRef.Repository.Slug, slug) || !strings.EqualFold(p.FromRef.Repository.Project.Key, key) {
				continue
			}
			if strings.HasPrefix(strings.TrimPrefix(p.FromRef.ID, branches), prefix) {
				out = append(out, c.pr(key, slug, p))
			}
		}
		return nil
	})
	return out, err
}

func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	key, slug, err := split(repo)
	if err != nil {
		return platform.PR{}, err
	}
	ref := func(branch string) map[string]any {
		return map[string]any{"id": branches + branch, "repository": map[string]any{
			"slug": slug, "project": map[string]any{"key": key}}}
	}
	body := map[string]any{"title": s.Title, "description": s.Body, "fromRef": ref(s.Head), "toRef": ref(s.Base)}
	if s.Draft {
		body["draft"] = true
	}
	var p prJSON
	if err := c.do(ctx, repo, http.MethodPost, c.api(repoPath(key, slug)+"/pull-requests"), body, &p); err != nil {
		return platform.PR{}, err
	}
	pr := c.pr(key, slug, p)
	pr.Head = s.Head
	// Bitbucket Data Center has no pull request labels, so s.Labels is not applied.
	return pr, nil
}

// RequestAutoMerge asks for the PR to merge once its merge checks pass. The merge
// call needs the PR's current version, so it is read first; a 409 means the PR
// changed in between and is returned for the next pass to retry. A 403 means the
// repository's auto-merge setting is off (docs/vendor/bitbucket-server/openapi-excerpt.json).
func (c *Client) RequestAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	key, slug, err := split(repo)
	if err != nil {
		return err
	}
	base := repoPath(key, slug) + "/pull-requests/" + strconv.Itoa(pr.Number)
	var cur prJSON
	if err := c.do(ctx, repo, http.MethodGet, c.api(base), nil, &cur); err != nil {
		return err
	}
	return c.do(ctx, repo, http.MethodPost, c.api(base+"/merge?version="+strconv.Itoa(cur.Version)),
		map[string]any{"autoMerge": true}, nil)
}

// ListRepos is every repository the token can write to, not archived. The listing
// carries no default branch (the field is write-only in the schema), so it is left
// empty and filled from the first checkout.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	return c.listRepos(ctx, false)
}

// ListArchivedRepos is the archived ones (`archived=ARCHIVED`, available since 8.0). A
// server before 8.0 ignores the filter and has no `archived` field, so the field is
// required to be true here and such a server lists none.
func (c *Client) ListArchivedRepos(ctx context.Context) ([]model.Repo, error) {
	return c.listRepos(ctx, true)
}

func (c *Client) listRepos(ctx context.Context, archived bool) ([]model.Repo, error) {
	var out []model.Repo
	path := "/repos?permission=REPO_WRITE"
	if archived {
		path += "&archived=ARCHIVED"
	}
	err := c.paged(ctx, model.Repo{}, path, func(raw json.RawMessage) error {
		var rs []struct {
			Slug     string `json:"slug"`
			Archived bool   `json:"archived"`
			State    string `json:"state"`
			Project  struct {
				Key string `json:"key"`
			} `json:"project"`
		}
		if err := json.Unmarshal(raw, &rs); err != nil {
			return err
		}
		for _, r := range rs {
			if r.Archived != archived || (r.State != "" && r.State != "AVAILABLE") || !keyRe.MatchString(r.Project.Key) || !slugRe.MatchString(r.Slug) {
				continue
			}
			out = append(out, model.Repo{FullName: r.Project.Key + "/" + r.Slug})
		}
		return nil
	})
	return out, err
}

// DefaultBranch is the repository's default branch name.
func (c *Client) DefaultBranch(ctx context.Context, repo model.Repo) (string, error) {
	key, slug, err := split(repo)
	if err != nil {
		return "", err
	}
	var b struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, repo, http.MethodGet, c.api(repoPath(key, slug)+"/default-branch"), nil, &b); err != nil {
		return "", err
	}
	name := strings.TrimPrefix(b.ID, branches)
	if name == "" || name == b.ID {
		return "", fmt.Errorf("bitbucket server: unexpected default branch id %q", b.ID)
	}
	return name, nil
}

// ChangedFiles lists the files that differ between two commits of a repository.
// compare/changes reports what `to` has that `from` lacks, so a push of
// fromHash..toHash asks from=toHash, to=fromHash.
func (c *Client) ChangedFiles(ctx context.Context, repo model.Repo, fromHash, toHash string) ([]string, error) {
	key, slug, err := split(repo)
	if err != nil {
		return nil, err
	}
	var files []string
	seen := map[string]bool{}
	add := func(p *struct {
		ToString   string   `json:"toString"`
		Components []string `json:"components"`
	}) {
		if p == nil {
			return
		}
		f := p.ToString
		if f == "" {
			f = strings.Join(p.Components, "/")
		}
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	q := "/compare/changes?from=" + url.QueryEscape(toHash) + "&to=" + url.QueryEscape(fromHash)
	err = c.paged(ctx, repo, repoPath(key, slug)+q, func(raw json.RawMessage) error {
		var cs []struct {
			Path *struct {
				ToString   string   `json:"toString"`
				Components []string `json:"components"`
			} `json:"path"`
			SrcPath *struct {
				ToString   string   `json:"toString"`
				Components []string `json:"components"`
			} `json:"srcPath"`
		}
		if err := json.Unmarshal(raw, &cs); err != nil {
			return err
		}
		for _, ch := range cs {
			add(ch.Path)
			add(ch.SrcPath)
		}
		return nil
	})
	return files, err
}

// Checkout shallow-clones the default branch over HTTP with the token as the basic-auth password.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	key, slug, err := split(repo)
	if err != nil {
		return nil, err
	}
	g := gitws.Options{GitBase: c.GitBase, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	cloneURL := strings.TrimRight(c.GitBase, "/") + "/scm/" + url.PathEscape(key) + "/" + url.PathEscape(slug) + ".git"
	return gitws.Checkout(ctx, g, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		return c.Credentials.Credential(ctx, repo)
	})
}
