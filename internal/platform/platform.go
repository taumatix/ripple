// Package platform abstracts the code host: clone, edit, push, open PRs, and how a
// host tells ripple which repositories to watch. docs/platform-authoring.md says how
// to add one.
package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// PR is an open pull request (merge request, change) as the engine sees it.
type PR struct {
	Number int
	URL    string
	Head   string
	// ID is the host's own handle for the PR when its API needs one beyond Number
	// (GitHub: the GraphQL node id that auto-merge takes). Only the platform that
	// returned the PR reads it.
	ID string
}

type PRSpec struct {
	Head, Base, Title, Body string
	// Draft opens the PR as a draft; a draft is never auto-merged.
	Draft bool
	// Labels are best effort: a failure to label does not fail the PR.
	Labels []string
}

// RateLimitError means the platform asked us to back off until Until.
type RateLimitError struct{ Until time.Time }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited until %s", e.Until.Format(time.RFC3339))
}

var ErrNotExist = errors.New("file does not exist")

// ErrNoToolchain means the go command is not available to RunGo.
var ErrNoToolchain = errors.New("go toolchain not available")

// ErrUnsafeCheckout means RunGo refused to run: the checkout holds a symlinked go
// file or a local replace target outside the repository, and go would read or write
// through it. It is permanent for that commit.
var ErrUnsafeCheckout = errors.New("unsafe checkout")

type workspaceModeKey struct{}

// WithWorkspaceMode returns a context for RunGo that lets the go command use the
// go.work file above dir. Without it RunGo runs every command with the workspace
// off, so each module is judged on its own go.mod and go.sum.
func WithWorkspaceMode(ctx context.Context) context.Context {
	return context.WithValue(ctx, workspaceModeKey{}, true)
}

// InWorkspaceMode reports whether ctx came from WithWorkspaceMode.
func InWorkspaceMode(ctx context.Context) bool {
	on, _ := ctx.Value(workspaceModeKey{}).(bool)
	return on
}

type scanOnlyKey struct{}

// WithScanOnly marks ctx so the Checkout it reaches may return a workspace that is
// only good for reading: a partial clone with no working tree, whose files arrive
// as they are read (Walk lists them from the tree, Prefetch fetches the ones a scan
// will read in one request). Such a workspace refuses writes, commits and go
// commands. A platform that cannot do that ignores the mark and returns a full
// checkout, so a caller must not rely on getting the light one.
func WithScanOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, scanOnlyKey{}, true)
}

// ScanOnly reports whether ctx came from WithScanOnly.
func ScanOnly(ctx context.Context) bool {
	on, _ := ctx.Value(scanOnlyKey{}).(bool)
	return on
}

// ErrScanFallback is wrapped by errors a scan-only workspace returns when the host
// could not serve it (no partial clone, a refused fetch). The caller repeats the
// scan on a full checkout instead of failing.
var ErrScanFallback = errors.New("scan-only checkout unavailable")

// Prefetcher is a Workspace that can load many files in one request ahead of
// ReadFile. Prefetch is only an optimisation for a full checkout, where it does nothing.
type Prefetcher interface {
	Prefetch(ctx context.Context, paths []string) error
}

// Workspace is a shallow checkout of a repo's default branch.
type Workspace interface {
	DefaultBranch() string
	// ReadFile returns ErrNotExist when the path is absent. Paths are
	// slash-separated and relative to the repo root; escapes are refused.
	ReadFile(path string) ([]byte, error)
	// WriteFile creates or overwrites a regular file inside the checkout.
	WriteFile(path string, data []byte) error
	// Walk calls fn for every regular file, skipping .git, vendor and node_modules.
	Walk(fn func(path string) error) error
	// CommitAndPush commits all changes on a new branch and force-pushes it
	// (branches under the ripple/ prefix are ours). false means nothing changed.
	CommitAndPush(ctx context.Context, branch, message string) (bool, error)
	// RunGo runs one of `go mod tidy`, `go mod vendor`, `go build ./...`, `go vet ./...`
	// or (only with WithWorkspaceMode) `go work sync` or `go work vendor` in dir
	// (relative to the repo root) and returns its combined output, trimmed to the
	// tail. Nothing else is allowed: the checkout is untrusted, so the environment is
	// stripped, cgo (unless `cgo: true`) and toolchain downloads are off, and modules come only from the
	// configured proxy. ErrNoToolchain when go is not installed.
	RunGo(ctx context.Context, dir string, args ...string) (string, error)
	// Reset discards local changes and returns to the default branch so the next
	// branch starts from a clean tree.
	Reset(ctx context.Context) error
	Close() error
}

// Platform is what every code host provides. Optional capabilities are separate
// interfaces a platform may also implement: AutoMerger, RateLimiter, Lister and
// LegacyConfig (see host.go). A call that the host refuses for rate limiting
// returns *RateLimitError.
type Platform interface {
	Checkout(ctx context.Context, repo model.Repo) (Workspace, error)
	// ListOpenPRs returns open PRs whose head branch starts with headPrefix.
	ListOpenPRs(ctx context.Context, repo model.Repo, headPrefix string) ([]PR, error)
	CreatePR(ctx context.Context, repo model.Repo, spec PRSpec) (PR, error)
}
