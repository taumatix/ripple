package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
)

// The dependency dashboard: one issue per opted-in repo (`dependency-dashboard: true`)
// listing what ripple has not finished: jobs deferred by a cooldown, a schedule or a
// retry, jobs due and waiting for a run, and failed jobs.
//
// The issue is found by its label and by DashboardMarker on the first line of its body,
// so a person's issue that merely carries the label is never overwritten. The body is a
// pure function of the open jobs and the clock-relative split between deferred and due,
// holds no "last updated" time, and its hash is kept in a store cursor: a pass that
// renders the same body makes no API call. The dashboard is a report. Nothing read back
// from the issue (edits, checkboxes) is acted on.
const (
	// DashboardMarker starts the body of ripple's dashboard issue.
	DashboardMarker = "<!-- ripple:dependency-dashboard -->"
	// DashboardLabel is applied to the dashboard issue.
	DashboardLabel = "ripple"

	dashboardTitle = "Dependency dashboard"
	// dashboardBudget keeps the body under GitHub's 65536-character limit.
	dashboardBudget = 60000
	// dashboardMaxField bounds one untrusted field (package, version, reason).
	dashboardMaxField = 200
	// dashboardBackoff is how long a repo whose issues cannot be written is left alone.
	dashboardBackoff = time.Hour
)

// clean makes an untrusted field safe to put inside a Markdown code span on one line.
func clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch {
		case r == '`' || r == '|' || r == '<' || r == '>':
			continue
		case unicode.IsControl(r) || unicode.IsSpace(r):
			r = ' '
		}
		if n++; n > dashboardMaxField {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// renderDashboard is the issue body for jobs at time now.
func renderDashboard(jobs []model.Job, now time.Time) string {
	return renderDashboardWith(jobs, nil, now)
}

// renderDashboardWith also lists advisories ripple cannot propose a fix for. Their ids,
// packages and versions come from OSV and the repo's go.mod: all untrusted, all cleaned.
func renderDashboardWith(jobs []model.Job, advisories []openAdvisory, now time.Time) string {
	var deferred, due, failed []model.Job
	for _, j := range jobs {
		switch {
		case j.State == model.JobFailed:
			failed = append(failed, j)
		case j.NotBefore.After(now):
			deferred = append(deferred, j)
		default:
			due = append(due, j)
		}
	}
	for _, s := range [][]model.Job{deferred, due, failed} {
		sort.Slice(s, func(i, k int) bool { return s[i].Key < s[k].Key })
	}
	var b strings.Builder
	b.WriteString(DashboardMarker + "\n# Dependency dashboard\n\n")
	b.WriteString("ripple keeps this issue up to date. It is a report: edits to it are ignored, and it is rewritten when the lists below change.\n")
	omitted := 0
	section := func(title, hint string, js []model.Job, detail func(model.Job) string) {
		if len(js) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n## %s (%d)\n\n%s\n\n", title, len(js), hint)
		for _, j := range js {
			line := fmt.Sprintf("- `%s` `%s`%s\n", clean(j.Package), clean(j.Version), detail(j))
			if b.Len()+len(line) > dashboardBudget {
				omitted++
				continue
			}
			b.WriteString(line)
		}
	}
	section("Deferred", "Waiting for a cooldown, a schedule window or a retry.", deferred, func(j model.Job) string {
		s := " until " + j.NotBefore.UTC().Format("2006-01-02 15:04 UTC")
		if r := clean(j.Reason); r != "" {
			s += ": `" + r + "`"
		}
		return s
	})
	section("Pending", "Due, waiting for ripple to run them (for example behind the open pull request limit).", due, func(j model.Job) string {
		if r := clean(j.Reason); r != "" {
			return ": `" + r + "`"
		}
		return ""
	})
	section("Failed", "ripple gave up on these. A newer release of the module creates a new job.", failed, func(j model.Job) string {
		s := " after " + strconv.Itoa(j.Attempts) + " attempts"
		if r := clean(j.Reason); r != "" {
			s += ": `" + r + "`"
		}
		return s
	})
	if len(advisories) > 0 {
		fmt.Fprintf(&b, "\n## Advisories without a fix ripple can propose (%d)\n\nPublished advisories that affect a version this repo uses and that no release fixes (or, where noted, only a pseudo-version does). No pull request is opened for them.\n\n", len(advisories))
		for _, o := range advisories {
			line := fmt.Sprintf("- [%s](%s) in `%s` `%s`: %s\n", clean(o.ID), safeAdvisoryURL(o), clean(o.Package), clean(o.Version), clean(o.Why))
			if b.Len()+len(line) > dashboardBudget {
				omitted++
				continue
			}
			b.WriteString(line)
		}
	}
	if len(jobs) == 0 && len(advisories) == 0 {
		b.WriteString("\nNothing is pending, deferred or failed.\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n... and %d more not shown, to stay under GitHub's issue size limit.\n", omitted)
	}
	return b.String()
}

func dashboardCursor(repo string) string { return "dashboard:" + repo }

// markDashboard remembers that repo's jobs changed outside a run of its own, so the
// next flushDashboards looks at it.
func (e *Engine) markDashboard(repo string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dirty == nil {
		e.dirty = map[string]bool{}
	}
	e.dirty[repo] = true
}

// flushDashboards syncs every repo marked since the last flush. It never fails a pass.
func (e *Engine) flushDashboards(ctx context.Context) {
	e.mu.Lock()
	repos := make([]string, 0, len(e.dirty))
	for r := range e.dirty {
		repos = append(repos, r)
	}
	e.dirty = nil
	e.mu.Unlock()
	sort.Strings(repos)
	for _, r := range repos {
		if ctx.Err() != nil {
			return
		}
		e.flushDashboard(ctx, r)
	}
}

// syncDashboard brings repo's dashboard issue up to date. The caller holds the repo
// lock. Every failure is logged and swallowed: the dashboard never fails a job or a scan.
func (e *Engine) syncDashboard(ctx context.Context, name string) {
	if e.Paused(ctx, name) {
		return // archived: the issue tracker is read-only too
	}
	lister, ok := e.Store.(store.OpenJobLister)
	if !ok {
		return
	}
	if _, ok := e.Platform.(platform.IssueTracker); !ok {
		return
	}
	e.mu.Lock()
	off := e.dashOff[name]
	e.mu.Unlock()
	if e.now().Before(off) {
		return
	}
	repo, found, err := e.Store.GetRepo(ctx, name)
	if err != nil || !found {
		return
	}
	cfg, err := RepoConfig(repo.Config)
	if err != nil || !cfg.DependencyDashboard {
		return
	}
	if err := e.updateDashboard(ctx, repo, lister); err != nil {
		switch {
		case errors.Is(err, platform.ErrNoIssues):
		case errors.Is(err, platform.ErrIssuesUnavailable):
			e.mu.Lock()
			if e.dashOff == nil {
				e.dashOff = map[string]time.Time{}
			}
			_, again := e.dashOff[name]
			e.dashOff[name] = e.now().Add(dashboardBackoff)
			e.mu.Unlock()
			if !again {
				e.log().Warn("dependency dashboard skipped: issues are disabled or the App may not write them", "repo", name, "err", err)
			}
		default:
			e.log().Warn("dependency dashboard not updated", "repo", name, "err", err)
		}
	}
}

func (e *Engine) updateDashboard(ctx context.Context, repo model.Repo, lister store.OpenJobLister) error {
	jobs, err := lister.ListOpenJobs(ctx, repo.FullName)
	if err != nil {
		return err
	}
	advisories, err := e.currentOpenAdvisories(ctx, repo.FullName)
	if err != nil {
		return err
	}
	body := renderDashboardWith(jobs, advisories, e.now())
	hash := bodyHash(body)
	cur, err := e.Store.GetCursor(ctx, dashboardCursor(repo.FullName))
	if err != nil {
		return err
	}
	number, last := parseDashboardCursor(cur)
	if number > 0 && last == hash {
		return nil // unchanged: no API call
	}
	save := func(n int) error {
		return e.Store.SetCursor(ctx, dashboardCursor(repo.FullName), strconv.Itoa(n)+":"+hash)
	}
	if number == 0 {
		iss, found, err := platform.FindIssue(ctx, e.Platform, repo, DashboardLabel, DashboardMarker)
		if err != nil {
			return err
		}
		if !found {
			if len(jobs) == 0 && len(advisories) == 0 {
				return nil // nothing to report and nothing reported yet
			}
			return e.createDashboard(ctx, repo, body, save)
		}
		number = iss.Number
		if iss.Body == body {
			return save(number)
		}
	}
	err = platform.UpdateIssueBody(ctx, e.Platform, repo, number, body)
	if errors.Is(err, platform.ErrIssueGone) {
		// Deleted, or issues were switched off since: find again to tell which.
		iss, found, ferr := platform.FindIssue(ctx, e.Platform, repo, DashboardLabel, DashboardMarker)
		if ferr != nil {
			return ferr
		}
		if found {
			if err = platform.UpdateIssueBody(ctx, e.Platform, repo, iss.Number, body); err == nil {
				return save(iss.Number)
			}
			return err
		}
		if len(jobs) == 0 && len(advisories) == 0 {
			return e.Store.SetCursor(ctx, dashboardCursor(repo.FullName), "")
		}
		return e.createDashboard(ctx, repo, body, save)
	}
	if err != nil {
		return err
	}
	return save(number)
}

func (e *Engine) createDashboard(ctx context.Context, repo model.Repo, body string, save func(int) error) error {
	iss, err := platform.CreateIssue(ctx, e.Platform, repo, platform.IssueSpec{Title: dashboardTitle, Body: body, Labels: []string{DashboardLabel}})
	if err != nil {
		return err
	}
	return save(iss.Number)
}

func parseDashboardCursor(s string) (int, string) {
	n, h, ok := strings.Cut(s, ":")
	if !ok {
		return 0, ""
	}
	num, err := strconv.Atoi(n)
	if err != nil {
		return 0, ""
	}
	return num, h
}

func bodyHash(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

// safeAdvisoryURL is the osv.dev page of the advisory, rebuilt from its id rather than
// taken from the stored value, so nothing but an osv.dev link can reach the issue.
func safeAdvisoryURL(o openAdvisory) string {
	return "https://osv.dev/vulnerability/" + url.PathEscape(clean(o.ID))
}

// flushDashboard syncs one repo's dashboard under its lease, so a replica that is not the
// holder never writes the issue. A repo another replica holds is skipped: its holder syncs
// the dashboard when its scan or run ends, from the same stored state.
func (e *Engine) flushDashboard(ctx context.Context, name string) {
	defer e.lockRepo(name)()
	hold, err := e.acquireRepo(ctx, name, false)
	if err != nil {
		if ctx.Err() == nil {
			e.log().Error("claim repo for dashboard", "repo", name, "err", err)
		}
		return
	}
	if hold == nil {
		return
	}
	defer hold.Release()
	e.syncDashboard(hold.Ctx, name)
}
