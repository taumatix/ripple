//go:build live

package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/livetest"
	"github.com/taumatix/ripple/internal/store"
)

// A ripple run pointed at the real api.osv.dev (ROADMAP 52): a repo that requires
// golang.org/x/text v0.3.6, which GO-2021-0113 and GO-2022-1059 cover, is registered; the scan's
// history check asks real OSV (querybatch, then the records) and opens a security PR. The
// repo, GitHub and the module proxy are the fakes; OSV is the only real service. Run with
// `go test -tags live`.
func TestLiveRippleRunAgainstRealOSV(t *testing.T) {
	hc, tr := livetest.Client()
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{tweak: func(a *app.App) {
		a.Engine.Advisories = true
		c := &advisory.Client{HTTP: hc}
		a.Engine.AdvisoryHistory = c
	}})
	// Every release a fix could name now or soon: a fix the proxy lacks would open a draft.
	versions := []string{"v0.3.6", "v0.3.7", "v0.3.8"}
	for minor := 39; minor <= 60; minor++ {
		versions = append(versions, fmt.Sprintf("v0.%d.0", minor))
	}
	stubModule(e, "golang.org/x/text", versions...)
	e.gh.AddRepo("acme/old-text", map[string]string{
		"go.mod":  gomod("example.com/oldtext", "golang.org/x/text v0.3.6"),
		"main.go": blankImports("golang.org/x/text"),
	})
	start(t, e, "acme/old-text")

	prs := e.gh.PRs("acme/old-text")
	open, highest := 0, ""
	for _, p := range prs {
		if !p.Closed {
			open++
			highest = p.Title
		}
	}
	// Real OSV lists advisories fixed in several releases for this version (0.3.7, 0.3.8 and
	// later ones); the run opens a PR per fix and leaves one open, for the highest.
	if len(prs) < 2 || open != 1 {
		t.Fatalf("want several security PRs opened and all but one closed, got %d PRs, %d open (OSV requests: %d)\nlogs:\n%s", len(prs), open, tr.Requests(), e.logs.String())
	}
	pr := prs[0]
	for _, p := range prs {
		if p.Title == highest {
			pr = p
		}
	}
	if !strings.HasPrefix(pr.Title, "[security] Update golang.org/x/text to v") || !hasLabel(pr.Labels, "security") || pr.Draft {
		t.Errorf("title %q labels %v draft %v", pr.Title, pr.Labels, pr.Draft)
	}
	if !strings.Contains(pr.Body, "https://osv.dev/vulnerability/GO-") {
		t.Errorf("body names no Go advisory:\n%s", pr.Body)
	}
	if tr.Requests() < 2 {
		t.Errorf("only %d OSV requests", tr.Requests())
	}
	t.Logf("%d PRs, one open: %q, after %d OSV requests", len(prs), pr.Title, tr.Requests())
}
