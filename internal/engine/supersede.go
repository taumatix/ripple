package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// staleVersion returns the version a ripple branch for pkg updates to, when head is
// such a branch. Branches are BranchPrefix+"go/"+slug(pkg)+"-"+slug(version), and
// slug is lossy, so the tail must be a valid semantic version that slug leaves as
// it is: a branch of another module that merely starts with this module's slug
// ("lib-extra-v1.0.0" against "lib") has a tail that is not one.
func staleVersion(head, pkg string) (string, bool) {
	tail, ok := strings.CutPrefix(head, BranchPrefix+"go/"+slug(pkg)+"-")
	if !ok || semver.Canonical(tail) != tail || slug(tail) != tail {
		return "", false
	}
	return tail, true
}

// staleActionVersion is staleVersion for a github-tags branch
// (BranchPrefix+"actions/"+slug(pkg)+"-"+slug(tag)): the tag, and its shape. Only a PR of
// the same shape is superseded, because v4->v5 and v4.1.2->v5.1.0 are updates of two
// different dependencies of one action and neither replaces the other.
func staleActionVersion(head, pkg string) (tag, shape string, ok bool) {
	tail, found := strings.CutPrefix(head, BranchPrefix+"actions/"+slug(pkg)+"-")
	if !found || slug(tail) != tail {
		return "", "", false
	}
	if _, shape, ok = tagShape(tail); !ok {
		return "", "", false
	}
	return tail, shape, true
}

// supersededBy is the PR (or, before it exists, the version) that replaces the
// stale ones of pkg. url is empty while the replacement is still to be opened. ds is
// the datasource of the package; the zero value is the Go module datasource.
type supersededBy struct {
	ds                string
	pkg, version, url string
}

func (b supersededBy) actions() bool { return b.ds == model.DatasourceGitHubTags }

// staleOf reports the version old updates by.pkg to when head is a ripple branch of that
// package and strictly older than by.version (and, for actions, of the same shape).
func (b supersededBy) staleOf(head string) (string, bool) {
	if !b.actions() {
		v, ok := staleVersion(head, b.pkg)
		return v, ok && semver.Compare(v, b.version) < 0
	}
	v, shape, ok := staleActionVersion(head, b.pkg)
	if !ok {
		return "", false
	}
	sv, byShape, _ := tagShape(b.version)
	oldSV, _, _ := tagShape(v)
	return v, shape == byShape && semver.Compare(oldSV, sv) < 0
}

// replaceable reports whether version can replace older PRs: a release for Go, a
// plain tag for actions.
func (b supersededBy) replaceable() bool {
	if b.actions() {
		_, _, ok := tagShape(b.version)
		return ok
	}
	return semver.IsValid(b.version) && semver.Prerelease(b.version) == ""
}

// ownedByRipple reports whether ripple's own record says old is the PR it opened for
// pkg at version: a finished job under that key whose PR is this one. A branch name
// or a title proves nothing, since anyone can push one shaped like ripple's.
func (r *runner) ownedByRipple(ds, pkg, version string, old platform.PR) bool {
	if ds == "" {
		ds = model.DatasourceGo
	}
	key := model.JobKey(r.repo.FullName, ds, pkg, version)
	j, ok, err := r.e.Store.GetJob(r.ctx, key)
	if err != nil {
		r.e.log().Warn("stale pull request not checked: reading its job failed", "repo", r.repo.FullName, "job", key, "err", err)
		return false
	}
	return ok && j.State == model.JobDone && j.PRURL != "" && j.PRURL == old.URL
}

// isRipplesReplacement reports whether newest is the PR of a ripple job for pkg at
// version, though that job may not have recorded the PR yet (a crash between opening
// the PR and finishing the job): a branch no job was ever created for is not.
func (r *runner) isRipplesReplacement(ds, pkg, version string, newest platform.PR) bool {
	if ds == "" {
		ds = model.DatasourceGo
	}
	j, ok, err := r.e.Store.GetJob(r.ctx, model.JobKey(r.repo.FullName, ds, pkg, version))
	return err == nil && ok && (j.PRURL == "" || j.PRURL == newest.URL)
}

// closeStale closes the open ripple PRs of by.pkg that update to a strictly older
// version than by.version, and returns the heads it closed. It never touches a
// group PR, a PR for another module, one for an equal or newer version, one whose
// branch is not a ripple branch, or one no recorded ripple job opened; the host
// further refuses any PR a person touched. A failure is logged and leaves that PR
// open for the next run of the repo to retry. exceptHead is the replacement's own
// branch.
func (r *runner) closeStale(by supersededBy, exceptHead string, open []platform.PR) map[string]bool {
	closed := map[string]bool{}
	if !r.cfg.ClosesSuperseded() || !by.replaceable() {
		return closed
	}
	touched := r.touchedPRs()
	for _, old := range open {
		oldVersion, ok := by.staleOf(old.Head)
		if !ok || old.Head == exceptHead || !r.ownedByRipple(by.ds, by.pkg, oldVersion, old) {
			continue
		}
		if stillTouched(touched, old) {
			r.e.log().Debug("stale pull request left open: remembered as touched", "repo", r.repo.FullName, "pr", old.URL)
			continue
		}
		done, err := platform.CloseUntouchedPR(r.ctx, r.e.Platform, r.repo, old, supersededComment(by, oldVersion))
		switch {
		case errors.Is(err, platform.ErrNoClosePR):
			r.e.log().Debug("host cannot close pull requests; stale pull request left open", "repo", r.repo.FullName, "pr", old.URL)
			return closed
		case err != nil:
			r.e.log().Warn("stale pull request not closed", "repo", r.repo.FullName, "pr", old.URL, "err", err)
			var rl *platform.RateLimitError
			if errors.As(err, &rl) {
				return closed
			}
		case !done:
			r.e.log().Info("stale pull request left open: it was touched by someone else", "repo", r.repo.FullName, "pr", old.URL)
			touched[old.URL] = old.HeadSHA
			r.saveTouchedPRs(touched)
		default:
			closed[old.Head] = true
			r.e.log().Info("closed superseded pull request", "repo", r.repo.FullName, "pr", old.URL, "by", by.url, "version", by.version)
		}
	}
	return closed
}

// supersede closes the stale PRs of the module pr was just opened for.
func (r *runner) supersede(p *prPlan, pr platform.PR, open []platform.PR) {
	if p.group != "" || len(p.items) == 0 {
		return
	}
	j := p.items[0].job
	r.closeStale(supersededBy{ds: j.Datasource, pkg: j.Package, version: j.Version, url: pr.URL}, p.branch, open)
}

// sweepSuperseded retries what an earlier run failed to close: for every module the
// repo uses whose newest open ripple PR has older open ones, close those. It makes
// no call when nothing is stale. It returns the heads it closed.
func (r *runner) sweepSuperseded(deps []model.Dependency, open []platform.PR) map[string]bool {
	closed := map[string]bool{}
	if !r.cfg.ClosesSuperseded() {
		return closed
	}
	r.forgetClosedTouchedPRs(open)
	seen := map[string]bool{}
	for _, d := range deps {
		if d.Datasource == model.DatasourceGitHubTags {
			for head := range r.sweepActions(d.Package, seen, open) {
				closed[head] = true
			}
			continue
		}
		if d.Datasource != model.DatasourceGo || seen[d.Package] {
			continue
		}
		seen[d.Package] = true
		var newest platform.PR
		newestVersion, stale := "", false
		for _, pr := range open {
			v, ok := staleVersion(pr.Head, d.Package)
			if !ok || semver.Prerelease(v) != "" {
				continue
			}
			if newestVersion != "" {
				stale = true
			}
			if newestVersion == "" || semver.Compare(v, newestVersion) > 0 {
				newest, newestVersion = pr, v
			}
		}
		if !stale || !r.isRipplesReplacement(model.DatasourceGo, d.Package, newestVersion, newest) {
			continue
		}
		for head := range r.closeStale(supersededBy{pkg: d.Package, version: newestVersion, url: newest.URL}, newest.Head, open) {
			closed[head] = true
		}
	}
	return closed
}

// sweepActions is the sweep for one action: per tag shape, the newest open ripple PR
// replaces the older open ones.
func (r *runner) sweepActions(pkg string, seen map[string]bool, open []platform.PR) map[string]bool {
	closed := map[string]bool{}
	if seen["actions:"+pkg] {
		return closed
	}
	seen["actions:"+pkg] = true
	type best struct {
		pr    platform.PR
		tag   string
		sv    string
		count int
	}
	byShape := map[string]*best{}
	for _, pr := range open {
		tag, shape, ok := staleActionVersion(pr.Head, pkg)
		if !ok {
			continue
		}
		sv, _, _ := tagShape(tag)
		b := byShape[shape]
		if b == nil {
			b = &best{}
			byShape[shape] = b
		}
		b.count++
		if b.count == 1 || semver.Compare(sv, b.sv) > 0 {
			b.pr, b.tag, b.sv = pr, tag, sv
		}
	}
	for _, b := range byShape {
		if b.count < 2 || !r.isRipplesReplacement(model.DatasourceGitHubTags, pkg, b.tag, b.pr) {
			continue
		}
		by := supersededBy{ds: model.DatasourceGitHubTags, pkg: pkg, version: b.tag, url: b.pr.URL}
		for head := range r.closeStale(by, b.pr.Head, open) {
			closed[head] = true
		}
	}
	return closed
}

func supersededComment(by supersededBy, oldVersion string) string {
	lead := "Superseded by " + by.url
	if by.url == "" {
		// The open-PR limit is reached and this PR holds the slot its replacement needs.
		lead = "Superseded by the pull request ripple is opening next"
	}
	return fmt.Sprintf("%s: `%s` has a newer release, `%s`, and this pull request only updates it to `%s`. "+
		"ripple is closing it because nobody but ripple has touched it; reopen it if you still want this version. "+
		"To keep ripple from closing superseded pull requests, set `close-superseded-pull-requests: false` in `.ripple.yaml`.",
		lead, by.pkg, by.version, oldVersion)
}

// touchedCursor holds, per repo, the open ripple PRs a host reported as touched by a
// person, each with the head commit it had then (JSON {url: sha}; an older run wrote a
// plain list of URLs, which reads as "head unknown"). A touch is remembered, not read
// again (about six requests) on every run, until the PR leaves the open list or its
// head moves: a head that moved may be a revert or a reset, so the host is asked once
// more. A host that reports no head commit keeps the touch until the PR closes. The
// repo's lease serializes writers.
func (r *runner) touchedCursor() string { return "supersede-touched:" + r.repo.FullName }

func (r *runner) touchedPRs() map[string]string {
	set := map[string]string{}
	raw, err := r.e.Store.GetCursor(r.ctx, r.touchedCursor())
	if err != nil {
		r.e.log().Warn("touched pull requests not read; checking them again", "repo", r.repo.FullName, "err", err)
		return set
	}
	if json.Unmarshal([]byte(raw), &set) == nil && raw != "" {
		return set
	}
	var urls []string
	if raw != "" && json.Unmarshal([]byte(raw), &urls) == nil {
		for _, u := range urls {
			set[u] = ""
		}
	}
	return set
}

func (r *runner) saveTouchedPRs(set map[string]string) {
	b, _ := json.Marshal(set) // map keys marshal sorted
	if err := r.e.Store.SetCursor(r.ctx, r.touchedCursor(), string(b)); err != nil {
		r.e.log().Warn("touched pull requests not remembered", "repo", r.repo.FullName, "err", err)
	}
}

// stillTouched reports whether pr is remembered as touched with the head it has now.
// A remembered touch with an unknown head, or a PR whose host reports no head, holds.
func stillTouched(set map[string]string, pr platform.PR) bool {
	sha, ok := set[pr.URL]
	return ok && (sha == "" || pr.HeadSHA == "" || sha == pr.HeadSHA)
}

// forgetClosedTouchedPRs drops what is remembered of a PR no longer open, or whose
// head moved since it was judged, so the memory cannot outlive what it describes.
func (r *runner) forgetClosedTouchedPRs(open []platform.PR) {
	set := r.touchedPRs()
	if len(set) == 0 {
		return
	}
	still := map[string]string{}
	for _, pr := range open {
		if stillTouched(set, pr) {
			still[pr.URL] = set[pr.URL]
		}
	}
	if len(still) != len(set) {
		r.saveTouchedPRs(still)
	}
}
