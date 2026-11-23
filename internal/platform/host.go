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
}

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
	// CloseUntouchedPR leaves comment on pr and closes it, but only when nobody but
	// ripple has touched it: every commit on its branch is ripple's own, and it has
	// no reviews, comments or rename. It returns false, nil, having changed nothing,
	// when someone has. It never closes on doubt: any failed check is an error and
	// leaves the PR open. It does not delete the branch.
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
	Repos    []string
	// Push only.
	Ref           string
	DefaultBranch string
	Files         []string
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
