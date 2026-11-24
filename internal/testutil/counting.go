package testutil

import (
	"context"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// CountingPlatform records every Platform call per repo, so tests can assert a
// repo that does not import a package was never touched.
type CountingPlatform struct {
	Inner platform.Platform

	mu    sync.Mutex
	calls map[string]int
	held  map[string]chan struct{}
}

// Hold makes every Checkout of repo block until Release (or its context ends), so a
// test can keep a scan or a job busy for as long as it needs.
func (c *CountingPlatform) Hold(repo string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil {
		c.held = map[string]chan struct{}{}
	}
	if c.held[repo] == nil {
		c.held[repo] = make(chan struct{})
	}
}

// Release unblocks the checkouts of repo held by Hold.
func (c *CountingPlatform) Release(repo string) {
	c.mu.Lock()
	ch := c.held[repo]
	delete(c.held, repo)
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (c *CountingPlatform) hit(repo string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[repo]++
}

// Calls is the number of Platform calls made for repo.
func (c *CountingPlatform) Calls(repo string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[repo]
}

func (c *CountingPlatform) Checkout(ctx context.Context, r model.Repo) (platform.Workspace, error) {
	c.hit(r.FullName)
	c.mu.Lock()
	ch := c.held[r.FullName]
	c.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Inner.Checkout(ctx, r)
}

func (c *CountingPlatform) ListOpenPRs(ctx context.Context, r model.Repo, p string) ([]platform.PR, error) {
	c.hit(r.FullName)
	return c.Inner.ListOpenPRs(ctx, r, p)
}

func (c *CountingPlatform) CreatePR(ctx context.Context, r model.Repo, s platform.PRSpec) (platform.PR, error) {
	c.hit(r.FullName)
	return c.Inner.CreatePR(ctx, r, s)
}

// RequestAutoMerge forwards to Inner, or answers platform.ErrNoAutoMerge (without
// counting a call) when Inner has no auto-merge.
func (c *CountingPlatform) RequestAutoMerge(ctx context.Context, r model.Repo, pr platform.PR) error {
	if _, ok := c.Inner.(platform.AutoMerger); !ok {
		return platform.ErrNoAutoMerge
	}
	c.hit(r.FullName)
	return platform.RequestAutoMerge(ctx, c.Inner, r, pr)
}

// CloseUntouchedPR forwards to Inner, or answers platform.ErrNoClosePR (without
// counting a call) when Inner cannot close pull requests.
func (c *CountingPlatform) CloseUntouchedPR(ctx context.Context, r model.Repo, pr platform.PR, comment string) (bool, error) {
	if _, ok := c.Inner.(platform.PRCloser); !ok {
		return false, platform.ErrNoClosePR
	}
	c.hit(r.FullName)
	return platform.CloseUntouchedPR(ctx, c.Inner, r, pr, comment)
}

// RateLimitedUntil and ReadsLegacyConfig pass Inner's answers through; they make no call.
func (c *CountingPlatform) RateLimitedUntil(r model.Repo) time.Time {
	return platform.RateLimitedUntil(c.Inner, r)
}

func (c *CountingPlatform) ReadsLegacyConfig(r model.Repo) bool {
	return platform.ReadsLegacyConfig(c.Inner, r)
}

// RepoHasID forwards to Inner and counts the call against repo.
func (c *CountingPlatform) RepoHasID(ctx context.Context, r model.Repo, id int64) (bool, error) {
	c.hit(r.FullName)
	return platform.RepoHasID(ctx, c.Inner, r, id)
}

// FindIssue, CreateIssue and UpdateIssueBody forward to Inner, or answer
// platform.ErrNoIssues (without counting a call) when Inner has no issue tracker.
func (c *CountingPlatform) FindIssue(ctx context.Context, r model.Repo, label, marker string) (platform.Issue, bool, error) {
	if _, ok := c.Inner.(platform.IssueTracker); !ok {
		return platform.Issue{}, false, platform.ErrNoIssues
	}
	c.hit(r.FullName)
	return platform.FindIssue(ctx, c.Inner, r, label, marker)
}

func (c *CountingPlatform) CreateIssue(ctx context.Context, r model.Repo, spec platform.IssueSpec) (platform.Issue, error) {
	if _, ok := c.Inner.(platform.IssueTracker); !ok {
		return platform.Issue{}, platform.ErrNoIssues
	}
	c.hit(r.FullName)
	return platform.CreateIssue(ctx, c.Inner, r, spec)
}

func (c *CountingPlatform) UpdateIssueBody(ctx context.Context, r model.Repo, number int, body string) error {
	if _, ok := c.Inner.(platform.IssueTracker); !ok {
		return platform.ErrNoIssues
	}
	c.hit(r.FullName)
	return platform.UpdateIssueBody(ctx, c.Inner, r, number, body)
}

// CloseIssue forwards to Inner, or answers platform.ErrNoIssues without counting a call.
func (c *CountingPlatform) CloseIssue(ctx context.Context, r model.Repo, number int) error {
	if _, ok := c.Inner.(platform.IssueCloser); !ok {
		return platform.ErrNoIssues
	}
	c.hit(r.FullName)
	return platform.CloseIssue(ctx, c.Inner, r, number)
}

// ReopenIssue forwards to Inner, or answers platform.ErrNoIssues without counting a call.
func (c *CountingPlatform) ReopenIssue(ctx context.Context, r model.Repo, number int) error {
	if _, ok := c.Inner.(platform.IssueReopener); !ok {
		return platform.ErrNoIssues
	}
	c.hit(r.FullName)
	return platform.ReopenIssue(ctx, c.Inner, r, number)
}

// ListTags, TagCommit and ReleaseOf forward to Inner without counting a call (the
// repo they name is another repository's, read through the dependent's credential),
// or answer platform.ErrNoTags when Inner cannot read tags.
func (c *CountingPlatform) ListTags(ctx context.Context, via model.Repo, target string) ([]string, error) {
	return platform.ListTags(ctx, c.Inner, via, target)
}

func (c *CountingPlatform) TagCommit(ctx context.Context, via model.Repo, target, tag string) (string, error) {
	return platform.TagCommit(ctx, c.Inner, via, target, tag)
}

func (c *CountingPlatform) ReleaseOf(ctx context.Context, via model.Repo, target, tag string) (platform.TagRelease, bool, error) {
	return platform.ReleaseOf(ctx, c.Inner, via, target, tag)
}
