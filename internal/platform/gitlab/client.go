// Package gitlab implements platform.Platform for GitLab (gitlab.com and self-managed):
// merge requests through the REST API v4, merge when checks pass, project discovery
// by membership listing, and a webhook receiver for push and project events.
//
// It was written from GitLab's own documentation, vendored under docs/vendor/gitlab
// (pinned in UPSTREAM.md). Nothing here has run against a real GitLab; docs/platform-gitlab.md
// lists what is unverified.
package gitlab

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

const (
	// GitUser is the basic-auth user git sends. docs/vendor/gitlab/user_profile_personal_access_tokens.md
	// ("Any string that is not empty. GitLab does not validate this value.") and the same
	// sentence for project access tokens: the token is the password.
	GitUser = "oauth2"

	// DraftPrefix starts the title of a draft merge request. The create endpoint has no
	// draft attribute; docs/vendor/gitlab/user_project_merge_requests_drafts.md: add
	// `Draft:` to the beginning of the merge request's title.
	DraftPrefix = "Draft: "

	perPage  = 100
	maxPages = 100
)

// A GitLab namespace path: up to 20 nested groups (user_group_subgroups__index.md, "Be
// nested up to 20 levels") and a project. Segments start with a letter, digit or
// underscore, which also rules out "." and "..". A trailing .git or .atom would not
// survive in a clone URL or feed URL, and GitLab refuses it for project paths.
var segment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

const maxSegments = 21

// CheckPath validates a project path ("group/sub/project") before any URL is built from it.
func CheckPath(p string) error {
	segs := strings.Split(p, "/")
	if len(segs) < 2 || len(segs) > maxSegments {
		return fmt.Errorf("refusing unsafe project path %q", p)
	}
	for _, s := range segs {
		if !segment.MatchString(s) || strings.HasSuffix(s, ".git") || strings.HasSuffix(s, ".atom") || len(s) > 255 {
			return fmt.Errorf("refusing unsafe project path %q", p)
		}
	}
	return nil
}

// Tokens is the credential source. Default covers every project it can reach;
// Scoped maps a namespace path ("group/sub") or a project path ("group/sub/app") to the
// token to use there instead, which is how project and group access tokens are used:
// each reaches only its own project or group. The longest matching scope wins.
// Both kinds are sent the same way (see Client).
type Tokens struct {
	Default string
	Scoped  map[string]string
}

// scope returns the key of the token that applies to project path ("" for Default) and
// the token itself; ok is false when none applies.
func (t Tokens) scope(path string) (key, secret string, ok bool) {
	best := -1
	for k, s := range t.Scoped {
		if (path == k || strings.HasPrefix(path, k+"/")) && len(k) > best {
			best, key, secret = len(k), k, s
		}
	}
	if best >= 0 {
		return key, secret, true
	}
	return "", t.Default, t.Default != ""
}

// all lists every distinct token with its scope key.
func (t Tokens) all() map[string]string {
	out := map[string]string{}
	if t.Default != "" {
		out[""] = t.Default
	}
	for k, s := range t.Scoped {
		out[k] = s
	}
	return out
}

// Credential implements platform.Credentials.
func (t Tokens) Credential(_ context.Context, repo model.Repo) (platform.Credential, error) {
	_, secret, ok := t.scope(repo.FullName)
	if !ok {
		return platform.Credential{}, fmt.Errorf("no GitLab token configured for %s", repo.FullName)
	}
	return platform.Credential{Username: GitUser, Secret: secret}, nil
}

// Client is GitLab as a platform.Platform. API calls authenticate with
// `Authorization: Bearer <token>`, which docs/vendor/gitlab/api_rest_authentication.md
// accepts for OAuth 2.0 tokens and for personal, project and group access tokens alike
// (the `PRIVATE-TOKEN` header it recommends for access tokens is not needed).
type Client struct {
	// BaseURL is the instance, e.g. https://gitlab.com; the API is BaseURL/api/v4. A
	// self-managed instance under a relative URL root works because the path is appended.
	BaseURL string
	// GitBase is the clone URL prefix, BaseURL when empty; the clone URL is GitBase/<path>.git.
	GitBase string
	Tokens  Tokens
	HTTP    *http.Client
	Log     *slog.Logger
	// AuthorName and AuthorEmail identify commits.
	AuthorName, AuthorEmail string
	WorkDir                 string
	GoProxy, GoSumDB        string
	GoCacheDir              string
	Sandbox                 sandbox.Backend
	// Now is the clock a Retry-After is counted from; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	limited map[string]time.Time
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

func (c *Client) api() string { return strings.TrimRight(c.BaseURL, "/") + "/api/v4" }

func (c *Client) gitBase() string {
	if c.GitBase != "" {
		return c.GitBase
	}
	return c.BaseURL
}

// projectURL is the API URL of a project: the path is one URL-encoded segment
// (group%2Fsub%2Fapp), api_rest__index.md "Namespaced paths".
func (c *Client) projectURL(path, rest string) string {
	return c.api() + "/projects/" + url.PathEscape(path) + rest
}

// RateLimitedUntil is when the last rate-limited answer to repo's token asked us to
// retry. GitLab rate limits per user or IP (administration_settings_user_and_ip_rate_limits.md),
// so a project token, which is its own bot user, has its own allowance.
func (c *Client) RateLimitedUntil(repo model.Repo) time.Time {
	key, _, _ := c.Tokens.scope(repo.FullName)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited[key]
}

func (c *Client) noteLimit(key string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limited == nil {
		c.limited = map[string]time.Time{}
	}
	if until.After(c.limited[key]) {
		c.limited[key] = until
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// retryAt: 429 carries Retry-After in seconds (administration_settings_user_and_ip_rate_limits.md,
// "Retry-After | 30"); RateLimit-Reset is Unix time. A bare 429 waits a minute.
func retryAt(r *http.Response, now time.Time) time.Time {
	if s, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil && s >= 0 {
		return now.Add(time.Duration(s) * time.Second)
	}
	if s, err := strconv.ParseInt(r.Header.Get("RateLimit-Reset"), 10, 64); err == nil {
		return time.Unix(s, 0)
	}
	return now.Add(time.Minute)
}

// do sends one API request with the token of scope key. It returns the status code;
// a 429 is a *platform.RateLimitError and is remembered for the scope.
func (c *Client) do(ctx context.Context, key, secret, method, rawURL string, in, out any) (int, error) {
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
	req.Header.Set("Authorization", "Bearer "+secret)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("gitlab %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		until := retryAt(resp, c.now())
		c.noteLimit(key, until)
		return resp.StatusCode, &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("gitlab %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("gitlab %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return resp.StatusCode, nil
}

// doRepo is do with the token that applies to repo.
func (c *Client) doRepo(ctx context.Context, repo model.Repo, method, rawURL string, in, out any) (int, error) {
	if err := CheckPath(repo.FullName); err != nil {
		return 0, err
	}
	key, secret, ok := c.Tokens.scope(repo.FullName)
	if !ok {
		return 0, fmt.Errorf("no GitLab token configured for %s", repo.FullName)
	}
	return c.do(ctx, key, secret, method, rawURL, in, out)
}

type mrJSON struct {
	IID          int    `json:"iid"`
	WebURL       string `json:"web_url"`
	SourceBranch string `json:"source_branch"`
}

func (m mrJSON) pr() platform.PR {
	return platform.PR{Number: m.IID, URL: m.WebURL, Head: m.SourceBranch}
}

// ListOpenPRs lists the opened merge requests whose source branch starts with prefix.
// The list endpoint has no prefix filter, so every page is read and filtered here.
// Paging stops on a short page rather than trusting X-Next-Page, which GitLab.com
// omits for large result sets (api_rest__index.md, "Pagination").
func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	var out []platform.PR
	for page := 1; page <= maxPages; page++ {
		var mrs []mrJSON
		q := fmt.Sprintf("?state=opened&per_page=%d&page=%d", perPage, page)
		if _, err := c.doRepo(ctx, repo, http.MethodGet, c.projectURL(repo.FullName, "/merge_requests"+q), nil, &mrs); err != nil {
			return nil, err
		}
		for _, m := range mrs {
			if strings.HasPrefix(m.SourceBranch, prefix) {
				out = append(out, m.pr())
			}
		}
		if len(mrs) < perPage {
			break
		}
	}
	return out, nil
}

// CreatePR opens a merge request. A draft is a title prefix; labels travel in the
// same call as a comma-separated list and GitLab creates the ones that do not exist.
// A label containing a comma cannot be expressed and is dropped.
func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	title := s.Title
	if s.Draft {
		title = DraftPrefix + title
	}
	payload := map[string]any{"source_branch": s.Head, "target_branch": s.Base, "title": title, "description": s.Body}
	var labels []string
	for _, l := range s.Labels {
		if l != "" && !strings.Contains(l, ",") {
			labels = append(labels, l)
		}
	}
	if len(labels) > 0 {
		payload["labels"] = strings.Join(labels, ",")
	}
	var m mrJSON
	if _, err := c.doRepo(ctx, repo, http.MethodPost, c.projectURL(repo.FullName, "/merge_requests"), payload, &m); err != nil {
		return platform.PR{}, err
	}
	pr := m.pr()
	pr.Head = s.Head
	return pr, nil
}

// RequestAutoMerge asks GitLab to merge the merge request when its checks pass.
// `auto_merge` is the current attribute; `merge_when_pipeline_succeeds` was deprecated
// in 17.11 and is sent too so an older self-managed instance, which ignores the new
// name, still sets it (api_merge_requests.md, "Merge a merge request"). Where every
// check already passes GitLab merges at once, as it does for a human who clicks the button.
func (c *Client) RequestAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	if pr.Number <= 0 {
		return errors.New("merge request has no iid")
	}
	rest := "/merge_requests/" + strconv.Itoa(pr.Number) + "/merge"
	body := map[string]any{"auto_merge": true, "merge_when_pipeline_succeeds": true, "squash": true}
	_, err := c.doRepo(ctx, repo, http.MethodPut, c.projectURL(repo.FullName, rest), body, nil)
	return err
}

// ListRepos is every project each configured token is a member of, unarchived.
// A project or group access token's bot is a member of its project or group, so it
// lists exactly what it was issued for. An error leaves the previous listing in place.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	seen := map[string]bool{}
	var out []model.Repo
	for key, secret := range c.Tokens.all() {
		for page := 1; page <= maxPages; page++ {
			var ps []struct {
				Path string `json:"path_with_namespace"`
			}
			u := fmt.Sprintf("%s/projects?membership=true&archived=false&simple=true&per_page=%d&page=%d", c.api(), perPage, page)
			if _, err := c.do(ctx, key, secret, http.MethodGet, u, nil, &ps); err != nil {
				return nil, err
			}
			for _, p := range ps {
				if CheckPath(p.Path) != nil || seen[p.Path] {
					continue
				}
				seen[p.Path] = true
				out = append(out, model.Repo{FullName: p.Path})
			}
			if len(ps) < perPage {
				break
			}
		}
	}
	return out, nil
}

// Checkout shallow-clones the project's default branch over HTTPS with the project's token.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	if err := CheckPath(repo.FullName); err != nil {
		return nil, err
	}
	o := gitws.Options{GitBase: c.gitBase(), AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	cloneURL := strings.TrimRight(c.gitBase(), "/") + "/" + repo.FullName + ".git"
	return gitws.Checkout(ctx, o, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		return c.Tokens.Credential(ctx, repo)
	})
}
