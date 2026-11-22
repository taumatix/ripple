package testutil

import (
	"context"
	"sync"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// CountingPlatform records every Platform call per repo, so tests can assert a
// repo that does not import a package was never touched.
type CountingPlatform struct {
	Inner platform.Platform

	mu    sync.Mutex
	calls map[string]int
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

func (c *CountingPlatform) EnableAutoMerge(ctx context.Context, r model.Repo, pr platform.PR) error {
	c.hit(r.FullName)
	return c.Inner.EnableAutoMerge(ctx, r, pr)
}
