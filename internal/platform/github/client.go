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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
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
	Tokens  TokenSource
	HTTP    *http.Client
	Log     *slog.Logger
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
}

var _ platform.Platform = (*Client)(nil)

func setHeaders(r *http.Request) {
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", APIVersion)
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

func (c *Client) do(ctx context.Context, inst int64, method, rawURL string, in, out any) (int, error) {
	tok, err := c.Tokens.Token(ctx, inst)
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
	setHeaders(req)
	req.Header.Set("Authorization", "Bearer "+tok)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("github %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")) {
		return resp.StatusCode, &platform.RateLimitError{Until: retryAt(resp)}
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

func retryAt(r *http.Response) time.Time {
	if s, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil {
		return time.Now().Add(time.Duration(s) * time.Second)
	}
	if s, err := strconv.ParseInt(r.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		return time.Unix(s, 0)
	}
	return time.Now().Add(time.Minute)
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
	return platform.PR{Number: p.Number, URL: p.HTMLURL, NodeID: p.NodeID, Head: p.Head.Ref}
}

func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	var out []platform.PR
	for page := 1; page <= 10; page++ {
		var prs []prJSON
		u := fmt.Sprintf("%s/repos/%s/pulls?state=open&per_page=100&page=%d", strings.TrimRight(c.APIBase, "/"), repo.FullName, page)
		if _, err := c.do(ctx, repo.InstallationID, http.MethodGet, u, nil, &prs); err != nil {
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
	_, err := c.do(ctx, repo.InstallationID, http.MethodPost, u, payload, &p)
	if err != nil {
		return platform.PR{}, err
	}
	pr := p.pr()
	pr.Head = s.Head
	if len(s.Labels) > 0 {
		lu := fmt.Sprintf("%s/repos/%s/issues/%d/labels", strings.TrimRight(c.APIBase, "/"), repo.FullName, pr.Number)
		if _, err := c.do(ctx, repo.InstallationID, http.MethodPost, lu, map[string]any{"labels": s.Labels}, nil); err != nil && c.Log != nil {
			c.Log.Warn("labelling pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
		}
	}
	return pr, nil
}

const automergeMutation = `mutation($id: ID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH}) { pullRequest { number } } }`

// EnableAutoMerge uses GraphQL (REST has no auto-merge endpoint). It fails when
// the repository does not allow auto-merge or has no required checks.
func (c *Client) EnableAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	if pr.NodeID == "" {
		return errors.New("pull request has no node id")
	}
	var out struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	u, err := url.JoinPath(c.APIBase, "graphql")
	if err != nil {
		return err
	}
	if _, err := c.do(ctx, repo.InstallationID, http.MethodPost, u, map[string]any{"query": automergeMutation, "variables": map[string]string{"id": pr.NodeID}}, &out); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		return fmt.Errorf("enable auto-merge: %s", out.Errors[0].Message)
	}
	return nil
}
