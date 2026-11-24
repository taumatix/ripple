// Package bitbucket implements platform.Platform for Bitbucket Cloud (REST 2.0).
// The vendored documentation it was written from is in docs/vendor/bitbucket-cloud;
// docs/platform-bitbucket.md says how to configure it and what is unverified.
package bitbucket

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

// GitUsername is the basic-auth user git sends with an access token or API token as
// the password (docs/vendor/bitbucket-cloud/api-tokens.md, oauth.md).
const GitUsername = "x-token-auth"

var (
	repoName  = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	commitSHA = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// Client is the Bitbucket Cloud platform: one credential, optionally many workspaces.
type Client struct {
	// APIBase is e.g. https://api.bitbucket.org/2.0
	APIBase string
	// GitBase is the clone URL prefix, e.g. https://bitbucket.org; the clone URL is
	// GitBase/workspace/slug.git
	GitBase string
	// Credentials supplies the token. Its Secret is sent as a Bearer token on the API
	// and as the git password under GitUsername.
	Credentials platform.Credentials
	// Workspaces are the workspace slugs ListRepos discovers repositories in.
	Workspaces []string
	HTTP       *http.Client
	Log        *slog.Logger
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
	defaults map[string]string // repo -> default branch, learned from ListRepos
}

var (
	_ platform.Platform    = (*Client)(nil)
	_ platform.AutoMerger  = (*Client)(nil)
	_ platform.RateLimiter = (*Client)(nil)
	_ platform.Lister      = (*Client)(nil)
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

func checkRepo(r model.Repo) error {
	if !repoName.MatchString(r.FullName) || strings.Contains(r.FullName, "/..") || strings.HasPrefix(r.FullName, "..") || strings.HasSuffix(r.FullName, "/.") || strings.HasPrefix(r.FullName, "./") {
		return fmt.Errorf("refusing unsafe repository name %q", r.FullName)
	}
	return nil
}

// RateLimitedUntil is when the last rate-limited answer asked us to retry. Bitbucket's
// limit is per user or token, and ripple holds one credential, so it is one time.
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

func (c *Client) apiURL(repo string, elems ...string) string {
	u := strings.TrimRight(c.APIBase, "/") + "/repositories/" + repo
	for _, e := range elems {
		u += "/" + url.PathEscape(e)
	}
	return u
}

func (c *Client) do(ctx context.Context, method, rawURL string, in, out any) (int, error) {
	cred, err := c.Credentials.Credential(ctx, model.Repo{})
	if err != nil {
		return 0, err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ripple")
	req.Header.Set("Authorization", "Bearer "+cred.Secret)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitbucket %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		until := retryAt(resp, c.now())
		c.noteLimit(until)
		return resp.StatusCode, &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("bitbucket %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("bitbucket %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return resp.StatusCode, nil
}

// retryAt: Atlassian documents the 1,000 requests per hour rolling window but not
// the 429 answer's headers (rate-limits.md), so Retry-After is honoured when present
// and a minute is assumed when it is not.
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

// page is the paginated envelope (rest-intro.md, "Pagination"). Only whether "next"
// is present is used: following the link itself would send the token wherever a
// response pointed, so pages are requested by number.
type page[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

// list reads every page of a collection, up to maxPages.
func list[T any](ctx context.Context, c *Client, rawURL string, query url.Values, maxPages int) ([]T, error) {
	var all []T
	for n := 1; n <= maxPages; n++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(n))
		var p page[T]
		if _, err := c.do(ctx, http.MethodGet, rawURL+"?"+q.Encode(), nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Values...)
		if p.Next == "" {
			return all, nil
		}
	}
	return nil, fmt.Errorf("more than %d pages of %s", maxPages, rawURL)
}

type prJSON struct {
	ID    int `json:"id"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Commit struct {
			Hash string `json:"hash"`
		} `json:"commit"`
	} `json:"source"`
}

func (p prJSON) pr() platform.PR {
	return platform.PR{Number: p.ID, URL: p.Links.HTML.Href, Head: p.Source.Branch.Name, HeadSHA: p.Source.Commit.Hash}
}

func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	prs, err := list[prJSON](ctx, c, c.apiURL(repo.FullName, "pullrequests"), url.Values{"state": {"OPEN"}, "pagelen": {"50"}}, 10)
	if err != nil {
		return nil, err
	}
	var out []platform.PR
	for _, p := range prs {
		if strings.HasPrefix(p.Source.Branch.Name, prefix) {
			out = append(out, p.pr())
		}
	}
	return out, nil
}

func branch(name string) map[string]any {
	return map[string]any{"branch": map[string]any{"name": name}}
}

// CreatePR opens the pull request. Bitbucket Cloud pull requests have no labels, so
// spec.Labels is ignored. The source branch is deleted when the PR is merged.
func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return platform.PR{}, err
	}
	payload := map[string]any{"title": s.Title, "description": s.Body, "source": branch(s.Head),
		"destination": branch(s.Base), "close_source_branch": true}
	if s.Draft {
		payload["draft"] = true
	}
	var p prJSON
	if _, err := c.do(ctx, http.MethodPost, c.apiURL(repo.FullName, "pullrequests"), payload, &p); err != nil {
		return platform.PR{}, err
	}
	pr := p.pr()
	pr.Head = s.Head
	return pr, nil
}

type mergeQueue struct {
	State string `json:"state"`
}

// RequestAutoMerge puts the pull request in its destination branch's merge queue.
// Bitbucket Cloud has no merge-when-green call: the merge endpoint merges at once
// unless the destination branch has an active merge queue, in which case it queues
// the pull request (merge-a-pull-request.md; the API says "the pull request is
// added to the queue instead"). Merging at once would skip the checks, so without a
// queue that accepts entries this returns platform.ErrNoAutoMerge and the pull
// request stays open for a human.
func (c *Client) RequestAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	if err := checkRepo(repo); err != nil {
		return err
	}
	if repo.DefaultBranch == "" || pr.Number <= 0 {
		return errors.New("auto-merge needs the destination branch and the pull request number")
	}
	queues, err := list[mergeQueue](ctx, c, c.apiURL(repo.FullName, "merge-queues"), url.Values{"target_branch": {repo.DefaultBranch}, "pagelen": {"10"}}, 1)
	if err != nil {
		return err
	}
	accepts := false
	for _, q := range queues {
		// ACTIVE accepts and lands entries; PAUSED accepts them but pauses landing.
		// Every other state blocks entry or means no queueing (merge_queue definition).
		accepts = accepts || q.State == "ACTIVE" || q.State == "PAUSED"
	}
	if !accepts {
		return platform.ErrNoAutoMerge
	}
	u := c.apiURL(repo.FullName, "pullrequests", strconv.Itoa(pr.Number), "merge") + "?async=true"
	status, err := c.do(ctx, http.MethodPost, u, map[string]any{"type": "pullrequest_merge_parameters", "merge_strategy": "squash", "close_source_branch": true}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return fmt.Errorf("merge queue answered %d, not 202: the pull request may have been merged without waiting for checks", status)
	}
	return nil
}

type repoJSON struct {
	FullName   string `json:"full_name"`
	SCM        string `json:"scm"`
	Mainbranch *struct {
		Name string `json:"name"`
	} `json:"mainbranch"`
}

// ListRepos is every git repository in the configured workspaces that the token can
// see. Bitbucket Cloud sends no event when a repository appears, so discovery is a
// periodic listing.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	var out []model.Repo
	defaults := map[string]string{}
	for _, ws := range c.Workspaces {
		if strings.Contains(ws, "/") || !repoName.MatchString(ws+"/x") {
			return nil, fmt.Errorf("refusing unsafe workspace %q", ws)
		}
		u := strings.TrimRight(c.APIBase, "/") + "/repositories/" + ws
		repos, err := list[repoJSON](ctx, c, u, url.Values{"pagelen": {"100"}}, 100)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if r.SCM != "git" || checkRepo(model.Repo{FullName: r.FullName}) != nil {
				continue
			}
			repo := model.Repo{FullName: r.FullName}
			if r.Mainbranch != nil {
				repo.DefaultBranch = r.Mainbranch.Name
				defaults[r.FullName] = r.Mainbranch.Name
			}
			out = append(out, repo)
		}
	}
	c.mu.Lock()
	c.defaults = defaults
	c.mu.Unlock()
	return out, nil
}

type commitFile struct {
	Path string `json:"path"`
}

type diffstatJSON struct {
	Old *commitFile `json:"old"`
	New *commitFile `json:"new"`
}

// PushFiles lists the paths the commits of one push changed. A repo:push payload
// carries no file list (event-payloads.md), so the receiver asks the diffstat API,
// one call per commit. It returns nil (a re-scan, which is always correct) for a push
// to a branch other than the default, which the app ignores anyway.
func (c *Client) PushFiles(ctx context.Context, repo, branch string, hashes []string) ([]string, error) {
	if err := checkRepo(model.Repo{FullName: repo}); err != nil {
		return nil, err
	}
	c.mu.Lock()
	def, known := c.defaults[repo]
	c.mu.Unlock()
	if known && def != branch {
		return nil, nil
	}
	seen := map[string]bool{}
	var files []string
	for _, h := range hashes {
		if !commitSHA.MatchString(h) {
			return nil, fmt.Errorf("unexpected commit id %q", h)
		}
		stats, err := list[diffstatJSON](ctx, c, c.apiURL(repo, "diffstat", h), url.Values{"pagelen": {"500"}}, 4)
		if err != nil {
			return nil, err
		}
		for _, s := range stats {
			for _, f := range []*commitFile{s.Old, s.New} {
				if f != nil && f.Path != "" && !seen[f.Path] {
					seen[f.Path] = true
					files = append(files, f.Path)
				}
			}
		}
	}
	return files, nil
}

// Checkout shallow-clones the repo's default branch (see gitws.Checkout), the token
// sent per network git command as x-token-auth basic auth.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	o := gitws.Options{GitBase: c.GitBase, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	cloneURL := strings.TrimRight(c.GitBase, "/") + "/" + repo.FullName + ".git"
	return gitws.Checkout(ctx, o, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		cred, err := c.Credentials.Credential(ctx, repo)
		cred.Username = GitUsername
		return cred, err
	})
}
