// Package gitea implements platform.Platform for Gitea and Forgejo: one client with
// two flavours. Forgejo forked Gitea and reports a version of the form
// X.Y.Z+gitea-A.B.C, which is how the flavour is detected. Written from the
// documentation vendored in docs/vendor/gitea and docs/vendor/forgejo; docs/platform-gitea.md
// lists what no real instance has confirmed.
package gitea

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

// Flavour is which of the two projects an instance runs.
type Flavour string

const (
	Auto    Flavour = ""
	Gitea   Flavour = "gitea"
	Forgejo Flavour = "forgejo"
)

// ParseFlavour reads a RIPPLE_GITEA_FLAVOUR value.
func ParseFlavour(s string) (Flavour, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return Auto, nil
	case "gitea":
		return Gitea, nil
	case "forgejo":
		return Forgejo, nil
	}
	return Auto, fmt.Errorf("unknown flavour %q (want auto, gitea or forgejo)", s)
}

// DetectFlavour classifies the answer of GET /api/v1/version. Forgejo 7 and later
// carry "+gitea-<version>" (docs/vendor/forgejo/numbering-scheme.md); anything else
// is Gitea. Forgejo before 7 reported a plain "1.21.7-0" and cannot be told apart:
// set the flavour explicitly there (the two differ only in the merge body's key case,
// which real servers read case-insensitively).
func DetectFlavour(version string) Flavour {
	if strings.Contains(version, "+gitea-") {
		return Forgejo
	}
	return Gitea
}

const pageSize = 50 // MAX_RESPONSE_ITEMS default (docs/vendor/*/config-excerpt.txt)

// workInProgress is the title prefix that makes a PR a draft
// (docs/vendor/gitea/pull-request.md); CreatePullRequestOption has no draft field.
const workInProgress = "WIP: "

var segment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type Client struct {
	// APIBase is the instance URL, e.g. https://gitea.example.org; /api/v1 is added.
	APIBase string
	// GitBase is the clone URL prefix, usually the same instance URL.
	GitBase string
	// Credentials supplies the access token (Secret) and the git basic-auth user.
	Credentials platform.Credentials
	// Flavour, when not Auto, skips detection.
	Flavour Flavour
	HTTP    *http.Client
	Log     *slog.Logger

	AuthorName, AuthorEmail string
	WorkDir                 string
	GoProxy, GoSumDB        string
	GoCacheDir              string
	Sandbox                 sandbox.Backend
	// Now is the clock a Retry-After is counted from; nil means time.Now.
	Now func() time.Time
	// MergeSettle is how long RequestAutoMerge waits while the instance answers 405 "Please try
	// again later" (it is still checking whether the pull request can merge). Zero means 15 s;
	// negative means ask once.
	MergeSettle time.Duration

	mu      sync.Mutex
	flavour Flavour
	limited time.Time
}

var (
	_ platform.Platform    = (*Client)(nil)
	_ platform.AutoMerger  = (*Client)(nil)
	_ platform.RateLimiter = (*Client)(nil)
	_ platform.Lister      = (*Client)(nil)
	_ platform.PRCloser    = (*Client)(nil)
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

func (c *Client) api(path string) string { return strings.TrimRight(c.APIBase, "/") + "/api/v1" + path }

// RateLimitedUntil is when the last rate-limited answer asked us to retry. One
// token serves the whole instance, so the limit is not per repository.
func (c *Client) RateLimitedUntil(model.Repo) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited
}

func checkRepo(r model.Repo) (owner, name string, err error) {
	o, n, ok := strings.Cut(r.FullName, "/")
	if !ok || !segment.MatchString(o) || !segment.MatchString(n) || o == "." || o == ".." || n == "." || n == ".." {
		return "", "", fmt.Errorf("refusing unsafe repository name %q", r.FullName)
	}
	return o, n, nil
}

func (c *Client) do(ctx context.Context, repo model.Repo, method, rawURL string, in, out any) (http.Header, error) {
	cred, err := c.Credentials.Credential(ctx, repo)
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
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ripple")
	req.Header.Set("Authorization", "token "+cred.Secret)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && resp.Header.Get("Retry-After") != "") {
		until := retryAt(resp.Header, c.now())
		c.mu.Lock()
		if until.After(c.limited) {
			c.limited = until
		}
		c.mu.Unlock()
		return resp.Header, &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return resp.Header, &statusError{status: resp.StatusCode, msg: fmt.Sprintf("gitea %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, fmt.Errorf("gitea %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return resp.Header, nil
}

// statusError is a non-2xx answer; its text is what do always said.
type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

func isNotFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusNotFound
}

var ietfReset = regexp.MustCompile(`\bt=(\d+)`)

// retryAt reads Retry-After (seconds), then the IETF RateLimit header's t= (seconds
// to reset, as codeberg.org sends it), else waits a minute.
func retryAt(h http.Header, now time.Time) time.Time {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
		return now.Add(time.Duration(s) * time.Second)
	}
	if m := ietfReset.FindStringSubmatch(h.Get("RateLimit")); m != nil {
		s, _ := strconv.Atoi(m[1])
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

// DetectedFlavour is the configured flavour, or the one the instance's version
// endpoint reports (asked once per success).
func (c *Client) DetectedFlavour(ctx context.Context) (Flavour, error) {
	if c.Flavour != Auto {
		return c.Flavour, nil
	}
	c.mu.Lock()
	f := c.flavour
	c.mu.Unlock()
	if f != Auto {
		return f, nil
	}
	var v struct {
		Version string `json:"version"`
	}
	if _, err := c.do(ctx, model.Repo{}, http.MethodGet, c.api("/version"), nil, &v); err != nil {
		return Auto, err
	}
	f = DetectFlavour(v.Version)
	c.mu.Lock()
	c.flavour = f
	c.mu.Unlock()
	return f, nil
}

type prJSON struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

func (p prJSON) pr() platform.PR {
	return platform.PR{Number: p.Number, URL: p.HTMLURL, Head: p.Head.Ref, HeadSHA: p.Head.SHA, Body: p.Body}
}

// paged calls fn with each page of a listing until the server stops saying there
// is a next one (Link rel="next"), or sends an empty page. At most maxPages.
func (c *Client) paged(ctx context.Context, repo model.Repo, path string, fn func(raw json.RawMessage) (int, error)) error {
	const maxPages = 40
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; page <= maxPages; page++ {
		var raw json.RawMessage
		h, err := c.do(ctx, repo, http.MethodGet, c.api(path)+sep+"limit="+strconv.Itoa(pageSize)+"&page="+strconv.Itoa(page), nil, &raw)
		if err != nil {
			return err
		}
		n, err := fn(raw)
		if err != nil {
			return err
		}
		if n == 0 || !strings.Contains(h.Get("Link"), `rel="next"`) {
			return nil
		}
	}
	return nil
}

func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	o, n, err := checkRepo(repo)
	if err != nil {
		return nil, err
	}
	var out []platform.PR
	err = c.paged(ctx, repo, fmt.Sprintf("/repos/%s/%s/pulls?state=open", o, n), func(raw json.RawMessage) (int, error) {
		var prs []prJSON
		if err := json.Unmarshal(raw, &prs); err != nil {
			return 0, err
		}
		for _, p := range prs {
			if strings.HasPrefix(p.Head.Ref, prefix) {
				out = append(out, p.pr())
			}
		}
		return len(prs), nil
	})
	return out, err
}

func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	o, n, err := checkRepo(repo)
	if err != nil {
		return platform.PR{}, err
	}
	title := s.Title
	if s.Draft {
		title = workInProgress + title
	}
	var p prJSON
	_, err = c.do(ctx, repo, http.MethodPost, c.api(fmt.Sprintf("/repos/%s/%s/pulls", o, n)),
		map[string]any{"title": title, "head": s.Head, "base": s.Base, "body": s.Body}, &p)
	if err != nil {
		return platform.PR{}, err
	}
	pr := p.pr()
	pr.Head = s.Head
	if len(s.Labels) > 0 {
		// Label names are accepted here (IssueLabelsOption). A name the repository lacks
		// is dropped silently: the answer is 200 with the labels now on the issue
		// (Gitea 1.27.3, Forgejo 17.0.2), so the answer is read and the gap logged.
		// Forgejo 7.0.16 (Gitea 1.21) takes ids only and answers 422 to names, so a 422
		// is retried with the ids found in GET .../labels.
		applied, err := c.postLabels(ctx, repo, o, n, pr.Number, s.Labels)
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusUnprocessableEntity {
			applied, err = c.postLabelIDs(ctx, repo, o, n, pr.Number, s.Labels)
		}
		if c.Log != nil {
			if err != nil {
				c.Log.Warn("labelling pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
			} else if missing := notIn(s.Labels, applied); len(missing) > 0 {
				c.Log.Warn("labels not applied: the repository has no label of that name", "repo", repo.FullName, "pr", pr.Number, "labels", missing)
			}
		}
	}
	return pr, nil
}

type labelJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (c *Client) postLabels(ctx context.Context, repo model.Repo, o, n string, pr int, labels any) ([]labelJSON, error) {
	var applied []labelJSON
	_, err := c.do(ctx, repo, http.MethodPost, c.api(fmt.Sprintf("/repos/%s/%s/issues/%d/labels", o, n, pr)),
		map[string]any{"labels": labels}, &applied)
	return applied, err
}

// postLabelIDs resolves names to ids (the first 50 labels of the repository) and posts the ids.
func (c *Client) postLabelIDs(ctx context.Context, repo model.Repo, o, n string, pr int, names []string) ([]labelJSON, error) {
	var all []labelJSON
	if _, err := c.do(ctx, repo, http.MethodGet, c.api(fmt.Sprintf("/repos/%s/%s/labels?limit=50", o, n)), nil, &all); err != nil {
		return nil, err
	}
	var ids []int64
	for _, l := range all {
		for _, name := range names {
			if l.Name == name {
				ids = append(ids, l.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return c.postLabels(ctx, repo, o, n, pr, ids)
}

// notIn returns the names that no applied label carries.
func notIn(names []string, applied []labelJSON) []string {
	have := map[string]bool{}
	for _, l := range applied {
		have[l.Name] = true
	}
	var missing []string
	for _, name := range names {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// RequestAutoMerge schedules a squash merge for when the checks succeed
// ("merge when checks succeed"). The body's key is `do` on Gitea and `Do` on
// Forgejo, as their OpenAPI documents spell it.
func (c *Client) RequestAutoMerge(ctx context.Context, repo model.Repo, pr platform.PR) error {
	o, n, err := checkRepo(repo)
	if err != nil {
		return err
	}
	f, err := c.DetectedFlavour(ctx)
	if err != nil {
		return err
	}
	key := "do"
	if f == Forgejo {
		key = "Do"
	}
	wait := c.MergeSettle
	if wait == 0 {
		wait = 15 * time.Second
	}
	deadline := time.Now().Add(wait)
	poll := max(wait/30, 10*time.Millisecond)
	for {
		_, err = c.do(ctx, repo, http.MethodPost, c.api(fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", o, n, pr.Number)),
			map[string]any{key: "squash", "merge_when_checks_succeed": true}, nil)
		// Gitea 1.27.3 answers 405 while the pull request is still being checked for mergeability
		// (docs/vendor/gitea/real-answers.md); every other error is final.
		var se *statusError
		if err == nil || wait < 0 || !errors.As(err, &se) || se.status != http.StatusMethodNotAllowed ||
			!time.Now().Add(poll).Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// ownTimeline are the timeline entry types ripple's own actions leave on a pull
// request: labelling it (CreatePR) and scheduling or cancelling the auto-merge
// (RequestAutoMerge, Gitea cancelling it when checks fail). The names are Gitea's
// comment types from its source, not from its API documents
// (docs/vendor/gitea/supersede-excerpts.md); any other type counts as touched.
var ownTimeline = map[string]bool{"label": true, "pull_scheduled_merge": true, "pull_cancel_scheduled_merge": true}

// CloseUntouchedPR closes pr and comments on it unless a person touched it. Touched
// means: the head branch is not in this repository; the pull request has other than
// exactly one commit, or that commit's author or committer is not ripple's identity;
// it has a review, an issue comment, a timeline entry other than ownTimeline (which is
// how Gitea and Forgejo report a rename, "change_title"), or more than one "pull_push"
// (the first records the push that opened it; a later push or force-push adds another,
// which the real instances confirm only for the first); or, on Gitea, a non-zero
// content_version, which an edited description raises (Forgejo reports no such field,
// so a description edit goes unnoticed there). Every read must succeed and nothing is
// closed on a failed one. The pull request is closed first and the comment is best
// effort, so a failed close leaves nothing to repeat on the retry. Verified only
// against the fake in internal/testutil.
func (c *Client) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr platform.PR, comment string) (bool, error) {
	o, n, err := checkRepo(repo)
	if err != nil {
		return false, err
	}
	if pr.Number <= 0 {
		return false, errors.New("pull request has no number")
	}
	pull := fmt.Sprintf("/repos/%s/%s/pulls/%d", o, n, pr.Number)
	issue := fmt.Sprintf("/repos/%s/%s/issues/%d", o, n, pr.Number)
	// swagger repoGetPullRequest: state, head.ref, head.repo.full_name, content_version.
	var cur struct {
		State string `json:"state"`
		Head  struct {
			Ref  string `json:"ref"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		ContentVersion *int `json:"content_version"`
	}
	if _, err := c.do(ctx, repo, http.MethodGet, c.api(pull), nil, &cur); err != nil {
		return false, err
	}
	if cur.State != "open" || cur.Head.Ref != pr.Head || cur.Head.Repo == nil || !strings.EqualFold(cur.Head.Repo.FullName, repo.FullName) {
		return false, nil
	}
	if cur.ContentVersion != nil && *cur.ContentVersion != 0 {
		return false, nil
	}
	// swagger repoGetPullRequestCommits: commit.author.email, commit.committer.email.
	var commits []struct {
		Commit struct {
			Author    struct{ Email string } `json:"author"`
			Committer struct{ Email string } `json:"committer"`
		} `json:"commit"`
	}
	if _, err := c.do(ctx, repo, http.MethodGet, c.api(pull+"/commits?limit=2&verification=false&files=false"), nil, &commits); err != nil {
		return false, err
	}
	ours := gitws.AuthorEmail(gitws.Options{AuthorEmail: c.AuthorEmail})
	if len(commits) != 1 || commits[0].Commit.Author.Email != ours || commits[0].Commit.Committer.Email != ours {
		return false, nil
	}
	touched := false
	// swagger repoListPullReviews and issueGetComments: any entry is a person's.
	for _, path := range []string{pull + "/reviews", issue + "/comments"} {
		if err := c.paged(ctx, repo, path, func(raw json.RawMessage) (int, error) {
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return 0, err
			}
			touched = touched || len(items) > 0
			return len(items), nil
		}); err != nil {
			return false, err
		}
		if touched {
			return false, nil
		}
	}
	// swagger issueGetCommentsAndTimeline: TimelineComment.type.
	pushes := 0
	if err := c.paged(ctx, repo, issue+"/timeline", func(raw json.RawMessage) (int, error) {
		var items []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return 0, err
		}
		for _, it := range items {
			if it.Type == "pull_push" {
				// Opening the pull request records the push that created its head, so one
				// is ripple's own; a second is a later push or a force-push (hosts-gitea.yml).
				pushes++
				continue
			}
			touched = touched || !ownTimeline[it.Type]
		}
		return len(items), nil
	}); err != nil {
		return false, err
	}
	if touched || pushes > 1 {
		return false, nil
	}
	// swagger repoEditPullRequest: state closed.
	if _, err := c.do(ctx, repo, http.MethodPatch, c.api(pull), map[string]string{"state": "closed"}, nil); err != nil {
		return false, err
	}
	// swagger issueCreateComment: body.
	if _, err := c.do(ctx, repo, http.MethodPost, c.api(issue+"/comments"), map[string]string{"body": comment}, nil); err != nil && c.Log != nil {
		c.Log.Warn("commenting on the closed superseded pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
	}
	return true, nil
}

// ListRepos is every repository the token can push to: not archived, not empty.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	return c.listRepos(ctx, false)
}

// ListArchivedRepos is the archived ones among them. Gitea and Forgejo send no archive
// event (docs/vendor/gitea/webhooks.md, docs/vendor/forgejo/webhooks.md), but each
// repository object carries `archived` (swagger), so a listing tells an archive from a deletion.
func (c *Client) ListArchivedRepos(ctx context.Context) ([]model.Repo, error) {
	return c.listRepos(ctx, true)
}

func (c *Client) listRepos(ctx context.Context, archived bool) ([]model.Repo, error) {
	var out []model.Repo
	err := c.paged(ctx, model.Repo{}, "/user/repos", func(raw json.RawMessage) (int, error) {
		var rs []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Archived      bool   `json:"archived"`
			Empty         bool   `json:"empty"`
			Permissions   *struct {
				Push bool `json:"push"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal(raw, &rs); err != nil {
			return 0, err
		}
		for _, r := range rs {
			if r.Archived != archived || r.Empty || (r.Permissions != nil && !r.Permissions.Push) {
				continue
			}
			out = append(out, model.Repo{FullName: r.FullName, DefaultBranch: r.DefaultBranch})
		}
		return len(rs), nil
	})
	return out, err
}

// Checkout shallow-clones the default branch over HTTP with the token as the basic-auth password.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	o, n, err := checkRepo(repo)
	if err != nil {
		return nil, err
	}
	g := gitws.Options{GitBase: c.GitBase, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	cloneURL := strings.TrimRight(c.GitBase, "/") + "/" + url.PathEscape(o) + "/" + url.PathEscape(n) + ".git"
	return gitws.Checkout(ctx, g, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		return c.Credentials.Credential(ctx, repo)
	})
}
