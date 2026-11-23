package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

const (
	advisoryCursor     = "osv:Go"
	advisoryNotePrefix = "osv-note:"
	// DefaultAdvisoryMaxFetch bounds the record requests of one poll.
	DefaultAdvisoryMaxFetch = 100
	// advisoryGiveUpAfter is how many polls in a row one record may fail before it is
	// skipped, so a record that always errors cannot hold the cursor for good.
	advisoryGiveUpAfter = 3
	maxSummaryRunes     = 300
)

// advisoryNote is what the PR says about the advisory that made a job urgent. It is
// kept in the cursor store under the job key, so no Job schema changes.
type advisoryNote struct {
	ID       string `json:"id"`
	Summary  string `json:"summary,omitempty"`
	Severity string `json:"severity,omitempty"`
	CVSS     string `json:"cvss,omitempty"`
	URL      string `json:"url"`
	// Pseudo marks a job whose target is a pseudo-version: no release fixes the advisory.
	Pseudo bool `json:"pseudo,omitempty"`
}

func newNote(v *advisory.Vuln) advisoryNote {
	s := strings.Join(strings.Fields(v.Summary), " ")
	if r := []rune(s); len(r) > maxSummaryRunes {
		s = string(r[:maxSummaryRunes]) + "..."
	}
	return advisoryNote{ID: v.ID, Summary: s, Severity: v.SeverityLabel(), CVSS: v.CVSS(), URL: v.URL()}
}

// advisoryNoteFor returns the note of a job opened for an advisory, or nil. It is
// only consulted when Engine.Advisories is set, so a deployment without OSV pays no
// extra store reads.
func (e *Engine) advisoryNoteFor(ctx context.Context, j model.Job) *advisoryNote {
	if !e.Advisories {
		return nil
	}
	raw, err := e.Store.GetCursor(ctx, advisoryNotePrefix+j.Key)
	if err != nil {
		e.log().Warn("read advisory note", "job", j.Key, "err", err)
		return nil
	}
	if raw == "" {
		return nil
	}
	var n advisoryNote
	if json.Unmarshal([]byte(raw), &n) != nil || n.ID == "" {
		return nil
	}
	return &n
}

// FanOutAdvisory turns one OSV record into jobs, for every repo that depends on what it
// names:
//
//   - A module in a go.mod at an affected version gets a job for the lowest release that
//     fixes it, which skips the cooldown (the repo's ignore rules, schedule, groups and
//     automerge still apply).
//   - A `toolchain` line, in a repo that opted in with go-toolchain, gets a job for the
//     patch release that fixes a stdlib or toolchain entry. A repo that did not opt in
//     has no toolchain dependency in the index, so it gets nothing, as before.
//   - An advisory with no release to move to (unfixed, a last_affected ceiling) makes no
//     job. One fixed only by a pseudo-version makes a job only where the entry sets
//     security-pseudo-versions. Where neither happens and the repo has a dependency
//     dashboard, the advisory is listed there instead.
//
// A repo already past the fix produces nothing. It is idempotent. It returns how many
// jobs were newly enqueued or made due.
func (e *Engine) FanOutAdvisory(ctx context.Context, v *advisory.Vuln) (int, error) {
	repos := map[string]*repoState{}
	if v.Withdrawn() {
		return 0, e.forgetOpenAdvisories(ctx, v)
	}
	n := newNote(v)
	total := 0
	for _, mod := range v.Modules() {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceGo, mod)
		if err != nil {
			return total, fmt.Errorf("list dependents of %s: %w", mod, err)
		}
		k, err := e.applyAdvisory(ctx, v, n, deps, repos)
		total += k
		if err != nil {
			return total, err
		}
	}
	if v.HasToolchain() {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceGoToolchain, model.PackageGoToolchain)
		if err != nil {
			return total, fmt.Errorf("list dependents of the go toolchain: %w", err)
		}
		k, err := e.applyAdvisory(ctx, v, n, deps, repos)
		total += k
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// verdictFor is what the record says about dependency d, or false when the record does
// not name what d is: a module path other than d's (the index answers for the whole
// major line), or a datasource the record has no entry kind for.
func verdictFor(v *advisory.Vuln, d model.Dependency) (advisory.Verdict, bool) {
	switch {
	case d.Datasource == model.DatasourceGo:
		return v.Assess(d.Package, d.Version), true
	case d.Datasource == model.DatasourceGoToolchain && d.Package == model.PackageGoToolchain:
		return v.AssessToolchain(d.Version), v.HasToolchain()
	}
	return advisory.Verdict{}, false
}

// applyAdvisory applies one record to the given dependency rows (which must belong to
// the module or toolchain the record names; others are skipped).
func (e *Engine) applyAdvisory(ctx context.Context, v *advisory.Vuln, note advisoryNote, deps []model.Dependency, repos map[string]*repoState) (int, error) {
	named := map[string]bool{}
	for _, m := range v.Modules() {
		named[m] = true
	}
	n := 0
	for _, d := range deps {
		ver, ok := verdictFor(v, d)
		if !ok || !ver.Affected || (d.Datasource == model.DatasourceGo && !named[d.Package]) {
			continue
		}
		rs, err := e.repoState(ctx, repos, d.Repo)
		if err != nil {
			return n, err
		}
		if !rs.found || rs.cfgErr != nil {
			continue
		}
		u, ok := rs.cfg.UpdateFor(d.Manager, d.File)
		if !ok {
			continue
		}
		fixed, pseudo := ver.Fixed, false
		if fixed == "" && ver.Pseudo != "" && u.SecurityPseudoVersions && d.Datasource == model.DatasourceGo {
			fixed, pseudo = ver.Pseudo, true
		}
		if fixed == "" {
			why := "no release fixes it yet"
			if ver.Pseudo != "" {
				why = "only a pseudo-version fixes it (" + ver.Pseudo + "); set security-pseudo-versions to propose it"
			}
			if err := e.recordOpenAdvisory(ctx, rs, d, openAdvisory{ID: v.ID, Datasource: d.Datasource, Package: d.Package, Version: d.Version, Why: why}); err != nil {
				return n, err
			}
			continue
		}
		var typ rules.UpdateType
		if pseudo {
			typ, ok = applicablePseudoFix(d, d.Package, fixed)
		} else {
			typ, ok = applicable(d, d.Datasource, d.Package, fixed)
		}
		if !ok {
			continue
		}
		dec := rules.EvaluateSecurity(e.Limits, u, d.Package, typ, e.now())
		if !dec.Enabled {
			continue
		}
		key := model.JobKey(d.Repo, d.Datasource, d.Package, fixed)
		jobNote := note
		jobNote.Pseudo = pseudo
		raw, err := json.Marshal(jobNote)
		if err != nil {
			return n, err
		}
		// The note goes first: a job must never be due without it. A GHSA record and
		// its Go-database twin both arrive; the one that carries a severity is kept.
		if err := e.keepNote(ctx, key, v, string(raw)); err != nil {
			return n, err
		}
		if err := e.clearOpenAdvisory(ctx, d.Repo, v.ID, d.Datasource, d.Package); err != nil {
			return n, err
		}
		inserted, err := e.Store.EnqueueJob(ctx, model.Job{
			Key: key, Repo: d.Repo, Datasource: d.Datasource, Package: d.Package, Version: fixed,
			ReleasedAt: e.now(), State: model.JobPending, NotBefore: dec.NotBefore,
		})
		if err != nil {
			return n, err
		}
		if inserted {
			n++
			e.markDashboard(d.Repo)
			continue
		}
		// The feed got there first and queued the same bump behind the cooldown.
		j, found, err := e.Store.GetJob(ctx, key)
		if err != nil {
			return n, err
		}
		if found && j.State == model.JobPending && j.NotBefore.After(dec.NotBefore) {
			j.NotBefore = dec.NotBefore
			if err := e.Store.UpdateJob(ctx, j); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// AdvisorySource is the OSV surface the watcher reads; *advisory.Client implements it.
type AdvisorySource interface {
	Modified(ctx context.Context, after advisory.Position) (entries []advisory.Entry, truncated bool, err error)
	Vuln(ctx context.Context, id string) (*advisory.Vuln, error)
}

// AdvisoryWatcher polls OSV's Go modification list and fans each changed record out.
// The cursor is a (modified time, id) position persisted after every record, so a
// restart resumes where it stopped and a record is never fanned out twice by a
// replay (and if it were, job keys make that harmless).
//
// Request volume is bounded: one list request per poll plus at most MaxFetch record
// requests. A backlog larger than that drains over several polls.
type AdvisoryWatcher struct {
	Engine   *Engine
	Source   AdvisorySource
	MaxFetch int // record requests per poll; zero means DefaultAdvisoryMaxFetch

	failID string
	failN  int
}

// Poll fans out the records modified since the cursor and returns how many jobs it
// enqueued. On first run it starts at the current time rather than replaying history.
// An OSV failure returns an error and leaves the cursor where it was; nothing else in
// the engine depends on it.
func (w *AdvisoryWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	raw, err := e.Store.GetCursor(ctx, advisoryCursor)
	if err != nil {
		return 0, err
	}
	if raw == "" {
		return 0, e.Store.SetCursor(ctx, advisoryCursor, advisory.Position{Time: e.now().UTC()}.String())
	}
	pos, err := advisory.ParsePosition(raw)
	if err != nil {
		return 0, err
	}
	entries, truncated, err := w.Source.Modified(ctx, pos)
	if err != nil {
		return 0, fmt.Errorf("osv modified list: %w", err)
	}
	if truncated {
		e.log().Warn("osv: more changed records than one poll reads; the oldest were skipped", "after", raw)
	}
	max := w.MaxFetch
	if max <= 0 {
		max = DefaultAdvisoryMaxFetch
	}
	jobs := 0
	for i, ent := range entries {
		if i >= max {
			break // the rest waits for the next poll
		}
		v, err := w.Source.Vuln(ctx, ent.ID)
		switch {
		case errors.Is(err, advisory.ErrNotFound):
			v = nil
		case err != nil:
			if w.failID != ent.ID {
				w.failID, w.failN = ent.ID, 0
			}
			if w.failN++; w.failN < advisoryGiveUpAfter {
				return jobs, fmt.Errorf("osv record %s: %w", ent.ID, err)
			}
			e.log().Error("osv: giving up on a record that keeps failing", "id", ent.ID, "err", err)
			v = nil
		}
		if v != nil {
			n, err := e.FanOutAdvisory(ctx, v)
			jobs += n
			if err != nil {
				return jobs, err
			}
		}
		w.failID, w.failN = "", 0
		if err := e.Store.SetCursor(ctx, advisoryCursor, advisory.Position{Time: ent.Time, ID: ent.ID}.String()); err != nil {
			return jobs, err
		}
	}
	return jobs, nil
}

// withSecurityLabel adds the "security" label when any item fixes an advisory.
func withSecurityLabel(items []item, labels []string) []string {
	for _, it := range items {
		if it.advisory != nil {
			return append(labels, "security")
		}
	}
	return labels
}

// securityText marks the PR title and lists the advisories in the body.
func securityText(items []item, title, body string) (string, string) {
	var b strings.Builder
	seen := map[string]bool{}
	for _, it := range items {
		a := it.advisory
		if a == nil || seen[a.ID] {
			continue
		}
		if !seen[""] {
			b.WriteString("\n**Security update.** The cooldown was skipped because this version fixes a published advisory:\n\n")
			seen[""] = true
		}
		seen[a.ID] = true
		fmt.Fprintf(&b, "- [%s](%s) in `%s`, fixed in `%s`", a.ID, a.URL, it.job.Package, it.job.Version)
		if a.Pseudo {
			b.WriteString(" (a pseudo-version: no release fixes this advisory, so this pins a commit; review it)")
		}
		if a.Severity != "" {
			fmt.Fprintf(&b, ", severity %s", a.Severity)
		}
		if a.CVSS != "" {
			fmt.Fprintf(&b, " (`%s`)", a.CVSS)
		}
		if a.Summary != "" {
			fmt.Fprintf(&b, ": %s", a.Summary)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return title, body
	}
	// The advisory text comes from OSV: it is shown, never interpreted.
	return "[security] " + title, strings.Replace(body, "\nOpened by [ripple]", b.String()+"\nOpened by [ripple]", 1)
}

// keepNote stores the note for a job key unless a richer one (with a severity) from
// another record of the same advisory is already there.
func (e *Engine) keepNote(ctx context.Context, key string, v *advisory.Vuln, note string) error {
	if v.SeverityLabel() == "" {
		if raw, err := e.Store.GetCursor(ctx, advisoryNotePrefix+key); err != nil {
			return err
		} else if raw != "" {
			var old advisoryNote
			if json.Unmarshal([]byte(raw), &old) == nil && old.Severity != "" {
				return nil
			}
		}
	}
	return e.Store.SetCursor(ctx, advisoryNotePrefix+key, note)
}
