package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// conflictEnv has a repo whose go directive (1.<n-2>) is out of support and whose toolchain
// line is vulnerable, so a security toolchain job (to go1.<n-2>.9) and a directive job (to
// go1.<n-1>.9) both want to edit the toolchain line of the same go.mod.
func conflictEnv(t *testing.T, st store.Store) (*env, *testutil.GoDL, *testutil.OSV, int) {
	t.Helper()
	n := localMinor(t)
	dl, osv := newDL(t), testutil.NewOSV(t)
	o := withOSV(osv, 0)
	o.goDL = dl
	e := newEnvOpts(t, st, defaultLimits(), o)
	dl.Set(goVer(n, 9), goVer(n-1, 9))
	e.gh.AddRepo("acme/app", withMode(tcRepo(fmt.Sprintf("1.%d", n-2), goVer(n-2, 3)), "directive"))
	return e, dl, osv, n
}

func toolchainFix(n int) []byte {
	return osvRecord("GO-2026-7200", "stdlib", fmt.Sprintf(`,{"fixed":"1.%d.9"}`, n-2))
}

// Entry 81: the security toolchain PR goes first, the directive job waits with a reason
// that names it, and once the security PR is merged the directive PR opens on top of it.
func TestSecurityToolchainPRGoesBeforeTheDirectivePR(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, _, osv, n := conflictEnv(t, st)
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7200", e.clock.Now(), toolchainFix(n))
		start(t, e, "acme/app") // the history check finds the advisory
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 || !strings.HasPrefix(prs[0].Title, "[security] Update the Go toolchain") {
			t.Fatalf("want only the security PR: %+v", prs)
		}
		dj := e.toolchainJob("acme/app", "go", fmt.Sprintf("v1.%d.9", n-1))
		if dj.State != model.JobPending || !strings.Contains(dj.Reason, "security") {
			t.Fatalf("the directive job must wait and say why: %+v", dj)
		}

		// The security PR is merged: the directive PR follows, from the merged line.
		e.gh.HumanClosePR("acme/app", prs[0].Number)
		e.pushGoMod("acme/app", fmt.Sprintf("module example.com/app\n\ngo 1.%d\n\ntoolchain %s\n", n-2, goVer(n-2, 9)))
		e.clock.Advance(2 * time.Hour)
		e.tick()
		var open int
		for _, p := range e.gh.PRs("acme/app") {
			if !p.Closed {
				open++
				if p.Title != fmt.Sprintf("Update the go directive to 1.%d", n-1) {
					t.Errorf("second PR %q", p.Title)
				}
			}
		}
		if open != 1 {
			t.Fatalf("want the directive PR open after the security PR merged: %+v", e.gh.PRs("acme/app"))
		}
	})
}

// A security PR that is already open holds a directive job that arrives later.
func TestAnOpenSecurityToolchainPRHoldsALaterDirectiveJob(t *testing.T) {
	e, dl, osv, n := conflictEnv(t, store.NewMemory())
	dl.Set() // the Go list is empty, so no directive job exists yet
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7200", e.clock.Now(), toolchainFix(n))
	start(t, e, "acme/app")
	e.tick()
	if prs := e.gh.PRs("acme/app"); len(prs) != 1 {
		t.Fatalf("want the security PR: %+v", prs)
	}
	dl.Set(goVer(n, 9), goVer(n-1, 9))
	e.clock.Advance(time.Minute)
	e.tick()
	if prs := e.gh.PRs("acme/app"); len(prs) != 1 {
		t.Fatalf("the directive PR must wait for the open security PR: %+v", prs)
	}
	if dj := e.toolchainJob("acme/app", "go", fmt.Sprintf("v1.%d.9", n-1)); !strings.Contains(dj.Reason, fmt.Sprintf("/pull/%d", e.gh.PRs("acme/app")[0].Number)) {
		t.Errorf("the reason must link the security PR: %q", dj.Reason)
	}
}

// Two advisories fixed at different patches: the higher fix goes first and the lower one
// is then moot.
func TestTheHigherSecurityToolchainFixGoesFirst(t *testing.T) {
	e, _, osv, n := conflictEnv(t, store.NewMemory())
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7200", e.clock.Now(), toolchainFix(n))
	osv.Publish("GO-2026-7201", e.clock.Now(), osvRecord("GO-2026-7201", "stdlib", fmt.Sprintf(`,{"fixed":"1.%d.5"}`, n-2)))
	start(t, e, "acme/app")
	e.tick()
	prs := e.gh.PRs("acme/app")
	if len(prs) != 1 {
		t.Fatalf("want one PR for the two advisories: %+v", prs)
	}
	mod, _ := e.gh.FileAt("acme/app", prs[0].Head, "go.mod")
	if !strings.Contains(mod, "toolchain "+goVer(n-2, 9)) {
		t.Errorf("the higher fix must win:\n%s", mod)
	}
}

func (e *env) toolchainJob(repo, pkg, version string) model.Job {
	e.t.Helper()
	j, ok, err := e.st.GetJob(context.Background(), model.JobKey(repo, model.DatasourceGoToolchain, pkg, version))
	if err != nil || !ok {
		e.t.Fatalf("toolchain job %s %s %s: ok=%v err=%v", repo, pkg, version, ok, err)
	}
	return j
}

// Entry 106: a directive PR that is already open when a security toolchain PR arrives is
// closed (untouched, with a reason) and its job waits for the security PR, so the two never
// conflict on the toolchain line; once the security PR is merged the directive PR returns.
func TestASecurityToolchainPRClosesAnOpenDirectivePR(t *testing.T) {
	forEachFake(t, func(t *testing.T, st store.Store) {
		e, _, osv, n := conflictEnv(t, st)
		start(t, e, "acme/app")
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 || strings.HasPrefix(prs[0].Title, "[security]") {
			t.Fatalf("precondition: want the directive PR open: %+v", prs)
		}
		directive := prs[0]

		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7200", e.clock.Now(), toolchainFix(n))
		e.tick()
		e.tick()

		var security int
		for _, p := range e.gh.PRs("acme/app") {
			if strings.HasPrefix(p.Title, "[security] Update the Go toolchain") && !p.Closed {
				security++
			}
		}
		if security != 1 {
			t.Fatalf("want the security PR open: %+v", e.gh.PRs("acme/app"))
		}
		var got testutil.PR
		for _, p := range e.gh.PRs("acme/app") {
			if p.Number == directive.Number {
				got = p
			}
		}
		if !got.Closed || !strings.Contains(strings.Join(got.Comments, "\n"), "security") {
			t.Fatalf("the directive PR must be closed with a reason: %+v", got)
		}
		dj := e.toolchainJob("acme/app", "go", fmt.Sprintf("v1.%d.9", n-1))
		if dj.State != model.JobPending || !strings.Contains(dj.Reason, "security") {
			t.Fatalf("the directive job must wait for the security PR: %+v", dj)
		}
	})
}

// A directive PR a person touched is theirs: the security PR still opens, beside it.
func TestASecurityToolchainPRLeavesATouchedDirectivePRAlone(t *testing.T) {
	e, _, osv, n := conflictEnv(t, store.NewMemory())
	start(t, e, "acme/app")
	e.tick()
	directive := e.gh.PRs("acme/app")[0]
	e.gh.HumanCommitOnBranch("acme/app", directive.Head)
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7200", e.clock.Now(), toolchainFix(n))
	e.tick()
	e.tick()
	for _, p := range e.gh.PRs("acme/app") {
		if p.Number == directive.Number && p.Closed {
			t.Fatalf("a touched PR was closed: %+v", p)
		}
	}
}

// Entry 127: a security toolchain PR that is open when a higher security toolchain fix
// arrives edits the same line, so it is closed (untouched, with a reason) and only the
// higher one stays open. The supersede flow already does this for the same package, which
// is what this test pins; entry 127 listed it as open.
func TestAHigherSecurityToolchainPRClosesAnOpenLowerOne(t *testing.T) {
	forEachFake(t, func(t *testing.T, st store.Store) {
		e, _, osv, n := conflictEnv(t, st)
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7201", e.clock.Now(), osvRecord("GO-2026-7201", "stdlib", fmt.Sprintf(`,{"fixed":"1.%d.5"}`, n-2)))
		start(t, e, "acme/app")
		e.tick()
		var lower testutil.PR
		for _, p := range e.gh.PRs("acme/app") {
			if strings.HasPrefix(p.Title, "[security] Update the Go toolchain") {
				lower = p
			}
		}
		if lower.Number == 0 {
			t.Fatalf("precondition: want the lower security PR open: %+v", e.gh.PRs("acme/app"))
		}

		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7200", e.clock.Now(), toolchainFix(n))
		e.tick()
		e.tick()

		open := 0
		for _, p := range e.gh.PRs("acme/app") {
			switch {
			case p.Number == lower.Number:
				if !p.Closed || !strings.Contains(strings.Join(p.Comments, "\n"), "Superseded") {
					t.Errorf("the lower security PR must be closed with a reason: %+v", p)
				}
			case !p.Closed && strings.HasPrefix(p.Title, "[security] Update the Go toolchain"):
				open++
			}
		}
		if open != 1 {
			t.Errorf("want exactly the higher security PR open: %+v", e.gh.PRs("acme/app"))
		}
	})
}
