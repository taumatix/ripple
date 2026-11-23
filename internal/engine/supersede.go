package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// supersededBy is the PR (or, before it exists, the version) that replaces the
// stale ones of pkg. url is empty while the replacement is still to be opened.
type supersededBy struct {
	pkg, version, url string
}

// ownedByRipple reports whether ripple's own record says old is the PR it opened for
// pkg at version: a finished job under that key whose PR is this one. A branch name
// or a title proves nothing, since anyone can push one shaped like ripple's.
func (r *runner) ownedByRipple(pkg, version string, old platform.PR) bool {
	key := model.JobKey(r.repo.FullName, model.DatasourceGo, pkg, version)
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
func (r *runner) isRipplesReplacement(pkg, version string, newest platform.PR) bool {
	j, ok, err := r.e.Store.GetJob(r.ctx, model.JobKey(r.repo.FullName, model.DatasourceGo, pkg, version))
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
	if !r.cfg.ClosesSuperseded() || !semver.IsValid(by.version) || semver.Prerelease(by.version) != "" {
		return closed
	}
	touched := r.touchedPRs()
	for _, old := range open {
		oldVersion, ok := staleVersion(old.Head, by.pkg)
		if !ok || old.Head == exceptHead || semver.Compare(oldVersion, by.version) >= 0 || !r.ownedByRipple(by.pkg, oldVersion, old) {
			continue
		}
		if touched[old.URL] {
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
			touched[old.URL] = true
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
	r.closeStale(supersededBy{pkg: p.items[0].job.Package, version: p.items[0].job.Version, url: pr.URL}, p.branch, open)
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
		if !stale || !r.isRipplesReplacement(d.Package, newestVersion, newest) {
			continue
		}
		for head := range r.closeStale(supersededBy{pkg: d.Package, version: newestVersion, url: newest.URL}, newest.Head, open) {
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

// touchedCursor holds, per repo, the URLs of open ripple PRs a host reported as
// touched by a person. Touched is sticky: a comment, review or commit does not
// un-happen, so the PR is not read again (about six requests) on every run, only
// remembered until it leaves the open list. The repo's lease serializes writers.
func (r *runner) touchedCursor() string { return "supersede-touched:" + r.repo.FullName }

func (r *runner) touchedPRs() map[string]bool {
	set := map[string]bool{}
	raw, err := r.e.Store.GetCursor(r.ctx, r.touchedCursor())
	if err != nil {
		r.e.log().Warn("touched pull requests not read; checking them again", "repo", r.repo.FullName, "err", err)
		return set
	}
	var urls []string
	if raw != "" && json.Unmarshal([]byte(raw), &urls) != nil {
		return set
	}
	for _, u := range urls {
		set[u] = true
	}
	return set
}

func (r *runner) saveTouchedPRs(set map[string]bool) {
	urls := make([]string, 0, len(set))
	for u := range set {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	b, _ := json.Marshal(urls)
	if err := r.e.Store.SetCursor(r.ctx, r.touchedCursor(), string(b)); err != nil {
		r.e.log().Warn("touched pull requests not remembered", "repo", r.repo.FullName, "err", err)
	}
}

// forgetClosedTouchedPRs drops what is remembered of a PR no longer open, so the
// memory cannot outlive the PR it describes.
func (r *runner) forgetClosedTouchedPRs(open []platform.PR) {
	set := r.touchedPRs()
	if len(set) == 0 {
		return
	}
	still := map[string]bool{}
	for _, pr := range open {
		if set[pr.URL] {
			still[pr.URL] = true
		}
	}
	if len(still) != len(set) {
		r.saveTouchedPRs(still)
	}
}
