package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// Repo-state events: a host that can say a repository was renamed, moved or changed
// its default branch lets ripple follow it without a re-scan (rename) or with exactly
// one (default branch). Hosts that cannot say so never reach this code.

const renamedCursor = "renamed|"

// maxRenameHops bounds following a chain of renames (a -> b -> c).
const maxRenameHops = 8

// lockRepos takes the per-repo locks of two names in a fixed order, so two renames
// that cross cannot deadlock.
func (e *Engine) lockRepos(a, b string) func() {
	names := []string{a, b}
	sort.Strings(names)
	u1 := e.lockRepo(names[0])
	u2 := e.lockRepo(names[1])
	return func() { u2(); u1() }
}

// Rename moves the stored identity of a repo from old to now: the repo record with
// its config and default branch, and its dependency rows. Nothing is cloned. Jobs
// keyed by the old name follow when they come due (see runRepo): the job key holds
// the repo name and the store has no way to list a repo's jobs, so the old name is
// remembered as an alias instead. Open PRs are not stored; the host lists them
// under the new name. An old name ripple does not know is ignored.
func (e *Engine) Rename(ctx context.Context, old, now string, scope int64) error {
	if old == now {
		return nil
	}
	defer e.lockRepos(old, now)()
	repo, found, err := e.Store.GetRepo(ctx, old)
	if err != nil || !found {
		return err
	}
	deps, err := e.Store.ListRepoDependencies(ctx, old)
	if err != nil {
		return err
	}
	for i := range deps {
		deps[i].Repo = now
	}
	repo.FullName = now
	if scope != 0 {
		repo.InstallationID = scope
	}
	// Order matters for a crash in the middle: the new name is complete before the
	// old one goes, and a retry of the event finds old still there and repeats.
	if err := e.Store.UpsertRepo(ctx, repo); err != nil {
		return err
	}
	if err := e.Store.ReplaceDependencies(ctx, now, deps); err != nil {
		return err
	}
	if err := e.Store.SetCursor(ctx, renamedCursor+old, now); err != nil {
		return err
	}
	if err := e.Store.DeleteRepo(ctx, old); err != nil {
		return err
	}
	e.log().Info("repository renamed", "from", old, "to", now, "dependencies", len(deps))
	return nil
}

// ForgetRename drops the alias for name. It is called when name is registered
// (again): a new repository that took a freed name must not inherit the old one's jobs.
func (e *Engine) ForgetRename(ctx context.Context, name string) error {
	if v, err := e.Store.GetCursor(ctx, renamedCursor+name); err != nil || v == "" {
		return err
	}
	return e.Store.SetCursor(ctx, renamedCursor+name, "")
}

// renamedTo follows the alias chain from name to the registered repo it became.
func (e *Engine) renamedTo(ctx context.Context, name string) (string, bool) {
	cur := name
	for range maxRenameHops {
		next, err := e.Store.GetCursor(ctx, renamedCursor+cur)
		if err != nil || next == "" {
			return "", false
		}
		cur = next
		if _, ok, err := e.Store.GetRepo(ctx, cur); err == nil && ok {
			return cur, true
		}
	}
	return "", false
}

// followRename re-keys jobs of a name that no longer exists to the repo it became,
// and reports whether it did. The old jobs end as skipped.
func (e *Engine) followRename(ctx context.Context, name string, jobs []model.Job) (string, bool) {
	target, ok := e.renamedTo(ctx, name)
	if !ok {
		return "", false
	}
	for _, j := range jobs {
		moved := j
		moved.Repo = target
		moved.Key = model.JobKey(target, j.Datasource, j.Package, j.Version)
		if _, err := e.Store.EnqueueJob(ctx, moved); err != nil {
			e.log().Error("move job to renamed repo", "job", j.Key, "err", err)
			continue
		}
		j.State, j.Reason = model.JobSkipped, fmt.Sprintf("repository renamed to %s", target)
		if err := e.Store.UpdateJob(ctx, j); err != nil {
			e.log().Error("update job", "job", j.Key, "err", err)
		}
	}
	return target, true
}

// SetDefaultBranch re-scans a registered repo against branch, which becomes the
// base of the PRs it opens from now on. PRs already open keep their base. The stored
// branch changes only once the scan succeeded, so a failed scan is retried by the
// queue and the repo keeps working from the old index meanwhile. An unknown repo,
// or one already on branch, costs nothing.
func (e *Engine) SetDefaultBranch(ctx context.Context, name, branch string, scope int64) error {
	repo, found, err := e.Store.GetRepo(ctx, name)
	if err != nil || !found || branch == "" || repo.DefaultBranch == branch {
		return err
	}
	repo.DefaultBranch = branch
	if scope != 0 {
		repo.InstallationID = scope
	}
	return e.Scan(ctx, repo)
}

// falseClaimTTL is how long a default-branch claim the host contradicted is not
// examined again, and maxFalseClaims bounds the table that remembers them.
const (
	falseClaimTTL  = time.Hour
	maxFalseClaims = 4096
)

// HealDefaultBranch is the repair for a missed default-branch change. claimed is the
// branch a verified push payload says is the default. It is only a reason to look: when
// it differs from the stored branch the repo is re-scanned with the branch left for the
// host to name, and what is stored is what the host reports, never the payload's text.
// So a stale redelivery or a false claim can cost one scan but cannot point ripple at
// another branch, and the same contradicted claim is not examined again for an hour. It
// reports whether it examined the repo; the scan then covers whatever the push changed.
func (e *Engine) HealDefaultBranch(ctx context.Context, repo model.Repo, claimed string, scope int64) (bool, error) {
	if claimed == "" || claimed == repo.DefaultBranch {
		return false, nil
	}
	key := repo.FullName + "|" + claimed
	e.mu.Lock()
	until, seen := e.falseClaims[key]
	e.mu.Unlock()
	if seen && e.now().Before(until) {
		return false, nil
	}
	repo.DefaultBranch = ""
	if scope != 0 {
		repo.InstallationID = scope
	}
	if err := e.Scan(ctx, repo); err != nil {
		return true, err
	}
	now, found, err := e.Store.GetRepo(ctx, repo.FullName)
	if err != nil {
		return true, err
	}
	if found && now.DefaultBranch != claimed {
		e.mu.Lock()
		if e.falseClaims == nil || len(e.falseClaims) >= maxFalseClaims {
			e.falseClaims = map[string]time.Time{}
		}
		e.falseClaims[key] = e.now().Add(falseClaimTTL)
		e.mu.Unlock()
		e.log().Warn("push claimed a default branch the host does not report", "repo", repo.FullName, "claimed", claimed, "host", now.DefaultBranch)
	} else if found {
		e.log().Info("default branch healed from a push", "repo", repo.FullName, "branch", claimed)
	}
	return true, nil
}
