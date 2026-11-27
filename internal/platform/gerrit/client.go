// Package gerrit implements platform.Platform for Gerrit Code Review. A Gerrit
// "pull request" is a change: ripple pushes the update commit to refs/for/<branch>
// with a Change-Id footer and finds the change it created by that id. The vendored
// documentation it was written from is in docs/vendor/gerrit; docs/platform-gerrit.md
// says how to configure it and what is unverified against a real server.
package gerrit

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/sandbox"
)

// xssiPrefix starts every JSON answer (docs/vendor/gerrit/rest-api.md, "Response
// Format"); it is stripped before decoding.
const xssiPrefix = ")]}'"

var (
	// projectName is the conservative subset of Gerrit project names ripple accepts:
	// path-like names, no empty or dot segments.
	projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)
	// refPart is what may appear in a branch or topic inside a push ref, where '%',
	// ',' and '=' would be read as push options.
	refPart   = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	commitSHA = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// Client is the Gerrit platform: one server, one credential.
type Client struct {
	// BaseURL is the server root, e.g. https://gerrit.example.com. The REST API is
	// BaseURL/a/..., the authenticated git URL BaseURL/a/<project>, and a change's
	// page BaseURL/c/<project>/+/<number>.
	BaseURL string
	// Credentials supplies the account name (Username) and its HTTP password or
	// authentication token (Secret): HTTP Basic on the API and on git.
	Credentials platform.Credentials
	// ProjectPrefix, when set, limits discovery to projects whose name starts with it.
	ProjectPrefix string
	HTTP          *http.Client
	Log           *slog.Logger
	// AuthorName and AuthorEmail identify commits.
	AuthorName, AuthorEmail string
	// WorkDir is where checkouts are created; empty means the OS temp dir.
	WorkDir string
	// GoProxy, GoSumDB, GoCacheDir and Sandbox are the shared checkout setup (gitws.Options).
	GoProxy, GoSumDB, GoCacheDir string
	Sandbox                      sandbox.Backend
	// Now is the clock a Retry-After is counted from; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	limited time.Time
	// defaults is each project's default branch as ListRepoHeads last read it; ListRepos
	// reports it, so a moved head can be re-scanned on the right branch.
	defaults map[string]string
}

var (
	_ platform.Platform    = (*Client)(nil)
	_ platform.RateLimiter = (*Client)(nil)
	_ platform.Lister      = (*Client)(nil)
	_ interface {
		ListRepoHeads(context.Context) (map[string]string, error)
	} = (*Client)(nil) // app.HeadLister
	_ platform.PRCloser = (*Client)(nil)
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

func checkProject(name string) error {
	if !projectName.MatchString(name) {
		return fmt.Errorf("refusing unsafe project name %q", name)
	}
	return nil
}

// ChangeID is the Change-Id footer for the change that proposes branch: "I" and 40
// hex digits (docs/vendor/gerrit/user-changeid.md). It is derived from the branch,
// so pushing the branch again is a new patch set of the same change, not a second one.
func ChangeID(branch string) string {
	sum := sha1.Sum([]byte("ripple-change-id\x00" + branch))
	return "I" + hex.EncodeToString(sum[:])
}

// changeHandle is "project~branch~Change-Id", the change identifier that needs no
// number (rest-api-changes.md, "Change Endpoints"), as a path segment.
func changeHandle(project, base, changeID string) string {
	return url.PathEscape(project) + "~" + url.PathEscape(base) + "~" + changeID
}

// RateLimitedUntil is when the last 429 asked us to retry. The quota is per account
// and ripple holds one, so it is one time.
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

func (c *Client) apiURL(elems ...string) string {
	return strings.TrimRight(c.BaseURL, "/") + "/a/" + strings.Join(elems, "/")
}

func (c *Client) do(ctx context.Context, method, rawURL string, in, out any) error {
	cred, err := c.Credentials.Credential(ctx, model.Repo{})
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
	req.SetBasicAuth(cred.Username, cred.Secret)
	if in != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("gerrit %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		until := retryAt(resp, c.now())
		c.noteLimit(until)
		return &platform.RateLimitError{Until: until}
	}
	if resp.StatusCode >= 300 {
		return &statusError{Method: method, Path: req.URL.Path, Status: resp.StatusCode, Body: snippet(data)}
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(data), []byte(xssiPrefix)))
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("gerrit %s %s: decode: %w", method, req.URL.Path, err)
		}
	}
	return nil
}

type statusError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("gerrit %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// retryAt: rest-api.md ("429 Too Many Requests") says only that clients should back
// off exponentially and documents no Retry-After, so the header is honoured when
// present and a minute assumed when not.
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

type changeJSON struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Branch  string `json:"branch"`
	Topic   string `json:"topic"`
	Number  int    `json:"_number"`
	WIP     bool   `json:"work_in_progress"`
	// CurrentRevision is the current patch set's commit id; set only when the query
	// asked for o=CURRENT_REVISION (rest-api-changes.md).
	CurrentRevision string `json:"current_revision"`
	MoreChange      bool   `json:"_more_changes"`
}

func (c *Client) pr(ch changeJSON) platform.PR {
	return platform.PR{Number: ch.Number, Head: ch.Topic, ID: ch.ID, HeadSHA: ch.CurrentRevision,
		URL: fmt.Sprintf("%s/c/%s/+/%d", strings.TrimRight(c.BaseURL, "/"), ch.Project, ch.Number)}
}

const pageSize = 100

// ListOpenPRs lists the open changes of the project whose topic (the branch name
// ripple pushed them under) starts with prefix. The query filters on project and
// status only; the topic is matched here, so no query operator beyond the documented
// status: and project: ones is relied on.
func (c *Client) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]platform.PR, error) {
	if err := checkProject(repo.FullName); err != nil {
		return nil, err
	}
	var out []platform.PR
	for skip, pages := 0, 0; ; pages++ {
		if pages >= 50 {
			return nil, fmt.Errorf("more than %d changes open in %s", 50*pageSize, repo.FullName)
		}
		q := url.Values{"q": {"status:open project:" + repo.FullName}, "n": {strconv.Itoa(pageSize)}, "S": {strconv.Itoa(skip)}, "o": {"CURRENT_REVISION"}}
		var changes []changeJSON
		if err := c.do(ctx, http.MethodGet, c.apiURL("changes/")+"?"+q.Encode(), nil, &changes); err != nil {
			return nil, err
		}
		for _, ch := range changes {
			if ch.Topic != "" && strings.HasPrefix(ch.Topic, prefix) {
				out = append(out, c.pr(ch))
			}
		}
		if len(changes) == 0 || !changes[len(changes)-1].MoreChange {
			return out, nil
		}
		skip += len(changes)
	}
}

// CreatePR finishes the proposal the push of spec.Head began: pushing to refs/for
// already created the change, so this looks it up by its Change-Id and then marks it
// work in progress for a draft, posts spec.Body as its first message (the commit
// message is only the title), and adds spec.Labels as hashtags (best effort).
func (c *Client) CreatePR(ctx context.Context, repo model.Repo, s platform.PRSpec) (platform.PR, error) {
	if err := checkProject(repo.FullName); err != nil {
		return platform.PR{}, err
	}
	handle := changeHandle(repo.FullName, s.Base, ChangeID(s.Head))
	var ch changeJSON
	if err := c.do(ctx, http.MethodGet, c.apiURL("changes", handle), nil, &ch); err != nil {
		return platform.PR{}, fmt.Errorf("finding the change pushed for %s: %w", s.Head, err)
	}
	change := c.apiURL("changes", url.PathEscape(strconv.Itoa(ch.Number)))
	if s.Draft && !ch.WIP {
		if err := c.do(ctx, http.MethodPost, change+"/wip", map[string]any{}, nil); err != nil {
			return platform.PR{}, err
		}
	}
	review := map[string]any{"message": s.Body, "tag": "autogenerated:ripple"}
	if err := c.do(ctx, http.MethodPost, change+"/revisions/current/review", review, nil); err != nil {
		return platform.PR{}, err
	}
	if len(s.Labels) > 0 {
		err := c.do(ctx, http.MethodPost, change+"/hashtags", map[string]any{"add": s.Labels}, nil)
		var rl *platform.RateLimitError
		if asRateLimit(err, &rl) {
			return platform.PR{}, err
		}
		if err != nil {
			c.log().Warn("gerrit hashtags not set", "repo", repo.FullName, "change", ch.Number, "err", err)
		}
	}
	p := c.pr(ch)
	p.Head = s.Head
	return p, nil
}

// closeDetail is the slice of ChangeInfo that decides whether a person touched a
// change (rest-api-changes.md, "ChangeInfo", "RevisionInfo", "CommitInfo",
// "ChangeMessageInfo", "LabelInfo", "ApprovalInfo").
type closeDetail struct {
	Project   string `json:"project"`
	Status    string `json:"status"`
	Topic     string `json:"topic"`
	Revisions map[string]struct {
		Commit struct {
			Author    struct{ Email string } `json:"author"`
			Committer struct{ Email string } `json:"committer"`
		} `json:"commit"`
	} `json:"revisions"`
	Labels map[string]struct {
		All []struct {
			Value int `json:"value"`
		} `json:"all"`
	} `json:"labels"`
	Messages []struct {
		Author *struct{} `json:"author"`
		Tag    string    `json:"tag"`
	} `json:"messages"`
}

// CloseUntouchedPR abandons the change unless a person touched it. Touched means:
// a patch set whose commit author or committer is not ripple's identity (a person
// uploaded or edited one); a non-zero vote on any label; a change message written
// by a person (one with an author and no "autogenerated:" tag: Gerrit leaves the
// author of its own messages unset, and tags its and tools' automatic ones, which is
// the only marking the API gives, so ripple's own messages and CI bots that use it are
// ignored); or a published inline comment. Every read must succeed. Not detected: a
// patch set that a person uploaded under ripple's commit identity, a reviewer added
// without a message or vote, a hashtag or attention-set change, or a topic change
// (which makes the change a different proposal, so nothing is closed).
//
// The abandon carries the comment as its message (AbandonInput.message), so the
// close and the comment are one request: a failed one leaves neither and a retry
// repeats nothing. It reads (docs/vendor/gerrit/rest-api-changes.md):
//   - GET /changes/{number}?o=ALL_REVISIONS&o=ALL_COMMITS&o=DETAILED_LABELS&o=MESSAGES ("Query Changes")
//   - GET /changes/{number}/comments ("List Change Comments")
//   - POST /changes/{number}/abandon ("Abandon Change")
func (c *Client) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr platform.PR, comment string) (bool, error) {
	if err := checkProject(repo.FullName); err != nil {
		return false, err
	}
	change := c.apiURL("changes", url.PathEscape(strconv.Itoa(pr.Number)))
	q := url.Values{"o": {"ALL_REVISIONS", "ALL_COMMITS", "DETAILED_LABELS", "MESSAGES"}}
	var d closeDetail
	if err := c.do(ctx, http.MethodGet, change+"?"+q.Encode(), nil, &d); err != nil {
		return false, err
	}
	if d.Status != "NEW" || d.Topic != pr.Head || d.Project != repo.FullName {
		return false, nil
	}
	if len(d.Revisions) == 0 {
		return false, fmt.Errorf("gerrit change %d: the answer lists no patch sets", pr.Number)
	}
	ours := gitws.AuthorEmail(gitws.Options{AuthorEmail: c.AuthorEmail})
	for _, rev := range d.Revisions {
		if rev.Commit.Author.Email != ours || rev.Commit.Committer.Email != ours {
			return false, nil
		}
	}
	for _, l := range d.Labels {
		for _, a := range l.All {
			if a.Value != 0 {
				return false, nil
			}
		}
	}
	for _, m := range d.Messages {
		if m.Author != nil && !strings.HasPrefix(m.Tag, "autogenerated:") {
			return false, nil
		}
	}
	var inline map[string][]json.RawMessage
	if err := c.do(ctx, http.MethodGet, change+"/comments", nil, &inline); err != nil {
		return false, err
	}
	for _, cs := range inline {
		if len(cs) > 0 {
			return false, nil
		}
	}
	if err := c.do(ctx, http.MethodPost, change+"/abandon", map[string]any{"message": comment}, nil); err != nil {
		return false, err
	}
	return true, nil
}

func asRateLimit(err error, target **platform.RateLimitError) bool {
	rl, ok := err.(*platform.RateLimitError)
	if ok {
		*target = rl
	}
	return ok
}

type projectJSON struct {
	More bool `json:"_more_projects"`
}

// ListRepos is every active code project the account can see (optionally under
// ProjectPrefix). Gerrit's webhooks plugin can announce a new project, but listing
// is what catches projects whose hook was never configured, so the app lists. The
// default branch is left empty: "Get HEAD" is one call per project, and the first scan
// learns it from the clone (engine.Scan).
func (c *Client) ListRepos(ctx context.Context) ([]model.Repo, error) {
	repos, err := c.listProjects(ctx, "ACTIVE")
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range repos {
		repos[i].DefaultBranch = c.defaults[repos[i].FullName]
	}
	return repos, err
}

// ListRepoHeads is the commit each active project's default branch points at, for the app's
// head comparison (app.HeadLister). The project listing carries no commit, so it costs two reads
// per project: "Get HEAD" for the branch name and "Get Branch" for its commit
// (docs/vendor/gerrit/rest-api-projects.md), one project at a time. A 429 ends the listing with
// the rate-limit error (the app then keeps what it remembered); any other failure leaves that
// project out of the map. The default branch this call reads is remembered for ListRepos, and the
// app lists once more in the same round to get it, so the first round after a start already
// catches a head that moved while ripple was down.
func (c *Client) ListRepoHeads(ctx context.Context) (map[string]string, error) {
	repos, err := c.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, r := range repos {
		var head string
		if err := c.do(ctx, http.MethodGet, c.apiURL("projects", url.PathEscape(r.FullName), "HEAD"), nil, &head); err != nil {
			var rl *platform.RateLimitError
			if asRateLimit(err, &rl) {
				return nil, err
			}
			continue
		}
		branch := strings.TrimPrefix(head, "refs/heads/")
		if !refPart.MatchString(branch) {
			continue
		}
		var bi struct {
			Revision string `json:"revision"`
		}
		if err := c.do(ctx, http.MethodGet, c.apiURL("projects", url.PathEscape(r.FullName), "branches", url.PathEscape("refs/heads/"+branch)), nil, &bi); err != nil {
			var rl *platform.RateLimitError
			if asRateLimit(err, &rl) {
				return nil, err
			}
			continue
		}
		c.mu.Lock()
		if c.defaults == nil {
			c.defaults = map[string]string{}
		}
		c.defaults[r.FullName] = branch
		c.mu.Unlock()
		if commitSHA.MatchString(bi.Revision) {
			heads[r.FullName] = bi.Revision
		}
	}
	return heads, nil
}

// ListArchivedRepos is the READ_ONLY code projects, the closest Gerrit has to an archive
// (`GET /projects/?state=READ_ONLY`, docs/vendor/gerrit/rest-api-projects.md "State(s)"; a
// project in that state accepts no pushes or new changes). Gerrit sends no event for a
// state change, so the listing is what tells a read-only project from a deleted one, and
// ripple pauses it rather than forgetting it. HIDDEN projects are in neither listing.
func (c *Client) ListArchivedRepos(ctx context.Context) ([]model.Repo, error) {
	return c.listProjects(ctx, "READ_ONLY")
}

func (c *Client) listProjects(ctx context.Context, state string) ([]model.Repo, error) {
	var names []string
	for skip, pages := 0, 0; ; pages++ {
		if pages >= 100 {
			return nil, fmt.Errorf("more than %d projects", 100*pageSize)
		}
		q := url.Values{"type": {"CODE"}, "state": {state}, "n": {strconv.Itoa(pageSize)}, "S": {strconv.Itoa(skip)}}
		if c.ProjectPrefix != "" {
			q.Set("p", c.ProjectPrefix)
		}
		var page map[string]projectJSON
		if err := c.do(ctx, http.MethodGet, c.apiURL("projects/")+"?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		more := false
		for n, p := range page {
			names = append(names, n)
			more = more || p.More
		}
		if !more || len(page) == 0 {
			break
		}
		skip += len(page)
	}
	sort.Strings(names)
	var out []model.Repo
	for _, n := range names {
		if checkProject(n) == nil {
			out = append(out, model.Repo{FullName: n})
		}
	}
	return out, nil
}

type commitJSON struct {
	Parents []struct {
		Commit string `json:"commit"`
	} `json:"parents"`
}

type fileInfo struct {
	OldPath string `json:"old_path"`
}

const maxPushCommits = 20

// PushFiles lists the paths changed by the commits in oldRev..newRev on a branch of
// project. A ref-updated event names the two revisions and no files
// (docs/vendor/gerrit/json.md, "refUpdate"), so this walks first parents from newRev
// to oldRev and lists each commit's files. It returns nil, which makes the app
// re-scan (always correct), for a new branch, a merge commit, a history that does
// not reach oldRev within maxPushCommits, or any error.
func (c *Client) PushFiles(ctx context.Context, project, oldRev, newRev string) ([]string, error) {
	if err := checkProject(project); err != nil {
		return nil, err
	}
	if !commitSHA.MatchString(newRev) || !commitSHA.MatchString(oldRev) || strings.Trim(oldRev, "0") == "" {
		return nil, nil // a zero oldRev is a new branch
	}
	seen := map[string]bool{}
	var files []string
	cur := newRev
	for i := 0; i < maxPushCommits; i++ {
		if cur == oldRev {
			return files, nil
		}
		base := c.apiURL("projects", url.PathEscape(project), "commits", cur)
		var cm commitJSON
		if err := c.do(ctx, http.MethodGet, base, nil, &cm); err != nil {
			return nil, err
		}
		if len(cm.Parents) != 1 {
			return nil, nil
		}
		var fi map[string]fileInfo
		if err := c.do(ctx, http.MethodGet, base+"/files/", nil, &fi); err != nil {
			return nil, err
		}
		for p, f := range fi {
			for _, path := range []string{p, f.OldPath} {
				if path != "" && path != "/COMMIT_MSG" && path != "/MERGE_LIST" && !seen[path] {
					seen[path] = true
					files = append(files, path)
				}
			}
		}
		cur = cm.Parents[0].Commit
	}
	return nil, nil
}

// proposal is the gitws push shape: HEAD goes to refs/for/<base> with the branch as
// its topic, and its commit message ends with the branch's Change-Id.
var proposal = &gitws.Proposal{
	Ref: func(branch, base string) (string, error) {
		if !refPart.MatchString(branch) || !refPart.MatchString(base) {
			return "", fmt.Errorf("refusing to push %q for %q: characters Gerrit would read as push options", branch, base)
		}
		return "refs/for/" + base + "%topic=" + branch, nil
	},
	Trailer: func(branch string) string { return "Change-Id: " + ChangeID(branch) },
}

// Checkout shallow-clones the project's default branch over BaseURL/a/<project>,
// the credential sent per network git command as basic auth.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	if err := checkProject(repo.FullName); err != nil {
		return nil, err
	}
	o := gitws.Options{GitBase: c.BaseURL, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, WorkDir: c.WorkDir,
		GoProxy: c.GoProxy, GoSumDB: c.GoSumDB, GoCacheDir: c.GoCacheDir, Sandbox: c.Sandbox, Proposal: proposal}
	cloneURL := strings.TrimRight(c.BaseURL, "/") + "/a/" + repo.FullName
	ws, err := gitws.Checkout(ctx, o, cloneURL, repo.DefaultBranch, func(ctx context.Context) (platform.Credential, error) {
		return c.Credentials.Credential(ctx, repo)
	})
	if err != nil {
		return nil, err
	}
	return &forgeHintWorkspace{Workspace: ws, email: c.AuthorEmail}, nil
}

// forgeHintWorkspace explains Gerrit's author-email refusal: the push error names the missing "forge author"
// permission but not the settings that fix it (ROADMAP 291).
type forgeHintWorkspace struct {
	platform.Workspace
	email string
}

func (w *forgeHintWorkspace) CommitAndPush(ctx context.Context, branch, message string) (bool, error) {
	pushed, err := w.Workspace.CommitAndPush(ctx, branch, message)
	if err != nil && (strings.Contains(strings.ToLower(err.Error()), "forge author") || strings.Contains(strings.ToLower(err.Error()), "forge committer")) {
		email := w.email
		if email == "" {
			email = gitws.AuthorEmail(gitws.Options{})
		}
		err = fmt.Errorf("%w\nhint: Gerrit does not accept the commit email %q for this account: grant it Forge Author and Forge Committer on the branch, or register an address on the account (Settings, Email Addresses) and set RIPPLE_GERRIT_AUTHOR_EMAIL to it (docs/platform-gerrit.md, \"Author email\")", err, email)
	}
	return pushed, err
}
