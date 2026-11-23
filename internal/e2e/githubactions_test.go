package e2e

import (
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

const actionsConfig = "updates:\n  - ecosystem: github-actions\n"

// actionsUpstream is a fake GitHub Action repo with one tag per entry, each on its
// own commit, in the order given. Entries ending in "!" are annotated tags. It returns
// the commit of each tag.
func actionsUpstream(e *env, repo string, tags ...string) map[string]string {
	e.gh.AddRepo(repo, map[string]string{"action.yml": "name: up\nruns:\n  using: node20\n"})
	return addTags(e, repo, tags...)
}

func addTags(e *env, repo string, tags ...string) map[string]string {
	shas := map[string]string{}
	for i, t := range tags {
		e.gh.Commit(repo, map[string]string{"n": strings.Repeat("x", i+1) + t})
		if name, ok := strings.CutSuffix(t, "!"); ok {
			shas[name] = e.gh.AnnotatedTag(repo, name)
		} else {
			shas[t] = e.gh.Tag(repo, t)
		}
	}
	return shas
}

func validYAML(t *testing.T, s string) {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		t.Errorf("file is no longer valid YAML: %v\n%s", err, s)
	}
}

func TestActionsTagBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4", "v4.1.0")
		ci := "name: ci   # keep me\non: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: up/action@v4\n        with: {x: 1}\n      - uses: up/action/sub@v4 # sub-action\n      - uses: ./local\n      - uses: docker://alpine:3.19\n      - uses: up/action@main\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": ci})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5", "v5.0.0")
		e.gh.Release("up/action", "v5", t0.Add(-time.Hour))
		e.tick()
		e.tick() // a replay must not open a second PR

		head, body := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, ".github/workflows/ci.yml")
		want := strings.NewReplacer("up/action@v4\n", "up/action@v5\n", "up/action/sub@v4 #", "up/action/sub@v5 #").Replace(ci)
		if got != want {
			t.Errorf("only the two v4 refs should move, formatting kept:\n%s", got)
		}
		validYAML(t, got)
		for _, w := range []string{"up/action", "`v4`", "`v5`", "https://github.com/up/action/releases/tag/v5"} {
			if !strings.Contains(body, w) {
				t.Errorf("PR body lacks %q:\n%s", w, body)
			}
		}
		if strings.Contains(body, "module index") || !strings.Contains(body, "when a new tag appeared upstream") {
			t.Errorf("the footer must not claim the Go module index:\n%s", body)
		}
		if !strings.HasPrefix(head, "ripple/actions/") {
			t.Errorf("branch %q", head)
		}
	})
}

func TestActionsSHAPinnedBumpMovesCommentAndAnnotatedTagIsDereferenced(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		shas := actionsUpstream(e, "up/action", "v4.1.2!")
		ci := "jobs:\n  a:\n    steps:\n      - uses: up/action@" + shas["v4.1.2"] + " # v4.1.2\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yaml": ci})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		newShas := addTags(e, "up/action", "v4.2.0!")
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, ".github/workflows/ci.yaml")
		want := "jobs:\n  a:\n    steps:\n      - uses: up/action@" + newShas["v4.2.0"] + " # v4.2.0\n"
		if got != want {
			t.Errorf("want the commit (not the tag object) and the comment moved together:\n%s\nwant:\n%s", got, want)
		}
		if newShas["v4.2.0"] == shas["v4.1.2"] {
			t.Fatal("fixture: tags share a commit")
		}
		validYAML(t, got)
	})
}

func TestActionsCompositeActionAndReusableWorkflow(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v1.0.0")
		actionsUpstream(e, "up/wf", "v2")
		e.gh.AddRepo("acme/app", map[string]string{
			".ripple.yaml":                 actionsConfig,
			".github/actions/x/action.yml": "runs:\n  using: composite\n  steps:\n    - uses: up/action@v1.0.0\n",
			".github/workflows/r.yml":      "jobs:\n  r:\n    uses: up/wf/.github/workflows/x.yml@v2\n",
		})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v1.1.0")
		addTags(e, "up/wf", "v3")
		e.tick()
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("want one PR per action, got %+v", prs)
		}
		for _, pr := range prs {
			if f, _ := e.gh.FileAt("acme/app", pr.Head, ".github/actions/x/action.yml"); strings.Contains(pr.Head, "up-action") && !strings.Contains(f, "up/action@v1.1.0") {
				t.Errorf("composite action not bumped:\n%s", f)
			}
			if f, _ := e.gh.FileAt("acme/app", pr.Head, ".github/workflows/r.yml"); strings.Contains(pr.Head, "up-wf") && !strings.Contains(f, "x.yml@v3") {
				t.Errorf("reusable workflow not bumped:\n%s", f)
			}
		}
	})
}

func TestActionsUnchangedAndIgnoredFormsOpenNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		shas := actionsUpstream(e, "up/action", "v4")
		ci := "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n      - uses: up/action@main\n      - uses: ./local\n      - uses: docker://alpine:3.19\n      - uses: up/action@" + shas["v4"] + "\n      - uses: up/action@" + shas["v4"] + " # not a version\n"
		e.gh.AddRepo("acme/current", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": ci})
		// Not opted in: the same file, with a newer tag available, is none of ripple's business.
		e.gh.AddRepo("acme/optout", map[string]string{".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		start(t, e, "acme/current", "acme/optout")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5", "main2")
		e.tick()
		e.tick()

		if got := len(e.deps("acme/current")); got != 1 {
			t.Errorf("only the up/action@v4 tag reference is tracked, got %+v", e.deps("acme/current"))
		}
		for _, d := range e.deps("acme/current") {
			if d.Datasource != model.DatasourceGitHubTags {
				t.Errorf("unexpected dependency %+v", d)
			}
		}
		if len(e.deps("acme/optout")) != 0 {
			t.Errorf("a repo that did not opt in has dependencies: %+v", e.deps("acme/optout"))
		}
		// acme/current has v4 and v5 exists: that one is a real update. Everything else must not be.
		prs := e.gh.PRs("acme/current")
		if len(prs) != 1 {
			t.Fatalf("want exactly the v4 -> v5 PR, got %+v", prs)
		}
		f, _ := e.gh.FileAt("acme/current", prs[0].Head, ".github/workflows/ci.yml")
		if want := strings.Replace(ci, "up/action@v4\n", "up/action@v5\n", 1); f != want {
			t.Errorf("ignored forms were touched:\n%s", f)
		}
		if n := len(e.gh.PRs("acme/optout")); n != 0 {
			t.Errorf("opted-out repo got %d PRs", n)
		}
		// Nothing newer than what is there: a rerun is a no-op.
		e.tick()
		if n := len(e.gh.PRs("acme/current")); n != 1 {
			t.Errorf("replay opened another PR: %d", n)
		}
	})
}

func TestActionsAlreadyCurrentOpensNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4", "v5")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v5\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("an up-to-date repo got %d PRs", n)
		}
		if b := e.gh.Branches("acme/app"); len(b) != 0 {
			t.Errorf("branches pushed: %v", b)
		}
	})
}

func TestActionsMissingWorkflowsPermissionOpensNoPR(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		e.gh.DenyWorkflowPush("acme/app")
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5")
		e.tick()
		e.tick()

		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("a PR was opened without the permission: %+v", e.gh.PRs("acme/app"))
		}
		if b := e.gh.Branches("acme/app"); len(b) != 0 {
			t.Errorf("a branch was pushed: %v", b)
		}
		j, ok, err := e.st.GetJob(t.Context(), model.JobKey("acme/app", model.DatasourceGitHubTags, "up/action", "v5"))
		if err != nil || !ok {
			t.Fatalf("job: %v %v", ok, err)
		}
		if j.State != model.JobPending || !strings.Contains(j.Reason, "workflows permission") || j.Attempts != 0 {
			t.Errorf("job should wait, with the reason the dashboard shows: %+v", j)
		}
		if !strings.Contains(e.logs.String(), "workflows permission") {
			t.Errorf("reason not logged:\n%s", e.logs.String())
		}
		// The permission is granted: the same job goes through a day later.
		e.gh.AllowWorkflowPush("acme/app")
		e.clock.Advance(25 * time.Hour)
		e.tick()
		onePR(t, e, "acme/app", false)
	})
}
