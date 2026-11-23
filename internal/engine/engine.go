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

func (e *Engine) dueBatch() int {
	if e.DueBatch > 0 {
		return e.DueBatch
	}
	return dueBatch
}

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
	// AdvisoryHistory, when set, lets a scan ask OSV about the advisories that already
	// cover what a repo uses (see checkAdvisoryHistory). Nil leaves the poller the only source.
	AdvisoryHistory AdvisoryQuerier
	// MaxBackfill bounds the jobs one scan creates for dependencies new to the repo; zero
	// means DefaultMaxBackfill.
	MaxBackfill int
	// Replica names this process among the replicas sharing the Store; empty picks a random
	// id, which is right unless logs should carry a stable name. LeaseTTL is how long a
	// repo claim survives without renewal (zero means DefaultLeaseTTL). See docs/replicas.md.
	Replica  string
	LeaseTTL time.Duration
	// DueBatch is how many due jobs one listing reads; zero means 1000.
	DueBatch int

	// GoCC is the C compiler for repositories that opt into cgo; empty means cc.
	// GoProxyUser and GoProxyToken authenticate to the configured module proxy for the
	// steps that execute no repository code; empty means no credential.
	GoCC, GoProxyUser, GoProxyToken string

	// parseConfig is a test seam counting repo-file parses; nil means RepoConfig.
	parseConfig func([]byte) (*config.Repo, error)

	idOnce sync.Once
	id     string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// dirty is the repos whose jobs changed outside their own run, and dashOff the
	// repos whose issues could not be written until a time; both see dashboard.go.
	dirty map[string]bool
	// advMu serialises the read-modify-write of a repo's open-advisory list, which the
	// poller and a scan both write and only a scan does under the repo lock.
	advMu     sync.Mutex
	histCache map[string]cachedVuln
	dashOff   map[string]time.Time
	// falseClaims remembers default-branch claims the host contradicted (HealDefaultBranch).
	falseClaims map[string]time.Time
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
	m := e.repoMutex(name)
	m.Lock()
	return m.Unlock
}

func (e *Engine) repoMutex(name string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locks == nil {
		e.locks = map[string]*sync.Mutex{}
	}
	m := e.locks[name]
	if m == nil {
		m = &sync.Mutex{}
		e.locks[name] = m
	}
	return m
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
//
// The scan reads a partial clone with no working tree when the host allows it, so
// only the files a manager matches are downloaded; if the host cannot, it repeats on
// a full checkout.
func (e *Engine) Scan(ctx context.Context, repo model.Repo) error {
	defer e.lockRepo(repo.FullName)()
	hold, err := e.acquireRepo(ctx, repo.FullName, true)
	if err != nil {
		return fmt.Errorf("scan %s: %w", repo.FullName, err)
	}
	defer hold.Release()
	ctx = hold.Ctx
	defer func() { e.syncDashboard(ctx, repo.FullName) }()
	err = e.scan(platform.WithScanOnly(ctx), repo)
	if errors.Is(err, platform.ErrScanFallback) {
		e.log().Info("partial scan unavailable; scanning a full checkout", "repo", repo.FullName, "reason", err)
		err = e.scan(ctx, repo)
	}
	return err
}

func (e *Engine) scan(ctx context.Context, repo model.Repo) error {
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
	// Walk first, read after: a workspace that fetches lazily gets every path in one request.
	type hit struct {
		path string
		ms   []manager.Manager
	}
	var hits []hit
	files := 0
	err = ws.Walk(func(p string) error {
		if files++; files > maxFiles {
			return errScanCap
		}
		var matched []manager.Manager
		for _, m := range ms {
			if m.Matches(p) {
				matched = append(matched, m)
			}
		}
		if len(matched) > 0 {
			hits = append(hits, hit{p, matched})
		}
		return nil
	})
	if err == nil {
		if pf, ok := ws.(platform.Prefetcher); ok {
			paths := make([]string, len(hits))
			for i, h := range hits {
				paths[i] = h.path
			}
			err = pf.Prefetch(ctx, paths)
		}
	}
	var deps []model.Dependency
	seen := map[string]bool{}
	for _, h := range hits {
		if err != nil {
			break
		}
		for _, m := range h.ms {
			if seen[h.path+"|"+m.Name()] {
				continue
			}
			seen[h.path+"|"+m.Name()] = true
			content, rerr := ws.ReadFile(h.path)
			if errors.Is(rerr, platform.ErrScanFallback) {
				err = rerr
				break
			}
			if rerr != nil {
				e.log().Warn("skip unreadable file", "repo", repo.FullName, "file", h.path, "err", rerr)
				continue
			}
			found, xerr := m.Extract(h.path, content)
			if xerr != nil {
				e.log().Warn("skip unparsable file", "repo", repo.FullName, "file", h.path, "err", xerr)
				continue
			}
			for _, d := range found {
				d.Repo = repo.FullName
				deps = append(deps, d)
				if len(deps) > maxDeps {
					err = errDepCap
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
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
	if err := e.noteActionPackages(ctx, deps); err != nil {
		return err
	}
	if err := e.Store.ReplaceDependencies(ctx, repo.FullName, deps); err != nil {
		return err
	}
	if err := e.checkAdvisoryHistory(ctx, repo.FullName, cfg, deps); err != nil {
		// OSV being down must not fail a scan: the pairs stay unchecked and the next scan retries.
		e.log().Warn("advisory history not checked", "repo", repo.FullName, "err", err)
	}
	e.log().Info("scanned", "repo", repo.FullName, "dependencies", len(deps))
	return nil
}

// Remove forgets a repo (App uninstalled or repo removed from the installation).
func (e *Engine) Remove(ctx context.Context, name string) error {
	ctx, _, release, err := e.leaseRepo(ctx, name)
	if err != nil {
		return err
	}
	defer release()
	if err := e.Store.SetCursor(ctx, pausedCursor+name, ""); err != nil {
		return err
	}
	return e.Store.DeleteRepo(ctx, name)
}
