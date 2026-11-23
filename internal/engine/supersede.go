package engine

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

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

// supersede closes the open ripple PRs of the same module that update to a strictly
// older version than the one pr was just opened for. It never touches a group PR,
// a PR for another module, one for an equal or newer version, or one whose branch
// is not a ripple branch; the host further refuses any PR a person touched. A
// failure is logged and ends the pass: the stale PR stays open and the next newer
// release tries again. open is the listing taken before pr was created.
func (r *runner) supersede(p *prPlan, pr platform.PR, open []platform.PR) {
	if !r.cfg.ClosesSuperseded() || p.group != "" || len(p.items) == 0 {
		return
	}
	pkg, version := p.items[0].job.Package, p.items[0].job.Version
	if !semver.IsValid(version) || semver.Prerelease(version) != "" {
		return
	}
	for _, old := range open {
		oldVersion, ok := staleVersion(old.Head, pkg)
		if !ok || old.Head == p.branch || semver.Compare(oldVersion, version) >= 0 {
			continue
		}
		done, err := platform.CloseUntouchedPR(r.ctx, r.e.Platform, r.repo, old, supersededComment(pkg, oldVersion, version, pr))
		switch {
		case errors.Is(err, platform.ErrNoClosePR):
			r.e.log().Debug("host cannot close pull requests; stale pull request left open", "repo", r.repo.FullName, "pr", old.URL)
			return
		case err != nil:
			r.e.log().Warn("stale pull request not closed", "repo", r.repo.FullName, "pr", old.URL, "err", err)
			var rl *platform.RateLimitError
			if errors.As(err, &rl) {
				return
			}
		case !done:
			r.e.log().Info("stale pull request left open: it was touched by someone else", "repo", r.repo.FullName, "pr", old.URL)
		default:
			r.e.log().Info("closed superseded pull request", "repo", r.repo.FullName, "pr", old.URL, "by", pr.URL)
		}
	}
}

func supersededComment(pkg, oldVersion, newVersion string, pr platform.PR) string {
	return fmt.Sprintf("Superseded by %s: `%s` has a newer release, `%s`, and this pull request only updates it to `%s`. "+
		"ripple is closing it because nobody but ripple has touched it; reopen it if you still want this version. "+
		"To keep ripple from closing superseded pull requests, set `close-superseded-pull-requests: false` in `.ripple.yaml`.",
		pr.URL, pkg, newVersion, oldVersion)
}
