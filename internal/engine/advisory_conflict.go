package engine

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// Precedence between jobs that edit the same toolchain line (entry 81). A security
// toolchain job (the `toolchain` line, moved because an advisory names it) and a `go`
// directive job both rewrite the toolchain line of one go.mod, so two open PRs would
// conflict on the same line whichever merges first. The order is:
//
//  1. a security toolchain job, the higher target version first;
//  2. any other toolchain job.
//
// A job that ranks below a claim on the same file is deferred, with a reason naming the
// claim, and is re-judged on its next run: once the security PR is merged the dependency
// is already past it (or the directive PR opens on top of the merged line). A PR that is
// already open is never closed for this; whoever merges second resolves the conflict.
//
// Claims are the security toolchain plans of this run and the open security toolchain
// PRs ripple opened (its own finished job, advisory note and PR URL must agree, as
// supersede requires: a branch name proves nothing).

// claim is a security toolchain update that goes first on its files.
type claim struct {
	version string // semver target, "v1.2.3"
	branch  string
	ref     string // what the deferral reason names
	files   map[string]bool
}

// holdForSecurity removes from plans the toolchain items that must wait for a security
// toolchain update on the same file, defers their jobs, and returns the rest.
func (r *runner) holdForSecurity(plans []*prPlan, deps []model.Dependency, open []platform.PR) []*prPlan {
	if !r.e.Advisories {
		return plans
	}
	toolFiles := map[string]bool{}
	for _, d := range deps {
		if d.Datasource == model.DatasourceGoToolchain {
			toolFiles[d.File] = true
		}
	}
	if len(toolFiles) == 0 {
		return plans
	}
	var claims []claim
	for _, p := range plans {
		for _, it := range p.items {
			if it.job.Datasource == model.DatasourceGoToolchain && it.advisory != nil {
				claims = append(claims, claim{
					version: it.job.Version, branch: p.branch,
					ref:   "the security update of the " + it.job.Package + " to " + it.job.Version,
					files: itemFiles(it),
				})
			}
		}
	}
	for _, pr := range open {
		if tail, ok := r.securityToolchainPR(pr); ok {
			claims = append(claims, claim{version: tail, branch: pr.Head, ref: "the open security pull request " + pr.URL, files: toolFiles})
		}
	}
	if len(claims) == 0 {
		return plans
	}
	var kept []*prPlan
	for _, p := range plans {
		var items []item
		for _, it := range p.items {
			if by, held := heldBy(it, p.branch, claims); held {
				r.deferJob(it.job, r.e.now().Add(deferBy), fmt.Sprintf("waiting for %s: both edit the toolchain line of the same go.mod", by))
				continue
			}
			items = append(items, it)
		}
		if len(items) > 0 {
			p.items = items
			kept = append(kept, p)
		}
	}
	return kept
}

// securityToolchainPR reports whether pr is an open security toolchain PR ripple opened,
// and the version it moves to: its own finished job, advisory note and PR URL must agree.
func (r *runner) securityToolchainPR(pr platform.PR) (string, bool) {
	tail, ok := strings.CutPrefix(pr.Head, BranchPrefix+"go/"+slug(model.PackageGoToolchain)+"-")
	if !ok || semver.Canonical(tail) != tail || slug(tail) != tail {
		return "", false
	}
	j, found, err := r.e.Store.GetJob(r.ctx, model.JobKey(r.repo.FullName, model.DatasourceGoToolchain, model.PackageGoToolchain, tail))
	if err != nil || !found || j.State != model.JobDone || j.PRURL == "" || j.PRURL != pr.URL || r.e.advisoryNoteFor(r.ctx, j) == nil {
		return "", false
	}
	return tail, true
}

func itemFiles(it item) map[string]bool {
	f := map[string]bool{}
	for _, d := range it.deps {
		f[d.File] = true
	}
	return f
}

// heldBy returns the claim an item must wait for: one on a shared file that outranks it.
func heldBy(it item, branch string, claims []claim) (string, bool) {
	if it.job.Datasource != model.DatasourceGoToolchain {
		return "", false
	}
	files := itemFiles(it)
	for _, c := range claims {
		if c.branch == branch || !overlaps(files, c.files) {
			continue
		}
		if it.advisory == nil || semver.Compare(c.version, it.job.Version) > 0 {
			return c.ref, true
		}
	}
	return "", false
}

func overlaps(a, b map[string]bool) bool {
	for f := range a {
		if b[f] {
			return true
		}
	}
	return false
}

// closeBeaten runs after a plan's PR was opened. When it is a security toolchain PR, an
// open directive or toolchain PR of ripple's that does not fix an advisory edits the same
// line and would conflict with it, so that PR is closed (untouched, with a reason) and its
// job goes back to waiting: holdForSecurity then keeps it waiting while this PR is open,
// and it is judged afresh once it is merged. A PR a person touched, a host that cannot
// close, or close-superseded-pull-requests: false leave both open, as before.
func (r *runner) closeBeaten(p *prPlan, pr platform.PR, open []platform.PR) {
	if !r.e.Advisories || !r.cfg.ClosesSuperseded() {
		return
	}
	security := false
	for _, it := range p.items {
		if it.job.Datasource == model.DatasourceGoToolchain && it.advisory != nil {
			security = true
		}
	}
	if security {
		r.closeBeatenBy(pr, open)
	}
}

// sweepBeaten retries what closeBeaten failed to close in an earlier run: a close the
// host refused, or a repo that turned close-superseded-pull-requests on since. For every
// open security toolchain PR of ripple's it closes the open directive and toolchain PRs
// that are not security PRs and edit the same line, so a beaten PR does not stay open
// beside the one that beat it for good. It makes no call when no such pair is open and
// returns the heads it closed.
func (r *runner) sweepBeaten(open []platform.PR) map[string]bool {
	closed := map[string]bool{}
	if !r.e.Advisories || !r.cfg.ClosesSuperseded() {
		return closed
	}
	var beaten bool
	for _, old := range open {
		for _, pkg := range []string{model.PackageGoDirective, model.PackageGoToolchain} {
			if _, ok := staleVersion(old.Head, pkg); ok {
				beaten = true
			}
		}
	}
	if !beaten {
		return closed
	}
	for _, pr := range open {
		if _, ok := r.securityToolchainPR(pr); ok {
			for head := range r.closeBeatenBy(pr, open) {
				closed[head] = true
			}
		}
	}
	return closed
}

// closeBeatenBy closes the open ripple PRs that are not security PRs and edit the toolchain
// line that the security toolchain PR pr edits, and returns their heads.
func (r *runner) closeBeatenBy(pr platform.PR, open []platform.PR) map[string]bool {
	closed := map[string]bool{}
	for _, old := range open {
		if old.Head == pr.Head {
			continue
		}
		for _, pkg := range []string{model.PackageGoDirective, model.PackageGoToolchain} {
			tail, ok := staleVersion(old.Head, pkg)
			if !ok {
				continue
			}
			key := model.JobKey(r.repo.FullName, model.DatasourceGoToolchain, pkg, tail)
			j, found, err := r.e.Store.GetJob(r.ctx, key)
			if err != nil || !found || j.State != model.JobDone || j.PRURL == "" || j.PRURL != old.URL || r.e.advisoryNoteFor(r.ctx, j) != nil {
				continue
			}
			comment := fmt.Sprintf("Closing: %s fixes a security advisory by editing the same toolchain line of this go.mod, so the two would conflict. ripple will propose this update again once that pull request is merged or closed.", pr.URL)
			done, err := platform.CloseUntouchedPR(r.ctx, r.e.Platform, r.repo, old, comment)
			switch {
			case err != nil:
				r.e.log().Warn("pull request beaten by a security update not closed", "repo", r.repo.FullName, "pr", old.URL, "err", err)
			case !done:
				r.e.log().Info("pull request left open beside a security update: it was touched by someone else", "repo", r.repo.FullName, "pr", old.URL)
			default:
				closed[old.Head] = true
				j.State, j.PRURL = model.JobPending, ""
				j.NotBefore = r.e.now().Add(deferBy)
				j.Reason = fmt.Sprintf("waiting for the security pull request %s: both edit the toolchain line of the same go.mod", pr.URL)
				if err := r.e.updateJob(r.ctx, r.hold, j); err != nil {
					r.e.log().Error("requeue job of a closed pull request", "job", j.Key, "err", err)
				}
				r.e.log().Info("closed a pull request beaten by a security toolchain update", "repo", r.repo.FullName, "pr", old.URL, "by", pr.URL)
			}
		}
	}
	return closed
}
