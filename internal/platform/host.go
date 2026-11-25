package platform

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// Credential is what one API call or one network git command authenticates with.
// Git sends it as HTTP basic auth (Username:Secret); an API client uses Secret in
// whatever header its host expects. An empty Secret means anonymous.
type Credential struct {
	// Username is the basic-auth user git sends: "x-access-token" on GitHub,
	// "oauth2" on GitLab, the account name where a host wants one.
	Username string
	Secret   string
	// Scheme is how Secret is sent: "" for HTTP Basic (Username:Secret), SchemeBearer
	// for "Authorization: Bearer <Secret>" (a Microsoft Entra access token on Azure
	// DevOps). A host that knows no other scheme ignores it.
	Scheme string
}

// SchemeBearer is the Credential.Scheme that sends Secret as a bearer token.
const SchemeBearer = "bearer"

// Credentials is a host's credential source. It is asked for each request, so a
// source may mint, cache and rotate short-lived tokens; repo selects the scope on
// hosts where that matters (a GitHub App installation, a GitLab group token).
type Credentials interface {
	Credential(ctx context.Context, repo model.Repo) (Credential, error)
}

// StaticCredential is a Credentials that always returns itself: a personal access
// token, an app password, a project token.
type StaticCredential Credential

func (s StaticCredential) Credential(context.Context, model.Repo) (Credential, error) {
	return Credential(s), nil
}

// ErrNoAutoMerge means the host cannot be asked to merge a pull request once its
// checks pass. The pull request stays open for a human; it is not a failure.
var ErrNoAutoMerge = errors.New("host has no auto-merge")

// AutoMerger is the optional capability to ask the host to merge a pull request
// when its required checks pass (GitHub auto-merge, GitLab merge when pipeline
// succeeds, Azure DevOps auto-complete). A host that has none does not implement it.
type AutoMerger interface {
	RequestAutoMerge(ctx context.Context, repo model.Repo, pr PR) error
}

// RequestAutoMerge asks p to auto-merge pr, or returns ErrNoAutoMerge when p cannot.
func RequestAutoMerge(ctx context.Context, p Platform, repo model.Repo, pr PR) error {
	am, ok := p.(AutoMerger)
	if !ok {
		return ErrNoAutoMerge
	}
	return am.RequestAutoMerge(ctx, repo, pr)
}

// ErrNoClosePR means the host cannot close a pull request through ripple, so a
// superseded PR stays open for a human. It is not a failure.
var ErrNoClosePR = errors.New("host cannot close pull requests")

// PRCloser is the optional capability to close a stale ripple PR once a newer one
// replaces it. The host, not the engine, decides whether the PR is untouched,
// because what counts as a human touching it (commits, review comments, a rename)
// is host-specific. A host that cannot answer that reliably does not implement it.
type PRCloser interface {
	// CloseUntouchedPR closes pr and leaves comment on it, but only when nobody but
	// ripple has touched it: every commit on its branch is ripple's own, and it has
	// no reviews, comments, rename, edited description or rewritten branch that the
	// host can report. It returns false, nil, having changed nothing, when someone
	// has, or when pr is no longer open or its branch is no longer the one given. It
	// never closes on doubt: any failed check is an error and leaves the PR open. It
	// closes before it comments and a failed comment is only logged, so a retry
	// after a failed close never repeats a comment. It does not delete the branch.
	CloseUntouchedPR(ctx context.Context, repo model.Repo, pr PR, comment string) (bool, error)
}

// CloseUntouchedPR is p's CloseUntouchedPR, or ErrNoClosePR when p has none.
func CloseUntouchedPR(ctx context.Context, p Platform, repo model.Repo, pr PR, comment string) (bool, error) {
	c, ok := p.(PRCloser)
	if !ok {
		return false, ErrNoClosePR
	}
	return c.CloseUntouchedPR(ctx, repo, pr, comment)
}

// RateLimiter is the optional per-platform rate-limit signal: the time before which
// the host has asked not to be called for repo (the zero time when it has not). The
// engine checks it before spending a call, so one limited host or credential scope
// holds back only its own repos. A platform that returns *RateLimitError from its
// calls should also implement this.
type RateLimiter interface {
	RateLimitedUntil(repo model.Repo) time.Time
}

// RateLimitedUntil is p's signal for repo, or the zero time when p gives none.
func RateLimitedUntil(p Platform, repo model.Repo) time.Time {
	if rl, ok := p.(RateLimiter); ok {
		return rl.RateLimitedUntil(repo)
	}
	return time.Time{}
}

// LegacyConfig is implemented by the one host that still reads the deprecated
// per-repo file alias (config.LegacyFileName, .github/ripple.yml). Every other host
// reads only .ripple.yaml at the repository root.
type LegacyConfig interface {
	ReadsLegacyConfig(repo model.Repo) bool
}

// ReadsLegacyConfig reports whether p reads the deprecated alias for repo.
func ReadsLegacyConfig(p Platform, repo model.Repo) bool {
	lc, ok := p.(LegacyConfig)
	return ok && lc.ReadsLegacyConfig(repo)
}

// RepoIdentifier is the optional capability of asking the host whether an immutable
// repository id currently belongs to repo. The app uses it on a signed push whose id is
// not the one stored for the name: a repository deleted and recreated under the same
// name has a new id, and only the host can say so. found is false (with a nil error)
// when the host does not know the id or it names another repository.
type RepoIdentifier interface {
	RepoHasID(ctx context.Context, repo model.Repo, id int64) (found bool, err error)
}

// ErrNoRepoIdentity is what RepoHasID answers for a host without the capability.
var ErrNoRepoIdentity = errors.New("platform: host cannot resolve repository ids")

// RepoHasID asks p whether id names repo, or ErrNoRepoIdentity when p cannot say.
func RepoHasID(ctx context.Context, p Platform, repo model.Repo, id int64) (bool, error) {
	ri, ok := p.(RepoIdentifier)
	if !ok {
		return false, ErrNoRepoIdentity
	}
	return ri.RepoHasID(ctx, repo, id)
}

// EventKind is what a host told ripple about a repository.
type EventKind string

const (
	// ReposAdded: start watching the repos (an app installed, a project hook added,
	// a repo that appeared in a periodic listing).
	ReposAdded EventKind = "repos_added"
	// ReposRemoved: forget the repos.
	ReposRemoved EventKind = "repos_removed"
	// Push: the default branch may have changed; Files lists the changed paths.
	Push EventKind = "push"
	// RepoRenamed: the repo moved to NewRepo (a rename or a transfer); Repos holds
	// the old name. Optional: only a host that reports renames sends it.
	RepoRenamed EventKind = "repo_renamed"
	// DefaultBranchChanged: the repo's default branch is now DefaultBranch. Optional,
	// like RepoRenamed. Existing PRs are left alone; the repo is re-scanned.
	DefaultBranchChanged EventKind = "default_branch_changed"
	// RepoArchived: the repo is read-only. Optional, like RepoRenamed. ripple keeps its
	// index and pending jobs and acts on neither until RepoUnarchived; a host that does
	// not send it sends ReposRemoved and loses both.
	RepoArchived EventKind = "repo_archived"
	// RepoUnarchived: the repo is writable again; the paused jobs resume. A repo ripple no
	// longer holds is registered (scanned) as ReposAdded would.
	RepoUnarchived EventKind = "repo_unarchived"
)

// Event is one host notification in the host-neutral form the event queue stores.
// Repo names are the host's own (owner/repo, group/sub/project); the app prefixes
// them with the host name before they reach the store.
type Event struct {
	Kind EventKind
	// Scope is the credential scope the event arrived under (a GitHub App
	// installation id); the queue shares workers fairly between scopes. Zero is a
	// valid scope for hosts that have one credential.
	Scope int64
	// Delivery is the host's delivery id, empty when it has none. It may be unsigned.
	Delivery string
	// BodyKey identifies the verified body itself (webhook.SignatureKey), empty unless the
	// receiver was told to treat a repeated signed body as a repeat. The app claims it in
	// the store's delivery table beside Delivery, so a body replayed under a new id is
	// dropped by whichever replica it reaches.
	BodyKey string
	Repos   []string
	// Push only.
	Ref           string
	DefaultBranch string
	Files         []string
	// RepoRenamed only: the new native repo name.
	NewRepo string
	// RepoID is the host's immutable id for the repository, 0 when the host provides
	// none. A host sets it only when it vouches for it (a verified delivery): ripple
	// registers an unknown repo from a push only when it is non-zero.
	RepoID int64
	// RepoIDs is, for ReposAdded, the vouched id of each of Repos at the same index
	// (0 for one the payload gave none); nil when the host gives none.
	RepoIDs []int64
}

// Sink consumes events. An error makes the receiver answer 503, which tells a host
// that redelivers to try again.
type Sink interface {
	HandleEvent(ctx context.Context, e Event) error
}

// Lister is the optional discovery by periodic listing, for hosts with no install
// event: every repo the credential can see that ripple should watch. The app
// compares each listing with the previous one and queues ReposAdded and
// ReposRemoved for the difference.
type Lister interface {
	ListRepos(ctx context.Context) ([]model.Repo, error)
}

// ArchivedLister is the optional second half of a Lister: the repos the host lists
// as archived, which ListRepos leaves out. With it, the app reads a repo that moves
// between the two listings as RepoArchived or RepoUnarchived instead of removed or
// added, so an archive keeps the repo's index and pending jobs. An error leaves the
// previous listings in place, so a failing archived listing is never read as an
// unarchive. Only names matter; a repo ripple would not watch (empty, no push
// permission) must be left out here as in ListRepos.
type ArchivedLister interface {
	ListArchivedRepos(ctx context.Context) ([]model.Repo, error)
}

// Host is everything one code host contributes, as registered with the app.
type Host struct {
	// Name is lower-case letters, digits and dashes. It prefixes the host's repo
	// names in the store ("gitlab:group/project") unless the host is the default,
	// and the receiver is served at /webhook/<Name>.
	Name     string
	Platform Platform
	// Receiver builds the webhook handler that turns the host's deliveries into
	// Events for sink. It authenticates every delivery before reading more than
	// its headers. Nil when the host has no webhooks (it must then be a Lister).
	Receiver func(sink Sink) http.Handler
}

// ErrNoIssues means the host has no issue tracker ripple can write to, so there is
// no dependency dashboard. It is not a failure.
var ErrNoIssues = errors.New("host has no issues")

// ErrIssuesUnavailable means the host has issues but not for this repo: they are
// disabled, or the credential may not write them (GitHub: the App lacks the
// "Issues" permission). The caller logs it once and carries on.
var ErrIssuesUnavailable = errors.New("issues are disabled or not writable for this repository")

// ErrIssueGone means the issue number asked for no longer exists (deleted).
var ErrIssueGone = errors.New("issue no longer exists")

// Issue is an issue as the dependency dashboard sees it.
type Issue struct {
	Number int
	Body   string
	// Closed is whether a person closed it. The dashboard keeps updating a closed
	// issue's body and never reopens it.
	Closed bool
}

// IssueSpec is a new issue.
type IssueSpec struct {
	Title, Body string
	// Labels are applied when the issue is created.
	Labels []string
}

// IssueTracker is the optional capability to keep one ripple-owned issue per repo
// (the dependency dashboard). A host that has no issues, or cannot be written to
// reliably, does not implement it. Each method returns ErrIssuesUnavailable when
// the repo's issues are disabled or the credential may not write them.
type IssueTracker interface {
	// FindIssue returns the issue carrying label whose body starts with marker, closed
	// ones included; false when there is none. A person's issue that merely has the
	// label is not ripple's and is not returned.
	FindIssue(ctx context.Context, repo model.Repo, label, marker string) (Issue, bool, error)
	CreateIssue(ctx context.Context, repo model.Repo, spec IssueSpec) (Issue, error)
	// UpdateIssueBody replaces the body; ErrIssueGone when the issue is not there.
	// It changes nothing else (state, title, labels).
	UpdateIssueBody(ctx context.Context, repo model.Repo, number int, body string) error
}

// IssueCloser is the optional capability to close an issue ripple opened, used when a
// repo turns its dependency dashboard off. It is separate from IssueTracker so a host
// that can keep the dashboard but not close it stays valid.
type IssueCloser interface {
	// CloseIssue closes the issue and changes nothing else. Closing a closed issue
	// is not an error; ErrIssueGone when the issue is not there.
	CloseIssue(ctx context.Context, repo model.Repo, number int) error
}

// IssueReopener is the optional capability to reopen an issue ripple closed itself, used
// when a repo turns its dependency dashboard back on. A host without it keeps updating
// the closed issue, as it does for one a person closed.
type IssueReopener interface {
	// ReopenIssue reopens the issue and changes nothing else. Reopening an open issue
	// is not an error; ErrIssueGone when the issue is not there.
	ReopenIssue(ctx context.Context, repo model.Repo, number int) error
}

// ReopenIssue is p's ReopenIssue, or ErrNoIssues when p cannot reopen issues.
func ReopenIssue(ctx context.Context, p Platform, repo model.Repo, number int) error {
	r, ok := p.(IssueReopener)
	if !ok {
		return ErrNoIssues
	}
	return r.ReopenIssue(ctx, repo, number)
}

// CloseIssue is p's CloseIssue, or ErrNoIssues when p cannot close issues.
func CloseIssue(ctx context.Context, p Platform, repo model.Repo, number int) error {
	c, ok := p.(IssueCloser)
	if !ok {
		return ErrNoIssues
	}
	return c.CloseIssue(ctx, repo, number)
}

// FindIssue is p's FindIssue, or ErrNoIssues.
func FindIssue(ctx context.Context, p Platform, repo model.Repo, label, marker string) (Issue, bool, error) {
	t, ok := p.(IssueTracker)
	if !ok {
		return Issue{}, false, ErrNoIssues
	}
	return t.FindIssue(ctx, repo, label, marker)
}

// CreateIssue is p's CreateIssue, or ErrNoIssues.
func CreateIssue(ctx context.Context, p Platform, repo model.Repo, spec IssueSpec) (Issue, error) {
	t, ok := p.(IssueTracker)
	if !ok {
		return Issue{}, ErrNoIssues
	}
	return t.CreateIssue(ctx, repo, spec)
}

// UpdateIssueBody is p's UpdateIssueBody, or ErrNoIssues.
func UpdateIssueBody(ctx context.Context, p Platform, repo model.Repo, number int, body string) error {
	t, ok := p.(IssueTracker)
	if !ok {
		return ErrNoIssues
	}
	return t.UpdateIssueBody(ctx, repo, number, body)
}
