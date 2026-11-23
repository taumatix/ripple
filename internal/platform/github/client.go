// Package github implements platform.Platform for GitHub (github.com and the REST API).
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

// APIVersion is sent as X-GitHub-Api-Version (pinned in UPSTREAM.md).
const APIVersion = "2026-03-10"

var repoName = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

type Client struct {
	// APIBase is e.g. https://api.github.com
	APIBase string
	// GitBase is the clone URL prefix, e.g. https://github.com; the clone URL is GitBase/owner/repo.git
	GitBase string
	// GraphQLURL overrides the GraphQL endpoint; empty derives it from APIBase
	// (GraphQLURL func): api.github.com/graphql, or HOST/api/graphql for a GHES
	// base of HOST/api/v3.
	GraphQLURL string
	// Versions is the X-GitHub-Api-Version policy; nil sends APIVersion and falls
	// back to FallbackAPIVersion if the server answers 410. Share it with AppAuth.
	Versions *Versions
	// Tokens mints installation tokens (AppAuth) or returns a fixed one (StaticToken).
	Tokens TokenSource
	// Credentials, when set, replaces Tokens as the credential source for API calls
	// and git; nil means AppCredentials{Tokens}.
	Credentials platform.Credentials
	HTTP        *http.Client
	Log         *slog.Logger
	// AuthorName and AuthorEmail identify commits.
	AuthorName, AuthorEmail string
	// WorkDir is where checkouts are created; empty means the OS temp dir.
	WorkDir string
	// GoProxy is the only module source the go command may use (GOPROXY); empty
	// means https://proxy.golang.org. GoSumDB is GOSUMDB, empty means the default.
	GoProxy, GoSumDB string
	// GoCacheDir shares the build cache between checkouts; empty means a private
	// cache per checkout, which is slower but isolates repositories from each other.
	GoCacheDir string
	// Sandbox confines the go command and local git commands run on an untrusted
	// checkout. Nil means no confinement. Network git commands (clone, fetch, push)
	// run unconfined: they hold the installation token and execute no repository code.
	Sandbox sandbox.Backend
	// Now is the clock a Retry-After is counted from; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	limited map[int64]time.Time
}

var (
	_ platform.Platform     = (*Client)(nil)
	_ platform.AutoMerger   = (*Client)(nil)
	_ platform.PRCloser     = (*Client)(nil)
	_ platform.RateLimiter  = (*Client)(nil)
	_ platform.LegacyConfig = (*Client)(nil)
)

func (c *Client) credentials() platform.Credentials {
	if c.Credentials != nil {
		return c.Credentials
	}
	return AppCredentials{Tokens: c.Tokens}
}

// ReadsLegacyConfig is true: .github/ripple.yml is a deprecated alias on GitHub only.
func (c *Client) ReadsLegacyConfig(model.Repo) bool { return true }

// RateLimitedUntil is when the last rate-limited answer for repo's installation
// asked us to retry; GitHub's limits are per installation.
func (c *Client) RateLimitedUntil(repo model.Repo) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited[repo.InstallationID]
}

func (c *Client) noteLimit(inst int64, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limited == nil {
		c.limited = map[int64]time.Time{}
	}
	if until.After(c.limited[inst]) {
		c.limited[inst] = until
	}
}

func setHeaders(r *http.Request, version string) {
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", version)
	r.Header.Set("User-Agent", "ripple")
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func checkRepo(r model.Repo) error {
	if !repoName.MatchString(r.FullName) || strings.Contains(r.FullName, "/..") || strings.HasPrefix(r.FullName, "..") || strings.HasSuffix(r.FullName, "/.") || strings.HasPrefix(r.FullName, "./") {
		return fmt.Errorf("refusing unsafe repository name %q", r.FullName)
	}
	return nil
}

func (c *Client) do(ctx context.Context, repo model.Repo, method, rawURL string, in, out any) (int, error) {
	cred, err := c.credentials().Credential(ctx, repo)
	if err != nil {
		return 0, err
	}
	var payload []byte
	if in != nil {
		payload, _ = json.Marshal(in)
	}
	var (
		req  *http.Request
		resp *http.Response
	)
	for {
		var body io.Reader
		if in != nil {
			body = bytes.NewReader(payload)
		}
		req, err = http.NewRequestWithContext(ctx, method, rawURL, body)
		if err != nil {
			return 0, err
		}
		version := c.Versions.Current()
		setHeaders(req, version)
		req.Header.Set("Authorization", "Bearer "+cred.Secret)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err = c.client().Do(req)
		if err != nil {
			return 0, fmt.Errorf("github %s %s: %w", method, req.URL.Path, err)
		}
		if resp.StatusCode == http.StatusGone && c.Versions.Lower(version) {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if c.Log != nil {
				c.Log.Warn("github host does not support the API version; falling back", "host", req.URL.Host, "from", version, "to", c.Versions.Current())
			}
			continue
		}
		break
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")) {
		until := retryAt(resp, c.now())
		c.noteLimit(repo.InstallationID, until)
		return resp.StatusCode, &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("github %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("github %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func retryAt(r *http.Response, now time.Time) time.Time {
	if s, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil {
		return now.Add(time.Duration(s) * time.Second)
	}
	if s, err := strconv.ParseInt(r.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		return time.Unix(s, 0)
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

type prJSON struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	NodeID  string `json:"node_id"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

func (p prJSON) pr() platform.PR {
	return platform.PR{Number: p.Number, URL: p.HTMLURL, ID: p.NodeID, Head: p.Head.Ref}
}

func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	var out []platform.PR
	for page := 1; page <= 10; page++ {
		var prs []prJSON
		u := fmt.Sprintf("%s/repos/%s/pulls?state=open&per_page=100&page=%d", strings.TrimRight(c.APIBase, "/"), repo.FullName, page)
		if _, err := c.do(ctx, repo, http.MethodGet, u, nil, &prs); err != nil {
			return nil, err
		}
		for _, p := range prs {
			if strings.HasPrefix(p.Head.Ref, prefix) {
				out = append(out, p.pr())
			}
		}
		if len(prs) < 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return platform.PR{}, err
	}
	var p prJSON
	u := fmt.Sprintf("%s/repos/%s/pulls", strings.TrimRight(c.APIBase, "/"), repo.FullName)
	payload := map[string]any{"title": s.Title, "head": s.Head, "base": s.Base, "body": s.Body}
	if s.Draft {
		payload["draft"] = true
	}
	_, err := c.do(ctx, repo, http.MethodPost, u, payload, &p)
	if err != nil {
		return platform.PR{}, err
	}
	pr := p.pr()
	pr.Head = s.Head
	if len(s.Labels) > 0 {
		lu := fmt.Sprintf("%s/repos/%s/issues/%d/labels", strings.TrimRight(c.APIBase, "/"), repo.FullName, pr.Number)
		if _, err := c.do(ctx, repo, http.MethodPost, lu, map[string]any{"labels": s.Labels}, nil); err != nil && c.Log != nil {
			c.Log.Warn("labelling pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
		}
	}
	return pr, nil
}

// maxEventPages bounds the issue-events scan; a PR with more events than that is
// busy enough that it is treated as touched.
const maxEventPages = 5

// CloseUntouchedPR comments on pr and closes it unless a person touched it. Touched
// means: the head branch is not in this repository; the PR has other than exactly
// one commit, or that commit's author or committer is not ripple's identity (a
// person pushed to the branch); it has any review, review comment or issue comment;
// or its issue events hold a rename or a force-push of the head branch (a rewrite
// that keeps ripple's commit identity); or its description was ever edited (GraphQL
// userContentEdits; ripple never edits one). Every read must succeed and nothing
// is closed on a failed one. Not detected: a changed label or assignee. The PR is
// closed first and the comment is best effort, so a failed close leaves nothing
// to repeat on the retry.
func (c *Client) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr platform.PR, comment string) (bool, error) {
	if err := checkRepo(repo); err != nil {
		return false, err
	}
	base := fmt.Sprintf("%s/repos/%s", strings.TrimRight(c.APIBase, "/"), repo.FullName)
	get := func(path string, out any) error {
		_, err := c.do(ctx, repo, http.MethodGet, base+path, nil, out)
		return err
	}
	var cur struct {
		State  string `json:"state"`
		NodeID string `json:"node_id"`
		Head   struct {
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}
	if err := get(fmt.Sprintf("/pulls/%d", pr.Number), &cur); err != nil {
		return false, err
	}
	if cur.State != "open" || cur.Head.Ref != pr.Head || !strings.EqualFold(cur.Head.Repo.FullName, repo.FullName) {
		return false, nil
	}
	var commits []struct {
		Commit struct {
			Author    struct{ Email string } `json:"author"`
			Committer struct{ Email string } `json:"committer"`
		} `json:"commit"`
	}
	if err := get(fmt.Sprintf("/pulls/%d/commits?per_page=2", pr.Number), &commits); err != nil {
		return false, err
	}
	ours := gitws.AuthorEmail(gitws.Options{AuthorEmail: c.AuthorEmail})
	if len(commits) != 1 || commits[0].Commit.Author.Email != ours || commits[0].Commit.Committer.Email != ours {
		return false, nil
	}
	for _, kind := range []string{"/pulls/%d/reviews", "/pulls/%d/comments", "/issues/%d/comments"} {
		var found []json.RawMessage
		if err := get(fmt.Sprintf(kind+"?per_page=1", pr.Number), &found); err != nil {
			return false, err
		}
		if len(found) > 0 {
			return false, nil
		}
	}
	for page := 1; ; page++ {
		if page > maxEventPages {
			return false, nil
		}
		var events []struct {
			Event string `json:"event"`
		}
		if err := get(fmt.Sprintf("/issues/%d/events?per_page=100&page=%d", pr.Number, page), &events); err != nil {
			return false, err
		}
		for _, e := range events {
			if e.Event == "renamed" || e.Event == "head_ref_force_pushed" {
				return false, nil
			}
		}
		if len(events) < 100 {
			break
		}
	}
	if cur.NodeID == "" {
		return false, errors.New("pull request has no node id to read its description edits")
	}
	var edits struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Node struct {
				UserContentEdits struct {
					TotalCount int `json:"totalCount"`
				} `json:"userContentEdits"`
			} `json:"node"`
		} `json:"data"`
	}
	if _, err := c.do(ctx, repo, http.MethodPost, c.graphQLURL(), map[string]any{"query": editsQuery, "variables": map[string]string{"id": cur.NodeID}}, &edits); err != nil {
		return false, err
	}
	if len(edits.Errors) > 0 {
		return false, fmt.Errorf("read description edits: %s", edits.Errors[0].Message)
	}
	if edits.Data.Node.UserContentEdits.TotalCount > 0 {
		return false, nil
	}
	if _, err := c.do(ctx, repo, http.MethodPatch, fmt.Sprintf("%s/pulls/%d", base, pr.Number), map[string]string{"state": "closed"}, nil); err != nil {
		return false, err
	}
	if _, err := c.do(ctx, repo, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", base, pr.Number), map[string]string{"body": comment}, nil); err != nil && c.Log != nil {
		c.Log.Warn("commenting on the closed superseded pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
	}
	return true, nil
}

const editsQuery = `query($id: ID!) { node(id: $id) { ... on PullRequest { userContentEdits(first: 1) { totalCount } } } }`

const automergeMutation = `mutation($id: ID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH}) { pullRequest { number } } }`

// RequestAutoMerge enables auto-merge (squash) through GraphQL: REST has no
// auto-merge endpoint. It fails when the repository does not allow auto-merge or
// has no required checks.
func (c *Client) RequestAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	if pr.ID == "" {
		return errors.New("pull request has no node id")
	}
	var out struct {
		Errors []struct {
			Message string
			Type    string
		} `json:"errors"`
	}
	status, err := c.do(ctx, repo, http.MethodPost, c.graphQLURL(), map[string]any{"query": automergeMutation, "variables": map[string]string{"id": pr.ID}}, &out)
	if err != nil {
		// A host without GraphQL (disabled, or a path ripple was not told about)
		// answers 404: there is nothing to ask, which is not a failure.
		if status == http.StatusNotFound || status == http.StatusGone {
			return fmt.Errorf("%w: GraphQL endpoint %s answered %d", platform.ErrNoAutoMerge, c.graphQLURL(), status)
		}
		return err
	}
	if len(out.Errors) > 0 {
		m := out.Errors[0]
		// An older GHES schema validates the mutation away instead of failing.
		if m.Type == "undefinedField" || strings.Contains(m.Message, "enablePullRequestAutoMerge") && strings.Contains(m.Message, "doesn't exist") {
			return fmt.Errorf("%w: %s", platform.ErrNoAutoMerge, m.Message)
		}
		return fmt.Errorf("enable auto-merge: %s", m.Message)
	}
	return nil
}

// Checkout shallow-clones the repo's default branch (see gitws.Checkout) with an
// installation token fetched per network git command.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	o := gitws.Options{GitBase: c.GitBase, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	cloneURL := strings.TrimRight(c.GitBase, "/") + "/" + repo.FullName + ".git"
	return gitws.Checkout(ctx, o, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		return c.credentials().Credential(ctx, repo)
	})
}
