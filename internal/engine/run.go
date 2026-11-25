package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/module"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/rules"
)

const (
	maxAttempts = 5
	dueBatch    = 1000
	deferBy     = time.Hour
)

var unsafeBranch = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(s string) string {
	out := strings.Trim(unsafeBranch.ReplaceAllString(s, "-"), "-.")
	if out == "" {
		out = "x"
	}
	if len(out) > 100 {
		h := sha256.Sum256([]byte(s))
		out = out[:90] + "-" + hex.EncodeToString(h[:4])
	}
	return out
}

// item is one job resolved against the repo's current dependencies.
type item struct {
	job       model.Job
	deps      []model.Dependency
	automerge bool
	group     string
	typ       rules.UpdateType
	advisory  *advisoryNote // set when the job fixes a published advisory
	// bypassLimit is set when the job fixes an advisory and the update entry says its
	// security PRs skip the open pull request limit (security-bypass).
	bypassLimit bool
	// bypassCap is the update entry's security-bypass-limit-per-advisory (0: none).
	bypassCap int
}

// migrates reports whether the update renames the package (a Go major bump).
func (it item) migrates() bool {
	for _, d := range it.deps {
		if d.Package != it.job.Package {
			return true
		}
	}
	return false
}

// RunDue processes every job whose NotBefore has passed, one repo at a time per
// worker, and returns how many jobs reached a terminal state.
func (e *Engine) RunDue(ctx context.Context) (int, error) {
	jobs, err := e.Store.ListDueJobs(ctx, e.now(), e.dueBatch())
	if err != nil {
		return 0, err
	}
	byRepo := map[string][]model.Job{}
	var order []string
	for _, j := range jobs {
		if _, ok := byRepo[j.Repo]; !ok {
			order = append(order, j.Repo)
		}
		byRepo[j.Repo] = append(byRepo[j.Repo], j)
	}
	workers := e.Workers
	if workers < 1 {
		workers = 4
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	finished := 0
	for _, name := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			n, _ := e.runRepo(ctx, name, byRepo[name])
			mu.Lock()
			finished += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	e.flushDashboards(ctx)
	return finished, ctx.Err()
}

type runner struct {
	e        *Engine
	ctx      context.Context
	hold     *repoLease
	repo     model.Repo
	cfg      *config.Repo
	ms       []manager.Manager
	ws       platform.Workspace
	finished int
	// touched holds the module directories whose go.mod this PR edits.
	touched map[string]bool
	// migrations are the major bumps in this PR.
	migrations []migration
	// followed holds the files outside go.mod that took the Go version a toolchain bump moved to.
	followed []string
	// skipped holds Go files the import rewrite could not parse; the PR body lists them.
	skipped []string
	// ambiguous holds "file:line" of references to the old path in //go:generate lines
	// that were left alone; the PR body lists them.
	ambiguous []string
	// notes are lines the PR body gets after verification, e.g. what verification did not cover.
	notes []string
	// namedRelease is the Go release from the toolchain directory that verification ran
	// with, or "" when it ran with the server's own Go.
	namedRelease string
	// namedImage is the container image chosen for the go.mod's Go minor; empty when the
	// sandbox's default image ran.
	namedImage string
	// shas caches the commit a github-tags job's tag points at, by "package@tag".
	shas map[string]string
	// digests caches the manifest digest of a docker job's tag, by "package:tag".
	digests map[string]string
	// npmLocks holds the lockfiles beside the package.json files this PR edits.
	npmLocks map[string]bool
	// npmLockWhy is why ripple left each of those lockfiles to the owner; npmLocksDone
	// are the lockfiles it rewrote itself.
	npmLockWhy   map[string]string
	npmLocksDone map[string]bool
	// cargoLocks holds the Cargo.lock files that resolve the Cargo.toml files this PR edits.
	cargoLocks map[string]bool
	// helmLocks holds the Chart.lock and requirements.lock files that resolve the files this PR edits.
	helmLocks map[string]bool
	// gemLocks holds the Gemfile.lock files that resolve the Gemfiles this PR edits.
	gemLocks map[string]bool
	// packagistLocks holds the lock files that resolve the files this PR edits.
	packagistLocks map[string]bool
	// nugetLocks holds the lock files that resolve the files this PR edits.
	nugetLocks map[string]bool
	// pipLocks holds the lock files beside or above the Python files this PR edits.
	pipLocks map[string]bool
	// skipReasons says why a job was skipped when it is not the common "changed since indexed".
	skipReasons map[string]string
}

func (r *runner) finish(j model.Job, state model.JobState, reason, pr string) {
	j.State, j.Reason, j.PRURL = state, reason, pr
	if err := r.e.updateJob(r.ctx, r.hold, j); err != nil {
		r.e.log().Error("update job", "job", j.Key, "err", err)
		return
	}
	r.finished++
}

func (r *runner) deferJob(j model.Job, until time.Time, reason string) {
	j.NotBefore, j.Reason = until, reason
	if err := r.e.updateJob(r.ctx, r.hold, j); err != nil {
		r.e.log().Error("defer job", "job", j.Key, "err", err)
	}
}

func (r *runner) fail(j model.Job, err error) {
	if errors.Is(err, ErrLeaseLost) {
		return // another replica owns the repo now; this run's outcome is not the job's
	}
	j.Attempts++
	j.Reason = err.Error()
	var rl *platform.RateLimitError
	switch {
	case errors.As(err, &rl):
		j.Attempts--
		j.NotBefore = rl.Until
	case errors.Is(err, platform.ErrUnsafeCheckout), j.Attempts >= maxAttempts:
		j.State = model.JobFailed
		r.finished++
	default:
		j.NotBefore = r.e.now().Add(time.Duration(j.Attempts) * 5 * time.Minute)
	}
	if uerr := r.e.updateJob(r.ctx, r.hold, j); uerr != nil {
		r.e.log().Error("update job", "job", j.Key, "err", uerr)
	}
	r.e.log().Warn("job failed", "job", j.Key, "attempt", j.Attempts, "err", err)
}

// runRepo runs the jobs of one repo and returns how many reached a terminal state. The
// second result is true when another replica holds the repo, in which case nothing ran and
// the jobs are left as they were.
func (e *Engine) runRepo(ctx context.Context, name string, jobs []model.Job) (int, bool) {
	defer e.lockRepo(name)()
	hold, err := e.acquireRepo(ctx, name, false)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			e.log().Error("claim repo", "repo", name, "err", err)
		}
		return 0, false
	case hold == nil:
		return 0, true
	}
	defer hold.Release()
	ctx = hold.Ctx
	// The listing these jobs came from may predate another replica's run of the repo.
	if jobs = e.stillDue(ctx, jobs); len(jobs) == 0 {
		return 0, false
	}
	return e.runClaimed(ctx, hold, name, jobs), false
}

// stillDue re-reads each job under the repo's lease and keeps the ones that are still
// pending and due: a replica that listed them before another finished them must not run
// them again (a merged PR would be reopened).
func (e *Engine) stillDue(ctx context.Context, jobs []model.Job) []model.Job {
	now := e.now()
	var out []model.Job
	for _, j := range jobs {
		cur, ok, err := e.Store.GetJob(ctx, j.Key)
		if err != nil {
			if ctx.Err() == nil {
				e.log().Error("get job", "job", j.Key, "err", err)
			}
			continue
		}
		if ok && cur.State == model.JobPending && !cur.NotBefore.After(now) {
			out = append(out, cur)
		}
	}
	return out
}

func (e *Engine) runClaimed(ctx context.Context, hold *repoLease, name string, jobs []model.Job) int {
	if e.Paused(ctx, name) {
		// Archived: read-only. Keep the jobs and look again later; Resume makes them due.
		for _, j := range jobs {
			j.NotBefore, j.Reason = e.now().Add(pauseDefer), pausedReason
			if err := e.updateJob(ctx, hold, j); err != nil {
				e.log().Error("defer job of archived repo", "job", j.Key, "err", err)
			}
		}
		return 0
	}
	defer func() { e.syncDashboard(ctx, name) }()
	r := &runner{e: e, ctx: ctx, hold: hold}
	var err error
	var found bool
	if r.repo, found, err = e.Store.GetRepo(ctx, name); err != nil {
		e.log().Error("get repo", "repo", name, "err", err)
		return 0
	}
	if !found {
		if target, ok := e.followRename(ctx, hold, name, jobs); ok {
			e.log().Info("jobs followed a renamed repository", "from", name, "to", target, "jobs", len(jobs))
			return 0
		}
		for _, j := range jobs {
			r.finish(j, model.JobSkipped, "repository no longer registered", "")
		}
		return r.finished
	}
	if r.cfg, err = RepoConfig(r.repo.Config); err != nil {
		for _, j := range jobs {
			r.finish(j, model.JobSkipped, "repository config is invalid", "")
		}
		return r.finished
	}
	if r.ms, err = manager.Build(r.cfg); err != nil {
		for _, j := range jobs {
			r.finish(j, model.JobSkipped, err.Error(), "")
		}
		return r.finished
	}
	deps, err := e.Store.ListRepoDependencies(ctx, name)
	if err != nil {
		e.log().Error("list dependencies", "repo", name, "err", err)
		return 0
	}
	plans := r.plan(jobs, deps)
	if len(plans) == 0 {
		return r.finished
	}
	defer func() {
		if r.ws != nil {
			r.ws.Close()
		}
	}()
	r.execute(plans, deps)
	return r.finished
}

type prPlan struct {
	branch string
	group  string
	items  []item
	// draft is set by openPR when the PR it opened is a draft (verification failed).
	draft bool
}

// plan resolves jobs against current dependencies and groups them into PRs.
func (r *runner) plan(jobs []model.Job, deps []model.Dependency) []*prPlan {
	byBranch := map[string]*prPlan{}
	var order []*prPlan
	now := r.e.now()
	for _, j := range jobs {
		it := item{job: j, automerge: true, typ: rules.Patch, advisory: r.e.advisoryNoteFor(r.ctx, j)}
		var notBefore time.Time
		ignored, pseudo := false, false
		for _, d := range deps {
			u, haveU := r.cfg.UpdateFor(d.Manager, d.File)
			typ, ok := applicable(d, j.Datasource, j.Package, j.Version)
			if !ok && it.advisory != nil && it.advisory.Pseudo && haveU && u.SecurityPseudoVersions {
				// The advisory's only fix is a pseudo-version, which the repo opted in to.
				typ, ok = applicablePseudoFix(d, j.Package, j.Version)
				pseudo = pseudo || ok
			}
			if !ok && it.advisory != nil && haveU && u.SecurityFromPseudoVersions {
				// The dependency is at a pseudo-version and a release fixes its advisory.
				typ, ok = applicablePseudoCurrent(d, j.Package, j.Version)
				pseudo = pseudo || ok
			}
			if !ok && it.advisory == nil && haveU && u.FromPseudoVersions {
				// An ordinary release for a dependency at a pseudo-version the entry opted to move.
				typ, ok = applicablePseudoRelease(d, j.Package, j.Version, j.ReleasedAt)
				pseudo = pseudo || ok
			}
			if !ok || !haveU {
				continue
			}
			dec := rules.Evaluate(r.e.Limits, u, d.Package, typ, j.ReleasedAt, now)
			if it.advisory != nil {
				dec = rules.EvaluateSecuritySeverity(r.e.Limits, u, d.Package, typ, now, it.advisory.Severity)
			}
			if !dec.Enabled {
				ignored = true
				continue
			}
			if it.advisory != nil && u.BypassesSeverity(config.BypassLimit, it.advisory.Severity) {
				it.bypassLimit = true
				if u.SecurityBypassLimitPerAdvisory > 0 && (it.bypassCap == 0 || u.SecurityBypassLimitPerAdvisory < it.bypassCap) {
					it.bypassCap = u.SecurityBypassLimitPerAdvisory
				}
			}
			if dec.NotBefore.After(notBefore) {
				notBefore = dec.NotBefore
			}
			it.deps = append(it.deps, d)
			it.typ = rules.Max(it.typ, typ)
			it.automerge = it.automerge && dec.Automerge
			if it.group == "" {
				it.group = dec.Group
			}
		}
		if pseudo {
			it.automerge = false // a commit, not a release: a person looks at it
		}
		switch {
		case len(it.deps) == 0 && ignored:
			r.finish(j, model.JobSkipped, "ignored by the repository config", "")
			continue
		case len(it.deps) == 0:
			r.finish(j, model.JobSkipped, "dependency already up to date or no longer used", "")
			continue
		case notBefore.After(now):
			r.deferJob(j, notBefore, "waiting for cooldown or schedule")
			continue
		}
		branch := BranchPrefix + "go/" + slug(j.Package) + "-" + slug(j.Version)
		if j.Datasource == model.DatasourceGitHubTags {
			branch = BranchPrefix + "actions/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceDocker {
			branch = BranchPrefix + "docker/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourcePip {
			branch = BranchPrefix + "pip/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceCargo {
			branch = BranchPrefix + "cargo/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceHelm {
			branch = BranchPrefix + "helm/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceRubygems {
			branch = BranchPrefix + "rubygems/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourcePackagist {
			branch = BranchPrefix + "packagist/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceNuget {
			branch = BranchPrefix + "nuget/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceMaven {
			branch = BranchPrefix + "maven/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if j.Datasource == model.DatasourceNpm {
			branch = BranchPrefix + "npm/" + slug(j.Package) + "-" + slug(j.Version)
		}
		if it.group != "" {
			branch = BranchPrefix + "group/" + slug(it.group)
		}
		p := byBranch[branch]
		if p == nil {
			p = &prPlan{branch: branch, group: it.group}
			byBranch[branch] = p
			order = append(order, p)
		}
		p.items = append(p.items, it)
	}
	return order
}

// bypassesLimit reports whether the PR may open at the open pull request limit: every
// item fixes an advisory of an entry that opted in with security-bypass.
func (p *prPlan) bypassesLimit() bool {
	for _, it := range p.items {
		if !it.bypassLimit {
			return false
		}
	}
	return len(p.items) > 0
}

// securityFirst puts the plans that bypass the limit ahead of the others, so that at the
// limit a security PR is not beaten to the last slot by an ordinary update. The order is
// otherwise kept, and nothing moves when no plan bypasses.
func securityFirst(plans []*prPlan) []*prPlan {
	first := make([]*prPlan, 0, len(plans))
	var rest []*prPlan
	for _, p := range plans {
		if p.bypassesLimit() {
			first = append(first, p)
		} else {
			rest = append(rest, p)
		}
	}
	if len(first) == 0 {
		return plans
	}
	return append(first, rest...)
}

func (r *runner) execute(plans []*prPlan, deps []model.Dependency) {
	e := r.e
	if until := platform.RateLimitedUntil(e.Platform, r.repo); until.After(e.now()) {
		// The host asked to be left alone; spend no call on this repo until then.
		for _, p := range plans {
			for _, it := range p.items {
				r.deferJob(it.job, until, "rate limited")
			}
		}
		return
	}
	open, err := e.Platform.ListOpenPRs(r.ctx, r.repo, BranchPrefix)
	if err != nil {
		for _, p := range plans {
			for _, it := range p.items {
				r.fail(it.job, err)
			}
		}
		return
	}
	if closed := r.sweepSuperseded(deps, open); len(closed) > 0 {
		open = without(open, closed)
	}
	if closed := r.sweepBeaten(open); len(closed) > 0 {
		open = without(open, closed)
	}
	plans = r.holdForSecurity(plans, deps, open)
	heads := map[string]platform.PR{}
	for _, pr := range open {
		heads[pr.Head] = pr
	}
	limit := rules.EffectivePRLimit(e.Limits, r.cfg)
	count := len(open)
	plans = securityFirst(plans)
	relinked := false
	for pi, p := range plans {
		if pr, ok := heads[p.branch]; ok {
			for _, it := range p.items {
				if p.group != "" {
					r.deferJob(it.job, e.now().Add(deferBy), "group PR is open; waiting for it to be merged or closed")
				} else {
					r.finish(it.job, model.JobDone, "pull request already open", pr.URL)
					relinked = true
				}
			}
			continue
		}
		if count >= limit && p.group == "" && len(p.items) > 0 {
			// A stale PR of the module this plan updates holds the slot its replacement needs.
			j := p.items[0].job
			closed := r.closeStale(supersededBy{ds: j.Datasource, pkg: j.Package, version: j.Version}, p.branch, open)
			open, count = without(open, closed), count-len(closed)
		}
		if count >= limit && !(p.bypassesLimit() && !r.overAdvisoryCap(p, open)) {
			for _, it := range p.items {
				r.deferJob(it.job, e.now().Add(deferBy), fmt.Sprintf("open pull request limit (%d) reached", limit))
			}
			continue
		}
		pr, err := r.openPR(p)
		if errors.Is(err, ErrLeaseLost) || errors.Is(context.Cause(r.ctx), ErrLeaseLost) {
			return // another replica owns the repo; its run handles the rest
		}
		if errors.Is(err, errWorkflowsPermission) {
			// Retrying cannot help until someone grants the permission, and granting
			// it should need no other step: look again tomorrow, with the reason shown.
			for _, it := range p.items {
				r.deferJob(it.job, e.now().Add(24*time.Hour), workflowsReason)
			}
			e.log().Warn(workflowsReason, "repo", r.repo.FullName, "branch", p.branch)
			continue
		}
		if err != nil {
			var rl *platform.RateLimitError
			for _, it := range p.items {
				r.fail(it.job, err)
			}
			if errors.As(err, &rl) {
				for _, rest := range plans[pi+1:] {
					for _, it := range rest.items {
						r.deferJob(it.job, rl.Until, "rate limited")
					}
				}
				return
			}
			continue
		}
		if pr != nil {
			count++
			if !p.draft {
				// A draft that failed verification is no replacement for a PR that may be fine.
				r.supersede(p, *pr, open)
				r.closeBeaten(p, *pr, open)
			}
			// Later plans of this run see it: a module with advisories fixed in several
			// releases opens one plan per fix, and the highest must replace the lower ones
			// this run opened a moment ago.
			open = append(open, *pr)
		}
	}
	if relinked {
		// A job that found its PR already open recorded it only now (a rerun clears the
		// record), and the sweeps at the start of the run skip a PR no job owned yet. Judge
		// the open PRs again so a lower PR is not left until some later run has plans.
		if closed := r.sweepSuperseded(deps, open); len(closed) > 0 {
			open = without(open, closed)
		}
		r.sweepBeaten(open)
	}
}

// openPR applies a plan in a clean checkout, pushes its branch and opens the PR.
// It returns nil, nil when every job turned out to be a no-op.
func (r *runner) openPR(p *prPlan) (*platform.PR, error) {
	e := r.e
	if r.ws == nil {
		ws, err := e.Platform.Checkout(r.ctx, r.repo)
		if err != nil {
			return nil, err
		}
		r.ws = ws
	} else if err := r.ws.Reset(r.ctx); err != nil {
		return nil, err
	}
	r.touched = map[string]bool{}
	r.migrations = nil
	r.skipped = nil
	r.ambiguous = nil
	r.followed = nil
	r.npmLocks, r.npmLockWhy, r.npmLocksDone = nil, nil, nil
	r.cargoLocks = nil
	r.helmLocks = nil
	r.gemLocks = nil
	r.packagistLocks = nil
	r.nugetLocks = nil
	r.pipLocks = nil
	var applied []item
	for _, it := range p.items {
		ok, err := r.apply(it)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.job.Package, err)
		}
		if !ok {
			reason := "dependency changed since it was indexed"
			if why := r.skipReasons[it.job.Key]; why != "" {
				reason = why
			}
			r.finish(it.job, model.JobSkipped, reason, "")
			continue
		}
		applied = append(applied, it)
	}
	if len(applied) == 0 {
		return nil, nil
	}
	title, body := describe(p, applied)
	title, body = securityText(applied, title, body)
	if err := r.rewriteWorks(); err != nil {
		return nil, err
	}
	var draft bool
	if len(r.followed) > 0 {
		sort.Strings(r.followed)
		body += "\n## Go version in other files\n\nThese files named the old Go version on a line ripple recognises exactly, and were moved with it. Lines it does not recognise (matrix expressions, aliases such as `stable`, image digests) were left alone:\n\n"
		for _, f := range r.followed {
			body += "- `" + f + "`\n"
		}
	}
	if len(r.skipped) > 0 {
		sort.Strings(r.skipped)
		body += "\n## Files not rewritten\n\nThese Go files do not parse, so their imports were left as they were. Fix them by hand if they import the old path:\n\n"
		for _, f := range r.skipped {
			body += "- `" + f + "`\n"
		}
	}
	if len(r.ambiguous) > 0 {
		sort.Strings(r.ambiguous)
		body += "\n## Old path left in place\n\nThese //go:generate lines still mention the old import path. They are not a plain `go run <package>` (a version suffix, quoting, a variable, another command or a program argument), so they were left as they were. Check them by hand:\n\n"
		for _, f := range r.ambiguous {
			body += "- `" + f + "`\n"
		}
	}
	if len(r.touched) > 0 {
		var olds []string
		for _, it := range applied {
			for _, d := range it.deps {
				olds = append(olds, d.Package)
			}
		}
		failure, err := r.verify(olds)
		major := len(r.migrations) > 0
		switch {
		case errors.Is(err, platform.ErrNoToolchain) && major:
			failure, err = "ripple could not verify this update: the go toolchain is not installed on the server.", nil
		case errors.Is(err, platform.ErrNoCompiler) && major:
			failure, err = "ripple could not verify this update: this module needs cgo (`cgo: true`) and no C compiler is available on the server.", nil
		case errors.Is(err, platform.ErrNoCompiler):
			body += "\n_ripple could not verify this update: this module needs cgo (`cgo: true`) and no C compiler is available on the server. go.sum was refreshed; the code was not built._\n"
			failure, err = "", nil
		case errors.Is(err, platform.ErrNoToolchain):
			body += "\n_ripple could not refresh go.sum or verify this update: the go toolchain is not installed on the server._\n"
			failure, err = "", nil
		}
		if err != nil {
			return nil, err
		}
		draft = failure != ""
		p.draft = draft
		if r.namedRelease != "" {
			body = strings.Replace(body, serverGoSentence, "ripple checked `go mod tidy`, `go build ./...` and `go vet ./...` with go"+r.namedRelease+" from the server's toolchain directory, the release this go.mod names; CI on this pull request remains the full check.", 1)
		}
		if r.namedImage != "" {
			body = strings.Replace(body, serverGoSentence, "ripple checked `go mod tidy`, `go build ./...` and `go vet ./...` with the Go in the container image `"+r.namedImage+"`, the one configured for the Go minor this go.mod names; CI on this pull request remains the full check.", 1)
		}
		if !draft {
			for _, n := range r.notes {
				body += "\n" + n + "\n"
			}
		}
		if draft {
			what := "The go.mod change was applied but the module does not build"
			if major {
				what = "The import rewrite was applied but the module does not build"
			}
			body += "\n## Verification failed\n\n" + what + ". This pull request is a draft; it needs a human.\n\n```\n" + failure + "\n```\n"
		}
	}
	body += r.actionNotes(applied)
	body += r.npmNotes()
	body += r.cargoNotes()
	body += r.helmNotes()
	body += r.gemNotes()
	body += r.packagistNotes()
	body += r.nugetNotes()
	body += r.pipNotes()
	changed, err := r.ws.CommitAndPush(r.ctx, p.branch, title)
	if err != nil {
		if touchesWorkflows(applied) && workflowsDenied(err) {
			return nil, errWorkflowsPermission
		}
		return nil, err
	}
	if !changed {
		for _, it := range applied {
			r.finish(it.job, model.JobSkipped, "no file changed", "")
		}
		return nil, nil
	}
	// The branch is pushed; opening the PR is the step a second replica must not repeat, so
	// confirm the lease is still ours right before it. A replica that lost it stops here and
	// the new holder finds the branch and opens the one PR.
	if err := r.hold.Fence(); err != nil {
		return nil, err
	}
	pr, err := e.Platform.CreatePR(r.ctx, r.repo, platform.PRSpec{Head: p.branch, Base: r.ws.DefaultBranch(), Title: title, Body: body, Draft: draft, Labels: labelsFor(applied)})
	if err != nil {
		return nil, err
	}
	auto := !draft
	for _, it := range applied {
		auto = auto && it.automerge
	}
	if auto {
		switch err := platform.RequestAutoMerge(r.ctx, e.Platform, r.repo, pr); {
		case errors.Is(err, platform.ErrNoAutoMerge):
			e.log().Info("host has no auto-merge; pull request left for review", "repo", r.repo.FullName, "pr", pr.URL)
		case err != nil:
			e.log().Warn("auto-merge not enabled", "repo", r.repo.FullName, "pr", pr.URL, "err", err)
		}
	}
	// Hash what the host stored when it says (Azure DevOps truncates a long description);
	// otherwise what was sent.
	sent := body
	if pr.Body != "" {
		sent = pr.Body
	}
	bodyHash := model.BodyHash(sent)
	for _, it := range applied {
		j := it.job
		j.HeadSHA, j.BodyHash = pr.HeadSHA, bodyHash
		r.finish(j, model.JobDone, "", pr.URL)
	}
	e.log().Info("opened pull request", "repo", r.repo.FullName, "pr", pr.URL, "jobs", len(applied))
	return &pr, nil
}

func (r *runner) apply(it item) (bool, error) {
	touched := false
	for _, d := range it.deps {
		m, ok := manager.ByName(r.ms, d.Manager)
		if !ok {
			continue
		}
		content, err := r.ws.ReadFile(d.File)
		if err != nil {
			return false, err
		}
		version := it.job.Version
		if d.Datasource == model.DatasourceGitHubTags {
			version = r.actionVersion(it.job.Package, version)
		}
		if d.Datasource == model.DatasourceDocker {
			version = r.imageVersion(it.job.Package, version)
		}
		if d.Datasource == model.DatasourcePip {
			if why := r.pipGuard(d, it.job.Version); why != "" {
				why += r.pipRetarget(it.job, d)
				if r.skipReasons == nil {
					r.skipReasons = map[string]string{}
				}
				r.skipReasons[it.job.Key] = why
				continue
			}
		}
		if d.Datasource == model.DatasourceCargo {
			if why := r.cargoGuard(d, it.job.Version); why != "" {
				why += r.cargoRetarget(it.job, d)
				if r.skipReasons == nil {
					r.skipReasons = map[string]string{}
				}
				r.skipReasons[it.job.Key] = why
				continue
			}
		}
		if d.Datasource == model.DatasourceNpm {
			if why := r.npmGuard(d); why != "" {
				if r.skipReasons == nil {
					r.skipReasons = map[string]string{}
				}
				r.skipReasons[it.job.Key] = why
				continue
			}
		}
		out, err := m.Apply(content, d, it.job.Package, version)
		if errors.Is(err, manager.ErrStale) {
			continue
		}
		if err != nil {
			return false, err
		}
		if d.Datasource == model.DatasourceNpm {
			r.noteLockfiles(d, version)
		}
		if d.Datasource == model.DatasourceCargo {
			r.noteCargoLock(d)
		}
		if d.Datasource == model.DatasourceHelm {
			r.noteHelmLock(d)
		}
		if d.Datasource == model.DatasourceRubygems {
			r.noteGemLock(d)
		}
		if d.Datasource == model.DatasourcePackagist {
			r.notePackagistLock(d)
		}
		if d.Datasource == model.DatasourceNuget {
			r.noteNugetLock(d)
		}
		if d.Datasource == model.DatasourcePip {
			r.notePipLocks(d)
		}
		if d.Package != it.job.Package {
			u, _ := r.cfg.UpdateFor("gomod", d.File)
			res, err := manager.RewriteGoWith(r.ws, d.File, d.Package, it.job.Package, content,
				manager.GoRewriteOptions{Extended: u.Directives == "extended", NewVersion: it.job.Version})
			if err != nil {
				return false, err
			}
			r.skipped = append(r.skipped, res.Skipped...)
			r.ambiguous = append(r.ambiguous, res.Ambiguous...)
			r.migrations = append(r.migrations, migration{d.Package, it.job.Package})
		}
		if err := r.ws.WriteFile(d.File, out); err != nil {
			return false, err
		}
		if d.Manager == "gomod" {
			r.touched[path.Dir(d.File)] = true
		}
		if d.Datasource == model.DatasourceGoToolchain {
			if err := r.followGoVersion(d, it.job.Version); err != nil {
				return false, err
			}
		}
		touched = true
	}
	return touched, nil
}

func labelsFor(items []item) []string {
	var labels []string
	for _, it := range items {
		if it.typ == rules.Major {
			labels = []string{"major", "breaking"}
			break
		}
	}
	return withSecurityLabel(items, labels)
}

// releaseNotes links to where a human reads what changed: the GitHub release for
// github.com modules, the package documentation otherwise.
func releaseNotes(pkg, version string) string {
	if rest, ok := strings.CutPrefix(pkg, "github.com/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 {
			return fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", parts[0], parts[1], version)
		}
	}
	return fmt.Sprintf("https://pkg.go.dev/%s@%s", pkg, version)
}

// footerSource says where the new version was seen. Each manager says what is true
// of its own datasource; a PR that mixes datasources says only that it came from
// upstream.
func footerSource(items []item) string {
	phrase := ""
	for i, it := range items {
		p := datasourceFooter(it.job.Datasource)
		if i > 0 && p != phrase {
			return "a new version appeared upstream"
		}
		phrase = p
	}
	return phrase
}

// datasourceFooter is the footer phrase for a PR made of one datasource.
func datasourceFooter(datasource string) string {
	switch datasource {
	case model.DatasourceGitHubTags:
		return "a new tag appeared upstream"
	case model.DatasourceDocker:
		return "a new tag appeared on the registry"
	case model.DatasourceNpm:
		return "a new version was published to the npm registry"
	case model.DatasourcePip:
		return "a new version was published to PyPI"
	case model.DatasourceHelm:
		return "a new version appeared in the chart repository"
	case model.DatasourceRubygems:
		return "a new version appeared on rubygems.org"
	case model.DatasourcePackagist:
		return "a new version appeared on Packagist"
	case model.DatasourceNuget:
		return "a new version appeared on nuget.org"
	case model.DatasourceMaven:
		return "a new version appeared on Maven Central"
	}
	return "the new version appeared in the module index"
}

func describe(p *prPlan, items []item) (title, body string) {
	var b strings.Builder
	sort.Slice(items, func(i, j int) bool { return items[i].job.Package < items[j].job.Package })
	b.WriteString("| Package | From | To | Released | File |\n|---|---|---|---|---|\n")
	for _, it := range items {
		for _, d := range it.deps {
			name, from, to := d.Package, d.Version, it.job.Version
			if d.Datasource == model.DatasourceGoToolchain {
				name, from, to = toolchainRow(d, it.job.Version)
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | `%s` |\n", name, from, to, it.job.ReleasedAt.UTC().Format("2006-01-02 15:04 MST"), d.File)
		}
	}
	b.WriteString(toolchainNotes(items))
	for _, it := range items {
		if it.advisory != nil {
			continue // the security text says it
		}
		for _, d := range it.deps {
			if d.Datasource == model.DatasourceGo && module.IsPseudoVersion(d.Version) && !module.IsPseudoVersion(it.job.Version) {
				fmt.Fprintf(&b, "\n`%s` moves off the pseudo-version `%s` the module is required at (`from-pseudo-versions`); check that the commit's changes are in the release.\n", d.Package, d.Version)
				break
			}
		}
	}
	for _, it := range items {
		if it.typ == rules.Major {
			fmt.Fprintf(&b, "\n**Breaking: major version update.** `%s` moves to `%s`; go.mod and the import paths in the Go files were rewritten. Release notes: %s\n", it.deps[0].Package, it.job.Package, releaseNotes(it.job.Package, it.job.Version))
		}
	}
	b.WriteString("\nOpened by [ripple](https://github.com/taumatix/ripple) when " + footerSource(items) + ". Configure it in `.ripple.yaml`.\n")
	if len(items) == 1 && p.group == "" {
		if items[0].job.Datasource == model.DatasourceGoToolchain {
			_, _, to := toolchainRow(items[0].deps[0], items[0].job.Version)
			if items[0].job.Package == model.PackageGoDirective {
				return "Update the go directive to " + to, b.String()
			}
			return "Update the Go toolchain to " + to, b.String()
		}
		if items[0].typ == rules.Major {
			return fmt.Sprintf("[major] Update %s to %s", items[0].job.Package, items[0].job.Version), b.String()
		}
		return fmt.Sprintf("Update %s to %s", items[0].job.Package, items[0].job.Version), b.String()
	}
	name := p.group
	return fmt.Sprintf("Update %s dependencies (%d)", name, len(items)), b.String()
}

// toolchainRow is how a go directive or toolchain line update reads in the PR table:
// what changes, from what, to what, spelled as go.mod spells them.
func toolchainRow(d model.Dependency, target string) (name, from, to string) {
	if d.Package == model.PackageGoDirective {
		return "go directive", strings.TrimPrefix(d.Version, "v"), manager.GoDirective(d.Version, target)
	}
	return "toolchain line", "go" + strings.TrimPrefix(d.Version, "v"), "go" + strings.TrimPrefix(target, "v")
}

// serverGoSentence is what a toolchain PR says about verification; a run that used a
// release from the toolchain directory replaces it (see runner.namedGo).
const serverGoSentence = "ripple checked `go mod tidy`, `go build ./...` and `go vet ./...` with the Go installed on its server (toolchain downloads are off), which need not be the version named above; CI on this pull request is the check against that version."

// toolchainNotes explains, for a PR that moves the go directive or toolchain line,
// what the change asks of others and what verification did and did not cover.
func toolchainNotes(items []item) string {
	var directive, toolchain bool
	for _, it := range items {
		if it.job.Datasource != model.DatasourceGoToolchain {
			continue
		}
		directive = directive || it.job.Package == model.PackageGoDirective
		toolchain = toolchain || it.job.Package == model.PackageGoToolchain
	}
	var b strings.Builder
	if directive {
		b.WriteString("\n**The go directive is the least Go version this module needs.** Raising it raises the Go version needed by every module that depends on this one. ripple proposes it only because the old release is no longer supported by the Go project, and moves to the oldest release that still is.\n")
	}
	if toolchain {
		b.WriteString("\nThe toolchain line only selects the Go used to build this module; dependants do not read it.\n")
	}
	if directive || toolchain {
		b.WriteString("\n" + serverGoSentence + "\n")
	}
	return b.String()
}

// followGoVersion moves the Go version named beside a go.mod whose toolchain line or
// directive just moved from d.Version to target: the files in the module directory and
// the repo root, and the root workflows. Only a repo that opted in with go-toolchain
// reaches here, since only then is there a toolchain job at all.
func (r *runner) followGoVersion(d model.Dependency, target string) error {
	dirs := map[string]bool{".": true, path.Dir(d.File): true}
	var files []string
	err := r.ws.Walk(func(p string) error {
		if manager.HoldsGoVersion(p) && (dirs[path.Dir(p)] || manager.HoldsGoVersionRepoWide(p)) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range files {
		content, err := r.ws.ReadFile(p)
		if err != nil {
			return err
		}
		out, ok := manager.FollowGoVersion(p, content, d.Version, target)
		if !ok {
			continue
		}
		if err := r.ws.WriteFile(p, out); err != nil {
			return err
		}
		r.followed = append(r.followed, p)
	}
	return nil
}

// without is open minus the PRs whose heads are in closed.
func without(open []platform.PR, closed map[string]bool) []platform.PR {
	if len(closed) == 0 {
		return open
	}
	var out []platform.PR
	for _, pr := range open {
		if !closed[pr.Head] {
			out = append(out, pr)
		}
	}
	return out
}
