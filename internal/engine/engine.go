// Package engine ties the pieces together: scan repos into the reverse index, fan
// feed releases out to the repos that import them, and turn due jobs into PRs.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/store"
)

// BranchPrefix marks every branch ripple owns.
const BranchPrefix = "ripple/"

type Engine struct {
	Store    store.Store
	Platform platform.Platform
	Limits   rules.Limits
	Log      *slog.Logger
	// Now is injectable so tests control minimumReleaseAge and schedules.
	Now func() time.Time
	// Workers bounds how many repos RunDue processes at once.
	Workers int

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.New(slog.DiscardHandler)
}

// lockRepo serialises scans and PR runs per repo so a push re-scan cannot race a
// branch push.
func (e *Engine) lockRepo(name string) func() {
	e.mu.Lock()
	if e.locks == nil {
		e.locks = map[string]*sync.Mutex{}
	}
	m := e.locks[name]
	if m == nil {
		m = &sync.Mutex{}
		e.locks[name] = m
	}
	e.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// RepoConfig parses a stored repo file; nil or empty bytes mean the defaults.
func RepoConfig(raw []byte) (*config.Repo, error) {
	return config.Parse(raw)
}

// Scan checks out the repo, reads .github/ripple.yml (absent means defaults),
// extracts every dependency and replaces the repo's rows in the reverse index. An
// invalid file registers the repo with no dependencies, so nothing is updated until
// the file is fixed.
func (e *Engine) Scan(ctx context.Context, repo model.Repo) error {
	defer e.lockRepo(repo.FullName)()
	ws, err := e.Platform.Checkout(ctx, repo)
	if err != nil {
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	defer ws.Close()
	if repo.DefaultBranch == "" {
		repo.DefaultBranch = ws.DefaultBranch()
	}
	raw, err := ws.ReadFile(config.FileName)
	if err != nil && !errors.Is(err, platform.ErrNotExist) {
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	repo.Config = raw
	cfg, cerr := RepoConfig(raw)
	if cerr != nil {
		e.log().Warn("invalid repo config; no updates until fixed", "repo", repo.FullName, "err", cerr)
		if err := e.Store.UpsertRepo(ctx, repo); err != nil {
			return err
		}
		return e.Store.ReplaceDependencies(ctx, repo.FullName, nil)
	}
	ms, err := manager.Build(cfg)
	if err != nil {
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	var deps []model.Dependency
	seen := map[string]bool{}
	err = ws.Walk(func(p string) error {
		for _, m := range ms {
			if !m.Matches(p) || seen[p+"|"+m.Name()] {
				continue
			}
			seen[p+"|"+m.Name()] = true
			content, err := ws.ReadFile(p)
			if err != nil {
				e.log().Warn("skip unreadable file", "repo", repo.FullName, "file", p, "err", err)
				continue
			}
			found, err := m.Extract(p, content)
			if err != nil {
				e.log().Warn("skip unparsable file", "repo", repo.FullName, "file", p, "err", err)
				continue
			}
			for _, d := range found {
				d.Repo = repo.FullName
				deps = append(deps, d)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	if err := e.Store.UpsertRepo(ctx, repo); err != nil {
		return err
	}
	if err := e.Store.ReplaceDependencies(ctx, repo.FullName, deps); err != nil {
		return err
	}
	e.log().Info("scanned", "repo", repo.FullName, "dependencies", len(deps))
	return nil
}

// Remove forgets a repo (App uninstalled or repo removed from the installation).
func (e *Engine) Remove(ctx context.Context, name string) error {
	defer e.lockRepo(name)()
	return e.Store.DeleteRepo(ctx, name)
}
