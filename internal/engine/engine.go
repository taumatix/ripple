// Package engine ties the pieces together: scan repos into the reverse index, fan
// feed releases out to the repos that import them, and turn due jobs into PRs.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// Default scan caps, large enough for a big monorepo and small enough that one
// repo cannot exhaust memory or time.
const (
	DefaultMaxFiles        = config.DefaultMaxFiles
	DefaultMaxDependencies = config.DefaultMaxDependencies
)

var (
	errScanCap = errors.New("file cap exceeded")
	errDepCap  = errors.New("dependency cap exceeded")
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
	// MaxFiles and MaxDependencies bound one repo scan; zero means DefaultMaxFiles
	// and DefaultMaxDependencies. A repo over either cap is registered with no
	// dependencies and a logged warning rather than scanned partially.
	MaxFiles, MaxDependencies int
	// Advisories is set when OSV is enabled: the runner then looks up the advisory
	// note of each job (see FanOutAdvisory) to label the PR and skip the cooldown.
	Advisories bool
	// MaxBackfill bounds the jobs one scan creates for dependencies new to the repo; zero
	// means DefaultMaxBackfill.
	MaxBackfill int

	// GoCC is the C compiler for repositories that opt into cgo; empty means cc.
	// GoProxyUser and GoProxyToken authenticate to the configured module proxy for the
	// steps that execute no repository code; empty means no credential.
	GoCC, GoProxyUser, GoProxyToken string

	// parseConfig is a test seam counting repo-file parses; nil means RepoConfig.
	parseConfig func([]byte) (*config.Repo, error)

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

// Scan checks out the repo, reads .ripple.yaml, or on a host that still reads it the
// deprecated .github/ripple.yml (absent means defaults),
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
	legacy := platform.ReadsLegacyConfig(e.Platform, repo)
	src, err := config.Resolve(func(name string) ([]byte, error) {
		if name == config.LegacyFileName && !legacy {
			return nil, fs.ErrNotExist // the alias is GitHub's alone
		}
		b, err := ws.ReadFile(name)
		if errors.Is(err, platform.ErrNotExist) {
			err = fs.ErrNotExist
		}
		return b, err
	})
	if err != nil {
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	if w := src.Warning(); w != "" {
		e.log().Warn(w, "repo", repo.FullName)
	}
	repo.Config = src.Data
	cfg, cerr := config.ParseNamed(src.Name, src.Data)
	if src.Name == "" {
		cfg, cerr = config.Default(), nil
	}
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
	maxFiles, maxDeps := e.MaxFiles, e.MaxDependencies
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	if maxDeps <= 0 {
		maxDeps = DefaultMaxDependencies
	}
	var deps []model.Dependency
	seen := map[string]bool{}
	files := 0
	err = ws.Walk(func(p string) error {
		if files++; files > maxFiles {
			return errScanCap
		}
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
				if len(deps) > maxDeps {
					return errDepCap
				}
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errScanCap), errors.Is(err, errDepCap):
		what, limit := "file", maxFiles
		if errors.Is(err, errDepCap) {
			what, limit = "dependency", maxDeps
		}
		e.log().Warn(fmt.Sprintf("repo exceeds the %s cap; no updates until it is under it", what), "repo", repo.FullName, "cap", limit)
		if err := e.Store.UpsertRepo(ctx, repo); err != nil {
			return err
		}
		return e.Store.ReplaceDependencies(ctx, repo.FullName, nil)
	case err != nil:
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	if err := e.Store.UpsertRepo(ctx, repo); err != nil {
		return err
	}
	// Before the rows are replaced, so a failure is retried with the same "new" set.
	prev, err := e.Store.ListRepoDependencies(ctx, repo.FullName)
	if err != nil {
		return err
	}
	if err := e.backfill(ctx, repo.FullName, cfg, prev, deps); err != nil {
		return fmt.Errorf("scan %s: backfill: %w", repo.FullName, err)
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
