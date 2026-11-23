package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// ghesRepo is a repo that wants automerge for patch updates and has one to take.
func ghesRepo(e *env) {
	stubModule(e, "example.com/lib", "v1.0.0", "v1.0.1")
	e.gh.AddRepo("acme/g", map[string]string{
		"go.mod": gomod("example.com/g", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib"),
		".ripple.yaml": "updates:\n  - ecosystem: gomod\n    automerge: patch\n",
	})
}

func runGHES(t *testing.T, ghes *testutil.GHES, check func(e *env)) {
	forEach(t, func(t *testing.T, st store.Store) {
		// forEach reruns the body per store: one fake per run
		g := testutil.NewGHES(t)
		g.Versions, g.NoGraphQL, g.OldSchema = ghes.Versions, ghes.NoGraphQL, ghes.OldSchema
		e := newEnvOpts(t, st, defaultLimits(), opts{ghes: g})
		ghesRepo(e)
		start(t, e, "acme/g") // an installation webhook carrying GHES's headers
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.0.1")
		e.tick()
		if s := g.Strays(); len(s) != 0 {
			t.Errorf("requests outside /api/v3 and /api/graphql: %v", s)
		}
		check(e)
	})
}

func TestGHESUpdateAutomergesThroughApiGraphql(t *testing.T) {
	runGHES(t, &testutil.GHES{}, func(e *env) {
		prs := e.gh.PRs("acme/g")
		if len(prs) != 1 || !prs[0].AutoMerge {
			t.Fatalf("want one auto-merging PR, got %+v", prs)
		}
		if got, _ := e.gh.FileAt("acme/g", prs[0].Head, "go.mod"); !strings.Contains(got, "example.com/lib v1.0.1") {
			t.Errorf("go.mod on the PR branch:\n%s", got)
		}
	})
}

func TestGHESOlderReleaseStillGetsItsPR(t *testing.T) {
	runGHES(t, &testutil.GHES{Versions: []string{github.FallbackAPIVersion}}, func(e *env) {
		if prs := e.gh.PRs("acme/g"); len(prs) != 1 || !prs[0].AutoMerge {
			t.Fatalf("PRs %+v", prs)
		}
	})
}

func TestGHESWithoutGraphQLLeavesThePRForReview(t *testing.T) {
	runGHES(t, &testutil.GHES{NoGraphQL: true}, func(e *env) {
		prs := e.gh.PRs("acme/g")
		if len(prs) != 1 || prs[0].AutoMerge {
			t.Fatalf("want one PR without auto-merge, got %+v", prs)
		}
		if e.logLines("host has no auto-merge") != 1 || e.logLines("level=WARN") != 0 {
			t.Errorf("a missing GraphQL endpoint is not a warning:\n%s", e.logs.String())
		}
	})
}

func TestGHESSchemaWithoutAutoMergeLeavesThePRForReview(t *testing.T) {
	runGHES(t, &testutil.GHES{OldSchema: true}, func(e *env) {
		if prs := e.gh.PRs("acme/g"); len(prs) != 1 || prs[0].AutoMerge {
			t.Fatalf("PRs %+v", prs)
		}
		if e.logLines("host has no auto-merge") != 1 {
			t.Errorf("log:\n%s", e.logs.String())
		}
	})
}

// An action that lives on github.com is not on the GHES: its tags are read from the
// configured upstream, its SHA is resolved there, and the PR is still opened on the GHES.
func TestGHESActionsAreReadFromTheConfiguredUpstream(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		g := testutil.NewGHES(t)
		up := testutil.NewGitHub(t)
		up.PublicReads = true
		e := newEnvOpts(t, st, defaultLimits(), opts{ghes: g, upstream: up})
		up.AddRepo("up/action", map[string]string{"action.yml": "name: up\nruns:\n  using: node20\n"})
		up.Tag("up/action", "v4")
		up.Commit("up/action", map[string]string{"n": "5"})
		up.AnnotatedTag("up/action", "v5")
		up.Release("up/action", "v5", t0.Add(-time.Hour))
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, ".github/workflows/ci.yml")
		if !strings.Contains(got, "up/action@v5") {
			t.Errorf("the workflow was not moved to the upstream's v5:\n%s", got)
		}
		for _, b := range up.Bearers() {
			if strings.HasPrefix(b, "ghs_") {
				t.Errorf("the GHES installation token reached the upstream: %q", b)
			}
		}
		if s := g.Strays(); len(s) != 0 {
			t.Errorf("requests outside /api/v3 and /api/graphql: %v", s)
		}
	})
}
