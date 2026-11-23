package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/rules"
)

// actionsPackagesCursor holds the github-tags packages some repo uses, newline
// separated. The store answers "who uses this package" but cannot list packages,
// and the tags of a GitHub repo have no feed, so ActionsWatcher polls exactly the
// packages Scan recorded here instead of every repository on GitHub.
const actionsPackagesCursor = "github-tags:packages"

var actionsPackagesMu sync.Mutex

// noteActionPackages adds the github-tags packages of deps to the polled set. A
// lost update (two replicas racing) only delays a package until the next scan of
// a repo that uses it.
func (e *Engine) noteActionPackages(ctx context.Context, deps []model.Dependency) error {
	return e.notePackages(ctx, model.DatasourceGitHubTags, actionsPackagesCursor, deps)
}

// notePackages adds the packages of deps on datasource ds to the newline-separated set
// held in cursor.
func (e *Engine) notePackages(ctx context.Context, ds, cursor string, deps []model.Dependency) error {
	var add []string
	for _, d := range deps {
		if d.Datasource == ds {
			add = append(add, d.Package)
		}
	}
	if len(add) == 0 {
		return nil
	}
	actionsPackagesMu.Lock()
	defer actionsPackagesMu.Unlock()
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for _, p := range strings.Fields(raw) {
		set[p] = true
	}
	n := len(set)
	for _, p := range add {
		set[p] = true
	}
	if len(set) == n {
		return nil
	}
	all := make([]string, 0, len(set))
	for p := range set {
		all = append(all, p)
	}
	sort.Strings(all)
	return e.Store.SetCursor(ctx, cursor, strings.Join(all, "\n"))
}

// dropActionPackage removes pkg from the polled set. It re-reads the cursor under the
// lock and re-checks the dependents, so a repo that started using pkg since the poll
// began keeps it; a lost update still only costs a re-add at the next scan.
func (e *Engine) dropActionPackage(ctx context.Context, pkg string) error {
	actionsPackagesMu.Lock()
	defer actionsPackagesMu.Unlock()
	deps, err := e.Store.ListDependents(ctx, model.DatasourceGitHubTags, pkg)
	if err != nil || len(deps) > 0 {
		return err
	}
	raw, err := e.Store.GetCursor(ctx, actionsPackagesCursor)
	if err != nil {
		return err
	}
	var keep []string
	for _, p := range strings.Fields(raw) {
		if p != pkg {
			keep = append(keep, p)
		}
	}
	return e.Store.SetCursor(ctx, actionsPackagesCursor, strings.Join(keep, "\n"))
}

var tagShapeRE = regexp.MustCompile(`^(v?)([0-9]+(?:\.[0-9]+){0,2})$`)

// tagShape splits a tag into the semver ripple compares and its "shape": whether it
// has a v prefix and how many components it spells. v4, v4.1 and v4.1.2 are three
// different shapes of one release line, each moved only to its own shape, because
// `uses: a/b@v4` floats on purpose and must stay floating.
func tagShape(tag string) (sv, shape string, ok bool) {
	m := tagShapeRE.FindStringSubmatch(tag)
	if m == nil {
		return "", "", false
	}
	return "v" + m[2], fmt.Sprintf("%s%d", m[1], strings.Count(m[2], ".")+1), true
}

// actionTarget is the newest tag of the same shape as cur that is newer than it, or "".
// Upstream not publishing a tag of cur's shape for a newer release (a "v5" when only
// "v5.0.0" exists) leaves the dependency alone rather than changing how it floats.
func actionTarget(cur string, tags []string) string {
	curSV, curShape, ok := tagShape(cur)
	if !ok {
		return ""
	}
	best, bestSV := "", curSV
	for _, t := range tags {
		sv, shape, ok := tagShape(t)
		if ok && shape == curShape && semver.Compare(sv, bestSV) > 0 {
			best, bestSV = t, sv
		}
	}
	return best
}

// applicableTag judges a github-tags release for a dependency: the same package, the
// same tag shape, and newer.
func applicableTag(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceGitHubTags || d.Package != pkg {
		return "", false
	}
	curSV, curShape, ok1 := tagShape(d.Version)
	sv, shape, ok2 := tagShape(version)
	if !ok1 || !ok2 || curShape != shape {
		return "", false
	}
	return rules.Classify(curSV, sv)
}

// ActionsWatcher turns the tags of the GitHub Actions repos in use into jobs. It
// reads the tags of each package once per poll, through the first dependent repo's
// installation, and enqueues one job per (repo, package, target tag); the job key
// makes the poll idempotent.
type ActionsWatcher struct {
	Engine *Engine
}

// Poll returns how many jobs were new. It makes no request while no repo uses an action.
func (w *ActionsWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	raw, err := e.Store.GetCursor(ctx, actionsPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range strings.Fields(raw) {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceGitHubTags, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropActionPackage(ctx, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		via, found, err := e.Store.GetRepo(ctx, deps[0].Repo)
		if err != nil {
			return n, err
		}
		if !found {
			continue
		}
		tags, err := platform.ListTags(ctx, e.Platform, via, pkg)
		if err != nil {
			e.log().Warn("tags not read", "package", pkg, "err", err)
			continue
		}
		released := map[string]platform.TagRelease{}
		for _, d := range deps {
			target := actionTarget(d.Version, tags)
			rs, err := e.repoState(ctx, repos, d.Repo)
			if err != nil {
				return n, err
			}
			if !rs.found {
				continue
			}
			other := ""
			if target == "" {
				other = newestOtherShape(d.Version, tags)
			}
			if err := e.noteShapeSkip(ctx, rs, d, other); err != nil {
				return n, err
			}
			if target == "" {
				continue
			}
			typ, ok := applicable(d, model.DatasourceGitHubTags, pkg, target)
			if !ok {
				continue
			}
			rel, seen := released[target]
			if !seen {
				rel, _, err = platform.ReleaseOf(ctx, e.Platform, via, pkg, target)
				if err != nil {
					e.log().Warn("release not read", "package", pkg, "tag", target, "err", err)
				}
				released[target] = rel
			}
			// A tag with no release has no date: the cooldown runs from the first poll
			// that saw it, and EnqueueJob keeps the first ReleasedAt on a replay.
			at := rel.Published
			if at.IsZero() {
				at = e.now()
			}
			dec, ok := e.decide(rs, d, typ, at)
			if !ok || !dec.Enabled {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceGitHubTags, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceGitHubTags, Package: pkg, Version: target,
				ReleasedAt: at, State: model.JobPending, NotBefore: dec.NotBefore,
			})
			if err != nil {
				return n, err
			}
			if inserted {
				n++
			}
		}
	}
	return n, nil
}

// errWorkflowsPermission is a push GitHub refused because the App may not change
// files under .github/workflows.
var errWorkflowsPermission = errors.New("the GitHub App lacks the workflows permission")

const workflowsReason = "no pull request opened: the GitHub App lacks the workflows permission, which GitHub requires to change files under .github/workflows (see docs/github-actions.md); granting it is enough, the update is retried daily"

// workflowsDenied reports whether a push error is GitHub refusing a workflow change
// for want of the workflows permission. GitHub's documentation does not give the
// wording, so this matches loosely on the two words any version of it carries.
func workflowsDenied(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "workflow") && strings.Contains(s, "permission")
}

func touchesWorkflows(items []item) bool {
	for _, it := range items {
		for _, d := range it.deps {
			if strings.HasPrefix(d.File, ".github/workflows/") {
				return true
			}
		}
	}
	return false
}

// actionVersion is the version string Apply takes for a github-tags job: the tag,
// plus the commit it points at when that can be resolved. A failed resolution is
// not an error here: only a SHA-pinned line needs the commit, and the manager
// refuses to rewrite one without it.
func (r *runner) actionVersion(pkg, tag string) string {
	if sha, ok := r.shas[pkg+"@"+tag]; ok {
		return tag + "@" + sha
	}
	sha, err := platform.TagCommit(r.ctx, r.e.Platform, r.repo, pkg, tag)
	if err != nil || sha == "" {
		r.e.log().Warn("tag not resolved to a commit", "repo", r.repo.FullName, "package", pkg, "tag", tag, "err", err)
		return tag
	}
	if r.shas == nil {
		r.shas = map[string]string{}
	}
	r.shas[pkg+"@"+tag] = sha
	return tag + "@" + sha
}

// actionNotes links the release of each updated action that has one, so the reader
// finds the changelog where the host publishes it.
func (r *runner) actionNotes(items []item) string {
	seen := map[string]bool{}
	var lines []string
	for _, it := range items {
		if it.job.Datasource != model.DatasourceGitHubTags || seen[it.job.Key] {
			continue
		}
		seen[it.job.Key] = true
		rel, ok, err := platform.ReleaseOf(r.ctx, r.e.Platform, r.repo, it.job.Package, it.job.Version)
		if err == nil && ok && rel.URL != "" {
			lines = append(lines, fmt.Sprintf("- `%s` %s: %s", it.job.Package, it.job.Version, rel.URL))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	return "\n## Release notes\n\n" + strings.Join(lines, "\n") + "\n"
}
