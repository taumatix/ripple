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
	// dashboardVerify is how often an unchanged dashboard is read back to see whether a
	// person edited it. One read per repo per interval, and only while jobs are open.
	dashboardVerify = time.Hour
	dirtyCursor     = "dashboard-dirty"
	watchCursor     = "dashboard-watch"
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
	return renderDashboardFull(jobs, advisories, nil, now)
}

// renderDashboardFull also lists the actions left alone for want of a tag of their shape.
func renderDashboardFull(jobs []model.Job, advisories []openAdvisory, skips []shapeSkip, now time.Time) string {
	return renderDashboardNotice(jobs, advisories, skips, "", now)
}

// renderDashboardNotice also says the repo's config does not parse (cfgErr, untrusted and
// cleaned), which is why every job of the repo is skipped until the file is fixed.
func renderDashboardNotice(jobs []model.Job, advisories []openAdvisory, skips []shapeSkip, cfgErr string, now time.Time) string {
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
	b.WriteString("ripple keeps this issue up to date. It is a report: edits to it are undone, and it is rewritten when the lists below change.\n")
	if cfgErr != "" {
		fmt.Fprintf(&b, "\n## Configuration invalid\n\nThis repository's ripple config does not parse, so ripple opens no pull requests for it and skips its jobs: no updates until it is fixed. `%s`\n", clean(cfgErr))
	}
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
	if len(skips) > 0 {
		fmt.Fprintf(&b, "\n## Actions left alone (%d)\n\nGitHub Actions with a newer tag upstream, none of which has the shape they are pinned to (`v4` follows `v5`, not `v5.0.0`), so ripple does not move them. Pin a shape upstream publishes, or update by hand. No pull request is opened for them.\n\n", len(skips))
		for _, sk := range skips {
			line := fmt.Sprintf("- `%s` `%s`: newest tag `%s`\n", clean(sk.Package), clean(sk.Version), clean(sk.Newest))
			if b.Len()+len(line) > dashboardBudget {
				omitted++
				continue
			}
			b.WriteString(line)
		}
	}
	if len(jobs) == 0 && len(advisories) == 0 && len(skips) == 0 && cfgErr == "" {
		b.WriteString("\nNothing is pending, deferred or failed.\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n... and %d more not shown, to stay under GitHub's issue size limit.\n", omitted)
	}
	return b.String()
}

func dashboardCursor(repo string) string { return "dashboard:" + repo }

// setDashboardCursor stores repo's dashboard cursor and keeps watchCursor, the list of
// repos that have a dashboard, in step. The list is how a flush finds the dashboards due
// to be read back without listing every repo.
func (e *Engine) setDashboardCursor(ctx context.Context, repo, val string) error {
	if err := e.Store.SetCursor(ctx, dashboardCursor(repo), val); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	watch := e.cursorSet(ctx, watchCursor)
	if watch[repo] == (val != "") {
		return nil
	}
	if val != "" {
		watch[repo] = true
	} else {
		delete(watch, repo)
	}
	return e.saveCursorSet(ctx, watchCursor, watch)
}

func dashboardOffCursor(repo string) string { return "dashboard-off:" + repo }

// markDashboard remembers that repo's jobs changed outside a run of its own, so the
// next flushDashboards looks at it. The set is also kept in the store, so a process
// that stops before it flushes does not lose the refresh.
func (e *Engine) markDashboard(repo string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dirty == nil {
		e.dirty = map[string]bool{}
	}
	e.dirty[repo] = true
	ctx := context.Background()
	set := e.storedDirty(ctx)
	set[repo] = true
	e.saveDirty(ctx, set)
}

func (e *Engine) storedDirty(ctx context.Context) map[string]bool {
	return e.cursorSet(ctx, dirtyCursor)
}

func (e *Engine) saveDirty(ctx context.Context, set map[string]bool) {
	if err := e.saveCursorSet(ctx, dirtyCursor, set); err != nil {
		e.log().Warn("save dashboard refresh set", "err", err)
	}
}

// cursorSet reads a set of repo names kept one per line in a cursor.
func (e *Engine) cursorSet(ctx context.Context, name string) map[string]bool {
	set := map[string]bool{}
	v, err := e.Store.GetCursor(ctx, name)
	if err != nil {
		e.log().Warn("read dashboard cursor set", "cursor", name, "err", err)
		return set
	}
	for _, r := range strings.Fields(v) {
		set[r] = true
	}
	return set
}

func (e *Engine) saveCursorSet(ctx context.Context, name string, set map[string]bool) error {
	repos := make([]string, 0, len(set))
	for r := range set {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	return e.Store.SetCursor(ctx, name, strings.Join(repos, "\n"))
}

// flushDashboards syncs every repo marked since the last flush, by this process or by
// one that stopped before flushing. It never fails a pass.
func (e *Engine) flushDashboards(ctx context.Context) {
	e.mu.Lock()
	set := e.storedDirty(ctx)
	for r := range e.dirty {
		set[r] = true
	}
	e.dirty = nil
	if len(set) > 0 {
		e.saveDirty(ctx, nil)
	}
	watch := e.cursorSet(ctx, watchCursor)
	e.mu.Unlock()
	// A dashboard not read back for a while is looked at too: a person may have edited it.
	for r := range watch {
		cur, err := e.Store.GetCursor(ctx, dashboardCursor(r))
		if err != nil {
			continue
		}
		if _, _, checked := parseDashboardCursor(cur); !e.now().Before(checked.Add(dashboardVerify)) {
			set[r] = true
		}
	}
	repos := make([]string, 0, len(set))
	for r := range set {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for i, r := range repos {
		if ctx.Err() != nil {
			// Shutting down: keep what was not reached for the next process.
			for _, rest := range repos[i:] {
				e.markDashboard(rest)
			}
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
	if v, err := e.Store.GetCursor(ctx, dashboardOffCursor(name)); err == nil && v != "" {
		if sec, perr := strconv.ParseInt(v, 10, 64); perr == nil && e.now().Before(time.Unix(sec, 0)) {
			return
		}
	}
	repo, found, err := e.Store.GetRepo(ctx, name)
	if err != nil || !found {
		return
	}
	cfg, err := RepoConfig(repo.Config)
	if err != nil {
		// The opt-in cannot be read, so only a dashboard that already exists is kept
		// truthful: it says why nothing moves.
		cur, cerr := e.Store.GetCursor(ctx, dashboardCursor(name))
		if n, _, _ := parseDashboardCursor(cur); cerr != nil || n == 0 {
			return
		}
		e.finishDashboard(ctx, name, e.updateDashboard(ctx, repo, lister, err.Error()))
		return
	}
	if cfg.DependencyDashboard {
		err = e.updateDashboard(ctx, repo, lister, "")
	} else {
		err = e.closeDashboard(ctx, repo)
	}
	e.finishDashboard(ctx, name, err)
}

// finishDashboard logs a failed sync, and backs off a repo whose issues cannot be written.
func (e *Engine) finishDashboard(ctx context.Context, name string, err error) {
	if err != nil {
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
			until := strconv.FormatInt(e.now().Add(dashboardBackoff).Unix(), 10)
			if serr := e.Store.SetCursor(ctx, dashboardOffCursor(name), until); serr != nil {
				e.log().Warn("save dashboard backoff", "repo", name, "err", serr)
			}
			if !again {
				e.log().Warn("dependency dashboard skipped: issues are disabled or the App may not write them", "repo", name, "err", err)
			}
		default:
			e.log().Warn("dependency dashboard not updated", "repo", name, "err", err)
		}
	}
}

// closeDashboard closes the issue ripple opened for repo once its dependency-dashboard
// key is off, and forgets it, so a later pass makes no call. A repo that never had a
// dashboard (no cursor) costs nothing. An issue that is already gone or closed is done.
func (e *Engine) closeDashboard(ctx context.Context, repo model.Repo) error {
	cur, err := e.Store.GetCursor(ctx, dashboardCursor(repo.FullName))
	if err != nil {
		return err
	}
	number, _, _ := parseDashboardCursor(cur)
	if number == 0 {
		return nil
	}
	err = platform.CloseIssue(ctx, e.Platform, repo, number)
	if err != nil && !errors.Is(err, platform.ErrIssueGone) && !errors.Is(err, platform.ErrNoIssues) {
		return err
	}
	return e.setDashboardCursor(ctx, repo.FullName, "")
}

func (e *Engine) updateDashboard(ctx context.Context, repo model.Repo, lister store.OpenJobLister, cfgErr string) error {
	jobs, err := lister.ListOpenJobs(ctx, repo.FullName)
	if err != nil {
		return err
	}
	advisories, err := e.currentOpenAdvisories(ctx, repo.FullName)
	if err != nil {
		return err
	}
	skips, err := e.currentShapeSkips(ctx, repo.FullName)
	if err != nil {
		return err
	}
	body := renderDashboardNotice(jobs, advisories, skips, cfgErr, e.now())
	hash := bodyHash(body)
	cur, err := e.Store.GetCursor(ctx, dashboardCursor(repo.FullName))
	if err != nil {
		return err
	}
	number, last, checked := parseDashboardCursor(cur)
	if number > 0 && last == hash {
		if e.now().Before(checked.Add(dashboardVerify)) {
			return nil // unchanged and read back recently: no API call
		}
		// Read the issue back: a person may have edited it, and the hash only says what
		// ripple last wrote.
		iss, found, err := platform.FindIssue(ctx, e.Platform, repo, DashboardLabel, DashboardMarker)
		if err != nil {
			return err
		}
		if found && iss.Body == body {
			return e.setDashboardCursor(ctx, repo.FullName, strconv.Itoa(iss.Number)+":"+hash+":"+strconv.FormatInt(e.now().Unix(), 10))
		}
		if found {
			number = iss.Number
		}
		// Not found by its marker means a person removed the marker line, or the issue is
		// gone: write the body by number, which restores the marker, and let the gone
		// case below find or create it.
	}
	save := func(n int) error {
		return e.setDashboardCursor(ctx, repo.FullName, strconv.Itoa(n)+":"+hash+":"+strconv.FormatInt(e.now().Unix(), 10))
	}
	if number == 0 {
		iss, found, err := platform.FindIssue(ctx, e.Platform, repo, DashboardLabel, DashboardMarker)
		if err != nil {
			return err
		}
		if !found {
			if len(jobs) == 0 && len(advisories) == 0 && len(skips) == 0 && cfgErr == "" {
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
		if len(jobs) == 0 && len(advisories) == 0 && len(skips) == 0 && cfgErr == "" {
			return e.setDashboardCursor(ctx, repo.FullName, "")
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

// parseDashboardCursor reads "number:hash[:checkedUnix]". An older cursor has no time,
// which reads as long ago and so is verified once.
func parseDashboardCursor(s string) (int, string, time.Time) {
	n, rest, ok := strings.Cut(s, ":")
	if !ok {
		return 0, "", time.Time{}
	}
	num, err := strconv.Atoi(n)
	if err != nil {
		return 0, "", time.Time{}
	}
	h, ts, _ := strings.Cut(rest, ":")
	var checked time.Time
	if sec, err := strconv.ParseInt(ts, 10, 64); err == nil {
		checked = time.Unix(sec, 0)
	}
	return num, h, checked
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
