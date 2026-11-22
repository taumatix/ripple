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
	jobs, err := e.Store.ListDueJobs(ctx, e.now(), dueBatch)
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
			n := e.runRepo(ctx, name, byRepo[name])
			mu.Lock()
			finished += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	return finished, ctx.Err()
}

type runner struct {
	e        *Engine
	ctx      context.Context
	repo     model.Repo
	cfg      *config.Repo
	ms       []manager.Manager
	ws       platform.Workspace
	finished int
	// touched holds the module directories whose go.mod this PR edits.
	touched map[string]bool
	// migrations are the major bumps in this PR.
	migrations []migration
	// skipped holds Go files the import rewrite could not parse; the PR body lists them.
	skipped []string
	// ambiguous holds "file:line" of references to the old path in //go:generate lines
	// that were left alone; the PR body lists them.
	ambiguous []string
}

func (r *runner) finish(j model.Job, state model.JobState, reason, pr string) {
	j.State, j.Reason, j.PRURL = state, reason, pr
	if err := r.e.Store.UpdateJob(r.ctx, j); err != nil {
		r.e.log().Error("update job", "job", j.Key, "err", err)
		return
	}
	r.finished++
}

func (r *runner) deferJob(j model.Job, until time.Time, reason string) {
	j.NotBefore, j.Reason = until, reason
	if err := r.e.Store.UpdateJob(r.ctx, j); err != nil {
		r.e.log().Error("defer job", "job", j.Key, "err", err)
	}
}

func (r *runner) fail(j model.Job, err error) {
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
	if uerr := r.e.Store.UpdateJob(r.ctx, j); uerr != nil {
		r.e.log().Error("update job", "job", j.Key, "err", uerr)
	}
	r.e.log().Warn("job failed", "job", j.Key, "attempt", j.Attempts, "err", err)
}

func (e *Engine) runRepo(ctx context.Context, name string, jobs []model.Job) int {
	defer e.lockRepo(name)()
	r := &runner{e: e, ctx: ctx}
	var err error
	var found bool
	if r.repo, found, err = e.Store.GetRepo(ctx, name); err != nil {
		e.log().Error("get repo", "repo", name, "err", err)
		return 0
	}
	if !found {
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
	r.execute(plans)
	return r.finished
}

type prPlan struct {
	branch string
	group  string
	items  []item
}

// plan resolves jobs against current dependencies and groups them into PRs.
func (r *runner) plan(jobs []model.Job, deps []model.Dependency) []*prPlan {
	byBranch := map[string]*prPlan{}
	var order []*prPlan
	now := r.e.now()
	for _, j := range jobs {
		it := item{job: j, automerge: true, typ: rules.Patch}
		var notBefore time.Time
		ignored := false
		for _, d := range deps {
			typ, ok := applicable(d, j.Datasource, j.Package, j.Version)
			if !ok {
				continue
			}
			u, ok := r.cfg.UpdateFor(d.Manager, d.File)
			if !ok {
				continue
			}
			dec := rules.Evaluate(r.e.Limits, u, d.Package, typ, j.ReleasedAt, now)
			if !dec.Enabled {
				ignored = true
				continue
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

func (r *runner) execute(plans []*prPlan) {
	e := r.e
	open, err := e.Platform.ListOpenPRs(r.ctx, r.repo, BranchPrefix)
	if err != nil {
		for _, p := range plans {
			for _, it := range p.items {
				r.fail(it.job, err)
			}
		}
		return
	}
	heads := map[string]platform.PR{}
	for _, pr := range open {
		heads[pr.Head] = pr
	}
	limit := rules.EffectivePRLimit(e.Limits, r.cfg)
	count := len(open)
	for pi, p := range plans {
		if pr, ok := heads[p.branch]; ok {
			for _, it := range p.items {
				if p.group != "" {
					r.deferJob(it.job, e.now().Add(deferBy), "group PR is open; waiting for it to be merged or closed")
				} else {
					r.finish(it.job, model.JobDone, "pull request already open", pr.URL)
				}
			}
			continue
		}
		if count >= limit {
			for _, it := range p.items {
				r.deferJob(it.job, e.now().Add(deferBy), fmt.Sprintf("open pull request limit (%d) reached", limit))
			}
			continue
		}
		pr, err := r.openPR(p)
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
		}
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
	var applied []item
	for _, it := range p.items {
		ok, err := r.apply(it)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.job.Package, err)
		}
		if !ok {
			r.finish(it.job, model.JobSkipped, "dependency changed since it was indexed", "")
			continue
		}
		applied = append(applied, it)
	}
	if len(applied) == 0 {
		return nil, nil
	}
	title, body := describe(p, applied)
	if err := r.rewriteWorks(); err != nil {
		return nil, err
	}
	var draft bool
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
		case errors.Is(err, platform.ErrNoToolchain):
			body += "\n_ripple could not refresh go.sum or verify this update: the go toolchain is not installed on the server._\n"
			failure, err = "", nil
		}
		if err != nil {
			return nil, err
		}
		draft = failure != ""
		if draft {
			what := "The go.mod change was applied but the module does not build"
			if major {
				what = "The import rewrite was applied but the module does not build"
			}
			body += "\n## Verification failed\n\n" + what + ". This pull request is a draft; it needs a human.\n\n```\n" + failure + "\n```\n"
		}
	}
	changed, err := r.ws.CommitAndPush(r.ctx, p.branch, title)
	if err != nil {
		return nil, err
	}
	if !changed {
		for _, it := range applied {
			r.finish(it.job, model.JobSkipped, "no file changed", "")
		}
		return nil, nil
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
		if err := e.Platform.EnableAutoMerge(r.ctx, r.repo, pr); err != nil {
			e.log().Warn("auto-merge not enabled", "repo", r.repo.FullName, "pr", pr.URL, "err", err)
		}
	}
	for _, it := range applied {
		r.finish(it.job, model.JobDone, "", pr.URL)
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
		out, err := m.Apply(content, d, it.job.Package, it.job.Version)
		if errors.Is(err, manager.ErrStale) {
			continue
		}
		if err != nil {
			return false, err
		}
		if d.Package != it.job.Package {
			res, err := manager.RewriteGo(r.ws, d.File, d.Package, it.job.Package, content)
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
		touched = true
	}
	return touched, nil
}

func labelsFor(items []item) []string {
	for _, it := range items {
		if it.typ == rules.Major {
			return []string{"major", "breaking"}
		}
	}
	return nil
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

func describe(p *prPlan, items []item) (title, body string) {
	var b strings.Builder
	sort.Slice(items, func(i, j int) bool { return items[i].job.Package < items[j].job.Package })
	b.WriteString("| Package | From | To | Released | File |\n|---|---|---|---|---|\n")
	for _, it := range items {
		for _, d := range it.deps {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | `%s` |\n", d.Package, d.Version, it.job.Version, it.job.ReleasedAt.UTC().Format("2006-01-02 15:04 MST"), d.File)
		}
	}
	for _, it := range items {
		if it.typ == rules.Major {
			fmt.Fprintf(&b, "\n**Breaking: major version update.** `%s` moves to `%s`; go.mod and the import paths in the Go files were rewritten. Release notes: %s\n", it.deps[0].Package, it.job.Package, releaseNotes(it.job.Package, it.job.Version))
		}
	}
	b.WriteString("\nOpened by [ripple](https://github.com/taumatix/ripple) when the new version appeared in the module index. Configure it in `.ripple.yaml`.\n")
	if len(items) == 1 && p.group == "" {
		if items[0].typ == rules.Major {
			return fmt.Sprintf("[major] Update %s to %s", items[0].job.Package, items[0].job.Version), b.String()
		}
		return fmt.Sprintf("Update %s to %s", items[0].job.Package, items[0].job.Version), b.String()
	}
	name := p.group
	return fmt.Sprintf("Update %s dependencies (%d)", name, len(items)), b.String()
}
