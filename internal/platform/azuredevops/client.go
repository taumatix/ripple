// Package azuredevops implements platform.Platform for Azure DevOps Services
// (dev.azure.com): REST api-version 7.1, git over HTTPS with a personal access
// token, and service-hook webhooks. A repository is identified as
// "organization/project/repository".
//
// Everything here is written from the Microsoft documentation vendored in
// docs/vendor/azuredevops and exercised against a fake (internal/testutil); it has
// not been run against a real Azure DevOps organisation. Anything not stated in
// the vendored text is marked "assumed, unverified". Azure DevOps Server (on
// premises) is a different product and is not covered.
package azuredevops

import (
	"bytes"
	"context"
	"encoding/base64"
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

// APIVersion is the api-version query parameter on every REST call (UPSTREAM.md).
const APIVersion = "7.1"

// maxDescription is the longest pull request description sent; the service limit
// is not in the vendored docs, so this is a conservative assumed, unverified, bound
// (the web UI documents 4000 characters).
const maxDescription = 4000

// segment is one of organization, project, repository. Azure DevOps allows spaces
// in project and repository names; they are refused here (a residual in ROADMAP.md)
// so every segment is safe in a URL path and a git clone URL without escaping.
var segment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// Client is the Azure DevOps platform.Platform. It also implements AutoMerger
// (auto-complete), RateLimiter and Lister.
type Client struct {
	// APIBase is https://dev.azure.com; the REST URL is APIBase/org/project/_apis/....
	APIBase string
	// GitBase is the clone URL prefix, https://dev.azure.com; the clone URL is
	// GitBase/org/project/_git/repo.
	GitBase string
	// Org is the organization ListRepos enumerates. Repos named elsewhere carry their
	// own organization as the first path segment.
	Org string
	// Credentials is a personal access token (platform.StaticCredential{Secret: pat}):
	// REST sends it as Basic ":<pat>", git as Basic with an empty user name.
	Credentials platform.Credentials
	HTTP        *http.Client
	Log         *slog.Logger
	// AuthorName and AuthorEmail identify commits.
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

// RateLimitedUntil is when the last Retry-After asked us to wait. Azure DevOps
// limits per user across the organization (rate-limits.md), so one deadline covers
// every repo.
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

// split validates a repository's full name and returns its three segments.
func split(full string) (org, project, repo string, err error) {
	parts := strings.Split(full, "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("azure devops repository %q is not organization/project/repository", full)
	}
	for _, p := range parts {
		if !segment.MatchString(p) || p == "." || p == ".." || strings.HasSuffix(p, ".") {
			return "", "", "", fmt.Errorf("refusing unsafe azure devops repository name %q", full)
		}
	}
	return parts[0], parts[1], parts[2], nil
}

func (c *Client) base() string { return strings.TrimRight(c.APIBase, "/") }

// do sends one REST call. in is JSON-encoded when non-nil; out is decoded when non-nil.
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
	// use-personal-access-tokens.md: a PAT is the password of HTTP Basic, user name empty.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+cred.Secret)))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("azure devops %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	// rate-limits.md: "If you receive [Retry-After] in a response, wait the specified
	// time before sending another request. The response still returns HTTP 200."
	until, hasRetry := retryAt(resp, c.now())
	if hasRetry {
		c.noteLimit(until)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if !hasRetry {
			until = c.now().Add(time.Minute)
			c.noteLimit(until)
		}
		return &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("azure devops %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("azure devops %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return nil
}

func retryAt(r *http.Response, now time.Time) (time.Time, bool) {
	if s, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil && s >= 0 {
		return now.Add(time.Duration(s) * time.Second), true
	}
	return time.Time{}, false
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

type identity struct {
	ID string `json:"id"`
}

type prJSON struct {
	ID            int      `json:"pullRequestId"`
	SourceRefName string   `json:"sourceRefName"`
	CreatedBy     identity `json:"createdBy"`
	// LastMergeSourceCommit is the source branch's commit as of the last merge
	// calculation (rest-pull-requests-get-list.md); absent until the service has one.
	LastMergeSourceCommit struct {
		CommitID string `json:"commitId"`
	} `json:"lastMergeSourceCommit"`
}

func (c *Client) webURL(org, project, repo string, id int) string {
	return fmt.Sprintf("%s/%s/%s/_git/%s/pullrequest/%d", c.base(), org, project, repo, id)
}

func (p prJSON) pr(webURL string) platform.PR {
	return platform.PR{Number: p.ID, URL: webURL, ID: p.CreatedBy.ID, Head: strings.TrimPrefix(p.SourceRefName, "refs/heads/"), HeadSHA: p.LastMergeSourceCommit.CommitID}
}

func (c *Client) prsURL(org, project, repo string) string {
	return fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s/pullrequests", c.base(), org, project, repo)
}

// ListOpenPRs lists active pull requests (rest-pull-requests-get-list.md,
// searchCriteria.status=active, paged with $top/$skip) whose source branch starts
// with prefix.
func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	org, project, name, err := split(repo.FullName)
	if err != nil {
		return nil, err
	}
	const top = 100
	var out []platform.PR
	for skip := 0; skip < 10*top; skip += top {
		var res struct {
			Value []prJSON `json:"value"`
		}
		u := fmt.Sprintf("%s?searchCriteria.status=active&$top=%d&$skip=%d&api-version=%s", c.prsURL(org, project, name), top, skip, APIVersion)
		if err := c.do(ctx, repo, http.MethodGet, u, nil, &res); err != nil {
			return nil, err
		}
		for _, p := range res.Value {
			if strings.HasPrefix(strings.TrimPrefix(p.SourceRefName, "refs/heads/"), prefix) {
				out = append(out, p.pr(c.webURL(org, project, name, p.ID)))
			}
		}
		if len(res.Value) < top {
			break
		}
	}
	return out, nil
}

// CreatePR opens a pull request (rest-pull-requests-create.md). The repository is
// addressed by name in the repositoryId path segment: the docs name an id, and the
// service accepting a name there is assumed, unverified. Labels are added one by
// one (rest-labels-create.md) and are best effort.
func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	org, project, name, err := split(repo.FullName)
	if err != nil {
		return platform.PR{}, err
	}
	desc := s.Body
	if len(desc) > maxDescription {
		desc = desc[:maxDescription-3] + "..."
	}
	payload := map[string]any{
		"sourceRefName": "refs/heads/" + s.Head,
		"targetRefName": "refs/heads/" + s.Base,
		"title":         s.Title,
		"description":   desc,
	}
	if s.Draft {
		payload["isDraft"] = true
	}
	var p prJSON
	if err := c.do(ctx, repo, http.MethodPost, c.prsURL(org, project, name)+"?api-version="+APIVersion, payload, &p); err != nil {
		return platform.PR{}, err
	}
	pr := p.pr(c.webURL(org, project, name, p.ID))
	pr.Head = s.Head
	for _, l := range s.Labels {
		u := fmt.Sprintf("%s/%d/labels?api-version=%s", c.prsURL(org, project, name), p.ID, APIVersion)
		if err := c.do(ctx, repo, http.MethodPost, u, map[string]string{"name": l}, nil); err != nil {
			if c.Log != nil {
				c.Log.Warn("labelling pull request failed", "repo", repo.FullName, "pr", p.ID, "err", err)
			}
			var rl *platform.RateLimitError
			if errors.As(err, &rl) {
				break // the rest would be refused too
			}
		}
	}
	return pr, nil
}

// RequestAutoMerge sets auto-complete with a squash merge: PATCH the pull request
// with autoCompleteSetBy and completionOptions (rest-pull-requests-update.md).
// autoCompleteSetBy must be the user who sets it; taking that to be the pull
// request's createdBy.id, which is the authenticated user because ripple created
// it, is assumed, unverified. Whether the pull request then waits for policies or
// completes at once is the repository's branch policy.
func (c *Client) RequestAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	org, project, name, err := split(repo.FullName)
	if err != nil {
		return err
	}
	if pr.ID == "" {
		return fmt.Errorf("pull request %d has no creator identity", pr.Number)
	}
	body := map[string]any{
		"autoCompleteSetBy": map[string]string{"id": pr.ID},
		"completionOptions": map[string]any{"mergeStrategy": "squash", "deleteSourceBranch": true},
	}
	u := fmt.Sprintf("%s/%d?api-version=%s", c.prsURL(org, project, name), pr.Number, APIVersion)
	return c.do(ctx, repo, http.MethodPatch, u, body, nil)
}

// ListRepos lists every enabled repository with a default branch in every project
// of Org (rest-projects-list.md, rest-repositories-list.md). Azure DevOps has no
// install event, so this is how existing repositories are registered.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	if !segment.MatchString(c.Org) {
		return nil, fmt.Errorf("azure devops organization %q is not valid", c.Org)
	}
	anon := model.Repo{}
	const top = 100
	var repos []model.Repo
	for skip := 0; ; skip += top {
		var ps struct {
			Value []struct{ Name string } `json:"value"`
		}
		u := fmt.Sprintf("%s/%s/_apis/projects?$top=%d&$skip=%d&api-version=%s", c.base(), c.Org, top, skip, APIVersion)
		if err := c.do(ctx, anon, http.MethodGet, u, nil, &ps); err != nil {
			return nil, err
		}
		for _, p := range ps.Value {
			if !segment.MatchString(p.Name) {
				c.skip("project", p.Name)
				continue
			}
			var rs struct {
				Value []struct {
					Name          string `json:"name"`
					DefaultBranch string `json:"defaultBranch"`
					IsDisabled    bool   `json:"isDisabled"`
				} `json:"value"`
			}
			ru := fmt.Sprintf("%s/%s/%s/_apis/git/repositories?api-version=%s", c.base(), c.Org, url.PathEscape(p.Name), APIVersion)
			if err := c.do(ctx, anon, http.MethodGet, ru, nil, &rs); err != nil {
				return nil, err
			}
			for _, r := range rs.Value {
				if r.IsDisabled || r.DefaultBranch == "" {
					continue // disabled, or empty: nothing to scan
				}
				if !segment.MatchString(r.Name) {
					c.skip("repository", r.Name)
					continue
				}
				repos = append(repos, model.Repo{FullName: c.Org + "/" + p.Name + "/" + r.Name, DefaultBranch: strings.TrimPrefix(r.DefaultBranch, "refs/heads/")})
			}
		}
		if len(ps.Value) < top {
			return repos, nil
		}
	}
}

func (c *Client) skip(kind, name string) {
	if c.Log != nil {
		c.Log.Warn("azure devops name skipped: only letters, digits, . _ - are supported", "kind", kind, "name", name)
	}
}

// Checkout shallow-clones the default branch over HTTPS with the PAT as Basic
// credentials (git sends an empty user name, as the PAT docs show).
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	org, project, name, err := split(repo.FullName)
	if err != nil {
		return nil, err
	}
	o := gitws.Options{GitBase: c.GitBase, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	cloneURL := fmt.Sprintf("%s/%s/%s/_git/%s", strings.TrimRight(c.GitBase, "/"), org, project, name)
	return gitws.Checkout(ctx, o, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		return c.Credentials.Credential(ctx, repo)
	})
}

// maxPushCommits bounds the commits whose changes a push looks up; a larger push
// is re-scanned instead.
const maxPushCommits = 20

// ChangedFiles is the union of the paths the commits touched, from "Commits - Get
// Changes" (rest-commits-get-changes.md), without the leading slash and without
// folders. A rename counts its old path too. It returns an error when the answer
// may be incomplete (more commits than maxPushCommits, or fewer changes returned
// than changeCounts reports), and the caller then lets the engine re-scan.
func (c *Client) ChangedFiles(ctx context.Context, fullName string, commits []string) ([]string, error) {
	org, project, name, err := split(fullName)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 || len(commits) > maxPushCommits {
		return nil, fmt.Errorf("push has %d commits; changed files not resolved", len(commits))
	}
	repo := model.Repo{FullName: fullName}
	seen := map[string]bool{}
	var files []string
	for _, id := range commits {
		if !commitID.MatchString(id) {
			return nil, fmt.Errorf("unsupported commit id %q", id)
		}
		var res struct {
			ChangeCounts map[string]int `json:"changeCounts"`
			Changes      []struct {
				Item struct {
					Path     string `json:"path"`
					IsFolder bool   `json:"isFolder"`
				} `json:"item"`
				OriginalPath string `json:"originalPath"`
			} `json:"changes"`
		}
		u := fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s/commits/%s/changes?api-version=%s", c.base(), org, project, name, id, APIVersion)
		if err := c.do(ctx, repo, http.MethodGet, u, nil, &res); err != nil {
			return nil, err
		}
		total := 0
		for _, n := range res.ChangeCounts {
			total += n
		}
		if total > len(res.Changes) {
			return nil, fmt.Errorf("commit %s: %d of %d changes returned", id, len(res.Changes), total)
		}
		for _, ch := range res.Changes {
			for _, p := range []string{ch.Item.Path, ch.OriginalPath} {
				p = strings.TrimPrefix(p, "/")
				if p == "" || ch.Item.IsFolder || seen[p] {
					continue
				}
				seen[p] = true
				files = append(files, p)
			}
		}
	}
	return files, nil
}

var commitID = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
