// Package codecommit implements platform.Platform for AWS CodeCommit: pull requests
// through the JSON 1.1 API signed with SigV4, repository discovery by listing each
// configured region, git over HTTPS with the SigV4-derived git password, and a receiver
// for EventBridge events delivered through an SNS HTTPS subscription.
//
// It was written from AWS's own documentation, vendored under docs/vendor/codecommit
// (pinned in UPSTREAM.md). Nothing here has run against real AWS; docs/platform-codecommit.md
// lists what is unverified.
//
// Repositories are named "<region>/<name>" because a repository exists in one region
// and the name alone does not say which.
package codecommit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/sandbox"
)

const (
	service = "codecommit"
	// target is the X-Amz-Target prefix, from the "CodeCommit_20150413" JSON protocol
	// version in docs/vendor/codecommit/api-Welcome.md ("Making API requests").
	target = "CodeCommit_20150413"

	// DraftPrefix starts the title of a pull request ripple opens as a draft. CodeCommit
	// has no draft state (docs/vendor/codecommit/api-API_CreatePullRequest.md has no such
	// parameter), so the only honest signal is the title.
	DraftPrefix = "Draft: "

	// maxTitle: api-API_CreatePullRequest.md gives "Maximum length of 150" for the title
	// parameter but its InvalidTitleException text says 100; the lower bound is safe.
	maxTitle = 100
	// maxDescription is the documented "Maximum length of 10240" of the description.
	maxDescription = 10240

	maxPages = 100
)

var (
	// A region as AWS names them: us-east-2, ap-southeast-1, us-gov-west-1, cn-north-1.
	regionRE = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]+)+-[0-9]{1,2}$`)
	// A repository name: api-API_ListPullRequests.md "Pattern: [\w\.-]+", 1 to 100.
	// CodeCommit refuses a ".git" suffix, which would also change the clone URL.
	nameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

// CheckRegion validates an AWS region before it is put in a host name.
func CheckRegion(r string) error {
	if !regionRE.MatchString(r) {
		return fmt.Errorf("refusing unsafe region %q", r)
	}
	return nil
}

// CheckPath validates "<region>/<repository>" before any URL is built from it, and
// returns its parts.
func CheckPath(p string) (region, name string, err error) {
	region, name, ok := strings.Cut(p, "/")
	if !ok || CheckRegion(region) != nil || !nameRE.MatchString(name) || strings.HasSuffix(name, ".git") || name == "." || name == ".." {
		return "", "", fmt.Errorf("refusing unsafe repository path %q", p)
	}
	return region, name, nil
}

// domain is the DNS suffix of a region's endpoints (grc-init.py website_domain_mapping).
func domain(region string) string {
	switch region {
	case "cn-north-1", "cn-northwest-1":
		return "amazonaws.com.cn"
	case "eusc-de-east-1":
		return "amazonaws.eu"
	}
	return "amazonaws.com"
}

// GitHost is the git connection endpoint host of a region
// (docs/vendor/codecommit/ug-regions.md, "Git connection endpoints").
func GitHost(region string) string { return "git-codecommit." + region + "." + domain(region) }

// GitPassword is the password git sends over HTTPS for the repository at path
// ("/v1/repos/<name>") on host: the UTC timestamp, a "Z", and a SigV4 signature of the
// fixed canonical request "GIT\n<path>\n\nhost:<host>\n\nhost\n". It reproduces
// git-remote-codecommit's sign() (docs/vendor/codecommit/grc-init.py); the timestamp
// is that script's, which has no trailing Z of its own. The password is valid for as
// long as SigV4 accepts the timestamp (15 minutes).
func GitPassword(host, path, region, secretKey string, now time.Time) string {
	ts := now.UTC().Format("20060102T150405")
	date := ts[:8]
	canonical := "GIT\n" + path + "\n\nhost:" + host + "\n\nhost\n"
	h := sha256.Sum256([]byte(canonical))
	scope := date + "/" + region + "/" + service + "/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + ts + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	mac := func(key []byte, msg string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(msg))
		return m.Sum(nil)
	}
	k := mac([]byte("AWS4"+secretKey), date)
	k = mac(k, region)
	k = mac(k, service)
	k = mac(k, "aws4_request")
	return ts + "Z" + hex.EncodeToString(mac(k, sts))
}

// GitUsername is the git user name: the access key id, with the session token appended
// after a "%" when the credentials are temporary (grc-init.py git_url). The script
// percent-encodes it for the URL; sent in a Basic header it is used as is.
func GitUsername(c aws.Credentials) string {
	if c.SessionToken != "" {
		return c.AccessKeyID + "%" + c.SessionToken
	}
	return c.AccessKeyID
}

// Client is AWS CodeCommit as a platform.Platform.
type Client struct {
	// Regions are the regions repositories are discovered in.
	Regions []string
	// Creds supplies the IAM credentials: one account, every configured region.
	Creds aws.CredentialsProvider
	// Endpoint, when set, replaces https://codecommit.<region>.amazonaws.com for the API
	// (a test double or a VPC endpoint); GitBase likewise replaces
	// https://git-codecommit.<region>.amazonaws.com for git. When GitBase is set the
	// signature is made for its host:port, which is what the server sees.
	Endpoint, GitBase string
	HTTP              *http.Client
	Log               *slog.Logger
	// AuthorName and AuthorEmail identify commits.
	AuthorName, AuthorEmail string
	WorkDir                 string
	GoProxy, GoSumDB        string
	GoCacheDir              string
	Sandbox                 sandbox.Backend
	// Now is the clock signatures and a throttle wait are counted from; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	limited map[string]time.Time
}

var (
	_ platform.Platform    = (*Client)(nil)
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

func (c *Client) endpoint(region string) string {
	if c.Endpoint != "" {
		return strings.TrimRight(c.Endpoint, "/")
	}
	return "https://codecommit." + region + "." + domain(region)
}

func (c *Client) gitBase(region string) string {
	if c.GitBase != "" {
		return strings.TrimRight(c.GitBase, "/")
	}
	return "https://" + GitHost(region)
}

// RateLimitedUntil is when the last throttled answer in repo's region asked us to
// wait. CodeCommit throttles per account and region.
func (c *Client) RateLimitedUntil(repo model.Repo) time.Time {
	region, _, err := CheckPath(repo.FullName)
	if err != nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited[region]
}

func (c *Client) noteLimit(region string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limited == nil {
		c.limited = map[string]time.Time{}
	}
	if until.After(c.limited[region]) {
		c.limited[region] = until
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// throttleWait is how long a throttled region is left alone: docs/vendor/codecommit/
// api-CommonErrors.md gives ThrottlingException no Retry-After, and the SDKs back off
// exponentially, so this is a fixed guess of one minute.
const throttleWait = time.Minute

// call runs one API operation in region. The body is signed with SigV4
// (docs/vendor/codecommit/iam-reference_sigv-create-signed-request.md). A
// ThrottlingException, or a 429, is a *platform.RateLimitError and is remembered.
func (c *Client) call(ctx context.Context, region, op string, in, out any) error {
	if c.Creds == nil {
		return errors.New("codecommit: no AWS credentials configured")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(region)+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target+"."+op)
	req.Header.Set("User-Agent", "ripple")
	creds, err := c.Creds.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("codecommit: credentials: %w", err)
	}
	sum := sha256.Sum256(body)
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), service, region, c.now()); err != nil {
		return fmt.Errorf("codecommit: sign %s: %w", op, err)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("codecommit %s: %w", op, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		kind := e.Type
		if i := strings.LastIndex(kind, "#"); i >= 0 {
			kind = kind[i+1:]
		}
		if kind == "ThrottlingException" || resp.StatusCode == http.StatusTooManyRequests {
			until := c.now().Add(throttleWait)
			c.noteLimit(region, until)
			return &platform.RateLimitError{Until: until}
		}
		return fmt.Errorf("codecommit %s: status %d: %s", op, resp.StatusCode, snippet(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("codecommit %s: decode: %w", op, err)
		}
	}
	return nil
}

type pullRequest struct {
	ID          string `json:"pullRequestId"`
	Status      string `json:"pullRequestStatus"`
	Description string `json:"description"`
	Targets     []struct {
		Repository string `json:"repositoryName"`
		Source     string `json:"sourceReference"`
		Dest       string `json:"destinationReference"`
		SourceSHA  string `json:"sourceCommit"`
		DestSHA    string `json:"destinationCommit"`
		MergeBase  string `json:"mergeBase"`
	} `json:"pullRequestTargets"`
}

// head is the source branch of the pull request's target in repo ("" when none).
func (p pullRequest) head(name string) string {
	for _, t := range p.Targets {
		if t.Repository == name {
			return strings.TrimPrefix(t.Source, "refs/heads/")
		}
	}
	return ""
}

// headSHA is the commit the source branch pointed at when the pull request was read
// (GetPullRequest pullRequestTargets[].sourceCommit); "" when the target is absent.
func (p pullRequest) headSHA(name string) string {
	for _, t := range p.Targets {
		if t.Repository == name {
			return t.SourceSHA
		}
	}
	return ""
}

func prURL(region, name, id string) string {
	return "https://" + region + ".console.aws.amazon.com/codesuite/codecommit/repositories/" + name + "/pull-requests/" + id + "?region=" + region
}

func (c *Client) pr(region, name string, p pullRequest, head string) platform.PR {
	n := 0
	fmt.Sscanf(p.ID, "%d", &n)
	return platform.PR{Number: n, URL: prURL(region, name, p.ID), Head: head, HeadSHA: p.headSHA(name), Body: p.Description}
}

// ListOpenPRs lists the open pull requests whose source branch starts with prefix.
// ListPullRequests returns only ids (api-API_ListPullRequests.md), and has no source
// branch filter, so each open id is fetched with GetPullRequest and filtered here.
func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	region, name, err := CheckPath(repo.FullName)
	if err != nil {
		return nil, err
	}
	var ids []string
	token := ""
	for page := 0; page < maxPages; page++ {
		in := map[string]any{"repositoryName": name, "pullRequestStatus": "OPEN"}
		if token != "" {
			in["nextToken"] = token
		}
		var out struct {
			IDs  []string `json:"pullRequestIds"`
			Next string   `json:"nextToken"`
		}
		if err := c.call(ctx, region, "ListPullRequests", in, &out); err != nil {
			return nil, err
		}
		ids = append(ids, out.IDs...)
		if token = out.Next; token == "" {
			break
		}
	}
	var res []platform.PR
	for _, id := range ids {
		var out struct {
			PR pullRequest `json:"pullRequest"`
		}
		if err := c.call(ctx, region, "GetPullRequest", map[string]string{"pullRequestId": id}, &out); err != nil {
			return nil, err
		}
		if h := out.PR.head(name); strings.HasPrefix(h, prefix) && out.PR.Status == "OPEN" {
			res = append(res, c.pr(region, name, out.PR, h))
		}
	}
	return res, nil
}

var _ platform.PRCloser = (*Client)(nil)

func (c *Client) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}

// maxPRCommits bounds the walk over a pull request's commits: a branch longer than
// this is not one ripple wrote, and the walk is not worth its API calls.
const maxPRCommits = 50

// CloseUntouchedPR closes pr, leaving comment, only when no person is known to have
// touched it. See platform.PRCloser. What CodeCommit lets this read:
//
//   - GetPullRequest: status, source branch, source and destination commits and the
//     merge base (api-API_GetPullRequest.md, api-API_PullRequestTarget.md).
//   - DescribePullRequestEvents: every event type but PULL_REQUEST_CREATED,
//     PULL_REQUEST_SOURCE_REFERENCE_UPDATED and PULL_REQUEST_MERGE_STATE_CHANGED
//     (a status change, an approval or an approval rule edit) is a person's, and an
//     unknown type is treated as one (https://docs.aws.amazon.com/codecommit/latest/APIReference/API_DescribePullRequestEvents.md).
//   - GetCommentsForPullRequest: any comment at all, deleted or not
//     (https://docs.aws.amazon.com/codecommit/latest/APIReference/API_GetCommentsForPullRequest.md).
//   - GetCommit: author and committer email of each commit from the source tip back
//     to the merge base; all must be ripple's, and a merge commit is a person's
//     (https://docs.aws.amazon.com/codecommit/latest/APIReference/API_GetCommit.md).
//
// A title or description edit is not reported by any CodeCommit API and is not
// detected. Close is UpdatePullRequestStatus to CLOSED
// (https://docs.aws.amazon.com/codecommit/latest/APIReference/API_UpdatePullRequestStatus.md),
// then PostCommentForPullRequest, which needs the destination and source commit ids
// (https://docs.aws.amazon.com/codecommit/latest/APIReference/API_PostCommentForPullRequest.md).
// Whether CodeCommit accepts a comment on a closed pull request is unverified: a
// refusal is logged and the close stands.
func (c *Client) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr platform.PR, comment string) (bool, error) {
	region, name, err := CheckPath(repo.FullName)
	if err != nil {
		return false, err
	}
	id := strconv.Itoa(pr.Number)
	var got struct {
		PR pullRequest `json:"pullRequest"`
	}
	if err := c.call(ctx, region, "GetPullRequest", map[string]string{"pullRequestId": id}, &got); err != nil {
		return false, err
	}
	if got.PR.Status != "OPEN" || got.PR.head(name) != pr.Head {
		return false, nil
	}
	if touched, err := c.eventsTouched(ctx, region, id); err != nil || touched {
		return false, err
	}
	if touched, err := c.commentsTouched(ctx, region, id); err != nil || touched {
		return false, err
	}
	var src, dst, base string
	for _, t := range got.PR.Targets {
		if t.Repository == name {
			src, dst, base = t.SourceSHA, t.DestSHA, t.MergeBase
		}
	}
	if src == "" || dst == "" || base == "" {
		return false, fmt.Errorf("codecommit: pull request %s names no source, destination or merge base commit", id)
	}
	if touched, err := c.commitsTouched(ctx, region, name, src, base); err != nil || touched {
		return false, err
	}
	if err := c.call(ctx, region, "UpdatePullRequestStatus", map[string]string{"pullRequestId": id, "pullRequestStatus": "CLOSED"}, nil); err != nil {
		return false, err
	}
	in := map[string]string{"pullRequestId": id, "repositoryName": name, "beforeCommitId": dst, "afterCommitId": src,
		"content": comment, "clientRequestToken": "ripple-supersede-" + id}
	if err := c.call(ctx, region, "PostCommentForPullRequest", in, nil); err != nil {
		c.log().Warn("codecommit: closed a superseded pull request but could not comment", "repo", repo.FullName, "pr", pr.Number, "err", err)
	}
	return true, nil
}

// eventsTouched is whether the pull request's event history holds anything a person did.
func (c *Client) eventsTouched(ctx context.Context, region, id string) (bool, error) {
	token := ""
	for page := 0; page < maxPages; page++ {
		in := map[string]any{"pullRequestId": id}
		if token != "" {
			in["nextToken"] = token
		}
		var out struct {
			Events []struct {
				Type string `json:"pullRequestEventType"`
			} `json:"pullRequestEvents"`
			Next string `json:"nextToken"`
		}
		if err := c.call(ctx, region, "DescribePullRequestEvents", in, &out); err != nil {
			return false, err
		}
		for _, e := range out.Events {
			switch e.Type {
			case "PULL_REQUEST_CREATED", "PULL_REQUEST_SOURCE_REFERENCE_UPDATED", "PULL_REQUEST_MERGE_STATE_CHANGED":
			default:
				return true, nil
			}
		}
		if token = out.Next; token == "" {
			return false, nil
		}
	}
	return false, fmt.Errorf("codecommit: pull request %s has more than %d pages of events", id, maxPages)
}

// commentsTouched is whether the pull request has any comment.
func (c *Client) commentsTouched(ctx context.Context, region, id string) (bool, error) {
	token := ""
	for page := 0; page < maxPages; page++ {
		in := map[string]any{"pullRequestId": id}
		if token != "" {
			in["nextToken"] = token
		}
		var out struct {
			Data []struct {
				Comments []json.RawMessage `json:"comments"`
			} `json:"commentsForPullRequestData"`
			Next string `json:"nextToken"`
		}
		if err := c.call(ctx, region, "GetCommentsForPullRequest", in, &out); err != nil {
			return false, err
		}
		for _, d := range out.Data {
			if len(d.Comments) > 0 {
				return true, nil
			}
		}
		if token = out.Next; token == "" {
			return false, nil
		}
	}
	return false, fmt.Errorf("codecommit: pull request %s has more than %d pages of comments", id, maxPages)
}

// commitsTouched is whether any commit from src back to base is not ripple's: a
// different author or committer email, a merge, a branch that does not reach base
// within maxPRCommits, or no commits at all (not a branch ripple pushed).
func (c *Client) commitsTouched(ctx context.Context, region, name, src, base string) (bool, error) {
	want := gitws.AuthorEmail(gitws.Options{AuthorEmail: c.AuthorEmail})
	for id, n := src, 0; id != base; n++ {
		if n >= maxPRCommits {
			return true, nil
		}
		var out struct {
			Commit struct {
				Author    struct{ Email string } `json:"author"`
				Committer struct{ Email string } `json:"committer"`
				Parents   []string               `json:"parents"`
			} `json:"commit"`
		}
		if err := c.call(ctx, region, "GetCommit", map[string]string{"repositoryName": name, "commitId": id}, &out); err != nil {
			return false, err
		}
		cm := out.Commit
		if cm.Author.Email != want || cm.Committer.Email != want || len(cm.Parents) != 1 {
			return true, nil
		}
		id = cm.Parents[0]
	}
	return src == base, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// CreatePR opens a pull request. A draft is a title prefix; labels cannot be expressed
// (CodeCommit pull requests have none) and are dropped. The clientRequestToken is
// derived from the branch pair so a retry after a lost answer does not open a second
// pull request: CreatePullRequest is idempotent per token (api-API_CreatePullRequest.md).
func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	region, name, err := CheckPath(repo.FullName)
	if err != nil {
		return platform.PR{}, err
	}
	title := s.Title
	if s.Draft {
		title = DraftPrefix + title
	}
	sum := sha256.Sum256([]byte(region + "\x00" + name + "\x00" + s.Head + "\x00" + s.Base + "\x00" + s.Title))
	in := map[string]any{
		"title":       truncate(title, maxTitle),
		"description": truncate(s.Body, maxDescription),
		"targets": []map[string]string{{"repositoryName": name,
			"sourceReference": "refs/heads/" + s.Head, "destinationReference": "refs/heads/" + s.Base}},
		"clientRequestToken": hex.EncodeToString(sum[:16]),
	}
	var out struct {
		PR pullRequest `json:"pullRequest"`
	}
	if err := c.call(ctx, region, "CreatePullRequest", in, &out); err != nil {
		return platform.PR{}, err
	}
	return c.pr(region, name, out.PR, s.Head), nil
}

// ListRepos is every repository the credentials can list in each configured region.
// An error leaves the previous listing in place.
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	var out []model.Repo
	for _, region := range c.Regions {
		if err := CheckRegion(region); err != nil {
			return nil, err
		}
		token := ""
		for page := 0; page < maxPages; page++ {
			in := map[string]any{}
			if token != "" {
				in["nextToken"] = token
			}
			var resp struct {
				Repositories []struct {
					Name string `json:"repositoryName"`
				} `json:"repositories"`
				Next string `json:"nextToken"`
			}
			if err := c.call(ctx, region, "ListRepositories", in, &resp); err != nil {
				return nil, err
			}
			for _, r := range resp.Repositories {
				p := region + "/" + r.Name
				if _, _, err := CheckPath(p); err == nil {
					out = append(out, model.Repo{FullName: p})
				}
			}
			if token = resp.Next; token == "" {
				break
			}
		}
	}
	return out, nil
}

// maxDiffPages bounds ChangedFiles; a push touching more files than this is scanned in full.
const maxDiffPages = 5

// ChangedFiles lists the paths that differ between two commits of a repository, with
// GetDifferences (docs/vendor/codecommit/api-API_GetDifferences.md): a path an
// add, change or delete touched, and both paths of a rename. A result that does not
// fit maxDiffPages pages is nil, which the caller treats as "scan everything".
func (c *Client) ChangedFiles(ctx context.Context, repo model.Repo, before, after string) ([]string, error) {
	region, name, err := CheckPath(repo.FullName)
	if err != nil {
		return nil, err
	}
	var files []string
	token := ""
	for page := 0; page < maxDiffPages; page++ {
		in := map[string]any{"repositoryName": name, "beforeCommitSpecifier": before, "afterCommitSpecifier": after, "MaxResults": 100}
		if token != "" {
			in["NextToken"] = token
		}
		var out struct {
			Differences []struct {
				Before struct{ Path string } `json:"beforeBlob"`
				After  struct{ Path string } `json:"afterBlob"`
			} `json:"differences"`
			Next string `json:"NextToken"`
		}
		if err := c.call(ctx, region, "GetDifferences", in, &out); err != nil {
			return nil, err
		}
		for _, d := range out.Differences {
			for _, p := range []string{d.Before.Path, d.After.Path} {
				if p != "" {
					files = append(files, p)
				}
			}
		}
		if token = out.Next; token == "" {
			return files, nil
		}
	}
	return nil, nil
}

// Checkout shallow-clones the repository's default branch over HTTPS. The git password
// is signed afresh for every git command, because it expires in 15 minutes.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	region, name, err := CheckPath(repo.FullName)
	if err != nil {
		return nil, err
	}
	base := c.gitBase(region)
	o := gitws.Options{GitBase: base, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	path := "/v1/repos/" + name
	return gitws.Checkout(ctx, o, base+path, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		if c.Creds == nil {
			return platform.Credential{}, errors.New("codecommit: no AWS credentials configured")
		}
		cr, err := c.Creds.Retrieve(ctx)
		if err != nil {
			return platform.Credential{}, fmt.Errorf("codecommit: credentials: %w", err)
		}
		return platform.Credential{Username: GitUsername(cr), Secret: GitPassword(host, path, region, cr.SecretAccessKey, c.now())}, nil
	})
}
