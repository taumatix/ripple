package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// Repo-state events: a host that can say a repository was renamed, moved or changed
// its default branch lets ripple follow it without a re-scan (rename) or with exactly
// one (default branch). Hosts that cannot say so never reach this code.

const renamedCursor = "renamed|"

// pausedCursor marks a repo the host archived: read-only, so nothing may be done to it.
const pausedCursor = "paused|"

// pauseDefer is how far a due job of a paused repo is pushed back each time it comes up.
const pauseDefer = time.Hour

// repoIDCursor maps a host repository id to the stored name that holds it
// ("repoid|<id>" -> name), so a push under a new name finds the record to rename. It is
// a hint: it is trusted only while the record it names still carries that id.
const repoIDCursor = "repoid|"

// removedCursor is the tombstone a removal leaves ("removed|<name>" -> "<unix>|<id>", id 0
// when it was not known). The rule it serves: for removedTTL after a removal, a signed
// push for that name does not register it again unless it carries a different repository
// id than the removed one (a new repository that took the name). An explicit re-add
// (repos_added, unarchive of an unknown repo) clears it. Expired tombstones are never
// read again but are not swept: one small cursor row per removed repo.
const removedCursor = "removed|"

// removedTTL is how long a removal is remembered. A delivery the host retries arrives
// within hours; a day covers it without keeping a removed repo out for long.
const removedTTL = 24 * time.Hour

// maxRenameHops bounds following a chain of renames (a -> b -> c).
const maxRenameHops = 8

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
	ctx, holds, release, err := e.leaseRepos(ctx, old, now)
	if err != nil {
		return err
	}
	defer release()
	repo, found, err := e.Store.GetRepo(ctx, old)
	if err != nil || !found {
		return err
	}
	if _, there, err := e.Store.GetRepo(ctx, now); err != nil {
		return err
	} else if there {
		// A push under the new name registered it before this event arrived. Its record is
		// the fresher one, so it stays; the old one goes and the alias carries the old jobs.
		if err := e.Store.SetCursor(ctx, renamedCursor+old, now); err != nil {
			return err
		}
		if err := e.Store.DeleteRepo(ctx, old); err != nil {
			return err
		}
		e.log().Info("repository renamed; the new name was already registered", "from", old, "to", now)
		return nil
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
	if repo.RepoID != 0 {
		if err := e.Store.SetCursor(ctx, repoIDCursor+strconv.FormatInt(repo.RepoID, 10), now); err != nil {
			return err
		}
	}
	if err := e.Store.DeleteRepo(ctx, old); err != nil {
		return err
	}
	if v, _ := e.Store.GetCursor(ctx, pausedCursor+old); v != "" {
		if err := e.Store.SetCursor(ctx, pausedCursor+now, v); err != nil {
			return err
		}
		if err := e.Store.SetCursor(ctx, pausedCursor+old, ""); err != nil {
			return err
		}
	}
	// Re-key the pending jobs now, so a new repository that takes the freed name cannot be
	// handed them; followRename stays for jobs a retried event or an older version left behind.
	if lister, ok := e.Store.(store.OpenJobLister); ok {
		if jobs, err := lister.ListOpenJobs(ctx, old); err == nil {
			var pending []model.Job
			for _, j := range jobs {
				if j.State == model.JobPending {
					pending = append(pending, j)
				}
			}
			e.moveJobs(ctx, holds[old], now, pending)
		}
	}
	e.log().Info("repository renamed", "from", old, "to", now, "dependencies", len(deps))
	return nil
}

// ForgetRename drops the alias for name. It is called when name is registered
// (again): a new repository that took a freed name must not inherit the old one's jobs.
func (e *Engine) ForgetRename(ctx context.Context, name string) error {
	if v, err := e.Store.GetCursor(ctx, removedCursor+name); err == nil && v != "" {
		if err := e.Store.SetCursor(ctx, removedCursor+name, ""); err != nil {
			return err
		}
	}
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

// RenamedTo reports the registered repo that name was renamed to, following the alias
// chain (bounded by maxRenameHops).
func (e *Engine) RenamedTo(ctx context.Context, name string) (string, bool) {
	return e.renamedTo(ctx, name)
}

// RegisterFromPush registers a repo the first time a verified push names it, when the
// host vouched for the repo with an immutable id (the push beat the installation or rename
// event that would have registered it). It is idempotent: a registered repo is left alone.
// The branch is left for the host to name and the scan is the same one a repos_added does,
// so a repo the credential cannot reach fails the scan and the queue's retry bound applies.
func (e *Engine) RegisterFromPush(ctx context.Context, name string, scope, id int64) error {
	if _, found, err := e.Store.GetRepo(ctx, name); err != nil || found {
		return err
	}
	if err := e.ForgetRename(ctx, name); err != nil {
		return err
	}
	e.log().Info("repository registered from a push", "repo", name)
	return e.Scan(ctx, model.Repo{FullName: name, InstallationID: scope, RepoID: id})
}

// RepoIDOwner reports the stored repo that holds host repository id, if any.
func (e *Engine) RepoIDOwner(ctx context.Context, id int64) (string, bool) {
	name, err := e.Store.GetCursor(ctx, repoIDCursor+strconv.FormatInt(id, 10))
	if err != nil || name == "" {
		return "", false
	}
	if repo, ok, err := e.Store.GetRepo(ctx, name); err != nil || !ok || repo.RepoID != id {
		return "", false
	}
	return name, true
}

// AdoptRepoID stores the host's repository id on a registered repo that has none (one
// registered by an install event, or before ids were stored). A repo that has an id keeps it.
func (e *Engine) AdoptRepoID(ctx context.Context, name string, id int64) error {
	defer e.lockRepo(name)()
	repo, ok, err := e.Store.GetRepo(ctx, name)
	if err != nil || !ok || repo.RepoID != 0 || id == 0 {
		return err
	}
	repo.RepoID = id
	if err := e.Store.UpsertRepo(ctx, repo); err != nil {
		return err
	}
	return e.Store.SetCursor(ctx, repoIDCursor+strconv.FormatInt(id, 10), name)
}

func (e *Engine) releaseRepoID(ctx context.Context, id int64, name string) error {
	key := repoIDCursor + strconv.FormatInt(id, 10)
	if v, err := e.Store.GetCursor(ctx, key); err != nil || v != name {
		return err
	}
	return e.Store.SetCursor(ctx, key, "")
}

// RemovedRecently applies the tombstone rule (see removedCursor) to a signed push for
// name carrying repository id: true means the push is a late delivery for a removed repo.
func (e *Engine) RemovedRecently(ctx context.Context, name string, id int64) bool {
	v, err := e.Store.GetCursor(ctx, removedCursor+name)
	if err != nil || v == "" {
		return false
	}
	at, tomb, _ := strings.Cut(v, "|")
	secs, err1 := strconv.ParseInt(at, 10, 64)
	removed, err2 := strconv.ParseInt(tomb, 10, 64)
	if err1 != nil || err2 != nil || e.now().Sub(time.Unix(secs, 0)) > removedTTL {
		return false
	}
	return removed == 0 || removed == id
}

// followRename re-keys jobs of a name that no longer exists to the repo it became,
// and reports whether it did. The old jobs end as skipped.
func (e *Engine) followRename(ctx context.Context, hold *repoLease, name string, jobs []model.Job) (string, bool) {
	target, ok := e.renamedTo(ctx, name)
	if !ok {
		return "", false
	}
	e.moveJobs(ctx, hold, target, jobs)
	return target, true
}

// moveJobs re-keys jobs to target; the old rows end as skipped. hold is the lease of the
// repo the jobs came from, which fences those writes.
func (e *Engine) moveJobs(ctx context.Context, hold *repoLease, target string, jobs []model.Job) {
	for _, j := range jobs {
		moved := j
		moved.Repo = target
		moved.Key = model.JobKey(target, j.Datasource, j.Package, j.Version)
		if _, err := e.Store.EnqueueJob(ctx, moved); err != nil {
			e.log().Error("move job to renamed repo", "job", j.Key, "err", err)
			continue
		}
		j.State, j.Reason = model.JobSkipped, fmt.Sprintf("repository renamed to %s", target)
		if err := e.updateJob(ctx, hold, j); err != nil {
			e.log().Error("update job", "job", j.Key, "err", err)
		}
	}
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

// Paused reports whether name was archived and not yet unarchived.
func (e *Engine) Paused(ctx context.Context, name string) bool {
	v, err := e.Store.GetCursor(ctx, pausedCursor+name)
	return err == nil && v != ""
}

// Pause is what archive means: the repo is read-only, so ripple stops acting on it (no PRs,
// no dashboard writes) but keeps its index and pending jobs. An unknown repo is ignored.
func (e *Engine) Pause(ctx context.Context, name string) error {
	ctx, _, release, err := e.leaseRepo(ctx, name)
	if err != nil {
		return err
	}
	defer release()
	if _, found, err := e.Store.GetRepo(ctx, name); err != nil || !found {
		return err
	}
	if err := e.Store.SetCursor(ctx, pausedCursor+name, "1"); err != nil {
		return err
	}
	e.log().Info("repository archived; paused", "repo", name)
	return nil
}

// Resume undoes Pause: the jobs deferred meanwhile become due, with no scan. A repo that
// is not registered, or was not paused (the archive was never seen, or predates pausing),
// is scanned instead, which is what unarchive meant before. It reports whether jobs may
// be due so the caller can wake the runner.
func (e *Engine) Resume(ctx context.Context, name string, scope int64) error {
	lctx, hold, release, err := e.leaseRepo(ctx, name)
	if err != nil {
		return err
	}
	_, found, err := e.Store.GetRepo(lctx, name)
	if err != nil {
		release()
		return err
	}
	if !found || !e.Paused(lctx, name) {
		release() // Scan takes the lease itself
		if err := e.ForgetRename(ctx, name); err != nil {
			return err
		}
		return e.Scan(ctx, model.Repo{FullName: name, InstallationID: scope})
	}
	defer release()
	ctx = lctx
	if err := e.Store.SetCursor(ctx, pausedCursor+name, ""); err != nil {
		return err
	}
	if lister, ok := e.Store.(store.OpenJobLister); ok {
		jobs, err := lister.ListOpenJobs(ctx, name)
		if err != nil {
			return err
		}
		now := e.now()
		for _, j := range jobs {
			if j.State != model.JobPending || !j.NotBefore.After(now) || j.Reason != pausedReason {
				continue
			}
			j.NotBefore, j.Reason = now, ""
			if err := e.updateJob(ctx, hold, j); err != nil {
				return err
			}
		}
	}
	e.log().Info("repository unarchived; resumed", "repo", name)
	return nil
}

const pausedReason = "repository archived"
