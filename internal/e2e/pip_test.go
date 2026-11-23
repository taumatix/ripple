package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const pipConfig = "updates:\n  - ecosystem: pip\n"

func pipEnv(t *testing.T, st store.Store) (*env, *testutil.PyPI) {
	idx := testutil.NewPyPI(t)
	return newEnvOpts(t, st, defaultLimits(), opts{pypi: idx}), idx
}

const pipReqs = `# app
requests==2.31.0   # pinned
Flask_Login~=0.6.2
wild==1.*
hashed==1.0.0 \
    --hash=sha256:abc
`

const pipPyproject = `[project]
name = "app"
dependencies = [
  "click>=8.0",
  "requests==2.31.0",
]
`

func TestPipRequirementAndPyprojectBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		for _, p := range [][2]string{{"requests", "2.31.0"}, {"flask-login", "0.6.2"}, {"click", "8.0"}, {"wild", "1.0"}, {"hashed", "1.0.0"}} {
			idx.Publish(p[0], p[1])
		}
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig, "requirements.txt": pipReqs,
			"svc/pyproject.toml": pipPyproject, "poetry.lock": "# lock\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"requirements.txt": pipReqs})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 4 {
			t.Fatalf("requests (twice), flask-login and click are tracked, not wild or hashed: got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		idx.Publish("requests", "2.32.3")
		idx.Publish("requests", "2.33.0")
		idx.Yank("requests", "2.33.0")
		idx.Publish("requests", "3.0.0rc1") // a prerelease: ignored
		idx.Publish("flask-login", "0.6.10")
		idx.Publish("click", "8.1.7")
		idx.Publish("hashed", "1.0.1") // never moved: its hash would go stale
		idx.Publish("wild", "2.0")
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 3 {
			t.Fatalf("one PR per project (requests, flask-login, click), got %d", len(prs))
		}
		for _, pr := range prs {
			if !strings.HasPrefix(pr.Head, "ripple/pip/") {
				t.Errorf("branch %q", pr.Head)
			}
			reqs, _ := e.gh.FileAt("acme/app", pr.Head, "requirements.txt")
			pyp, _ := e.gh.FileAt("acme/app", pr.Head, "svc/pyproject.toml")
			wantReqs, wantPyp := pipReqs, pipPyproject
			switch {
			case strings.Contains(pr.Head, "requests"):
				wantReqs = strings.Replace(pipReqs, "requests==2.31.0", "requests==2.32.3", 1)
				wantPyp = strings.Replace(pipPyproject, "requests==2.31.0", "requests==2.32.3", 1)
			case strings.Contains(pr.Head, "flask-login"):
				wantReqs = strings.Replace(pipReqs, "~=0.6.2", "~=0.6.10", 1)
			case strings.Contains(pr.Head, "click"):
				wantPyp = strings.Replace(pipPyproject, "click>=8.0", "click>=8.1.7", 1)
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if reqs != wantReqs || pyp != wantPyp {
				t.Errorf("%s: only the version moves:\n%s\n%s", pr.Head, reqs, pyp)
			}
			lock, _ := e.gh.FileAt("acme/app", pr.Head, "poetry.lock")
			if lock != "# lock\n" {
				t.Errorf("%s: the lock file must be left alone, got %q", pr.Head, lock)
			}
			if strings.Contains(pr.Head, "click") || strings.Contains(pr.Head, "requests") {
				for _, w := range []string{"Lock files not regenerated", "`poetry.lock`"} {
					if !strings.Contains(pr.Body, w) {
						t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, pr.Body)
					}
				}
			}
		}
		for _, r := range idx.Requests() {
			if r.Authorization != "" || strings.Contains(r.Path, "hashed") || strings.Contains(r.Path, "wild") {
				t.Errorf("request %+v: anonymous", r)
			}
		}
		if len(e.gh.PRs("acme/optout")) != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestPipExtraIndexIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		idx.Publish("requests", "2.31.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig,
			"a/requirements.txt": "--extra-index-url https://pypi.corp.example/simple\nrequests==2.31.0\n",
			"b/requirements.txt": "requests==2.31.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("requests", "2.31.1")
		e.tick()
		e.tick()
		head, _ := onePR(t, e, "acme/app", false)
		a, _ := e.gh.FileAt("acme/app", head, "a/requirements.txt")
		b, _ := e.gh.FileAt("acme/app", head, "b/requirements.txt")
		if !strings.HasSuffix(a, "requests==2.31.0\n") || b != "requests==2.31.1\n" {
			t.Errorf("only the file without an index option moves:\n%s\n%s", a, b)
		}
	})
}

func TestPipPrivateIndexOnlyRepoOpensNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		idx.Publish("requests", "2.31.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig,
			"requirements.txt": "-i https://pypi.corp.example/simple\nrequests==2.31.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("requests", "2.31.1")
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("a private index got %d PRs", n)
		}
		j, ok, err := e.st.GetJob(context.Background(), model.JobKey("acme/app", model.DatasourcePip, "requests", "2.31.1"))
		if err != nil || !ok || j.State != model.JobSkipped || !strings.Contains(j.Reason, "requirements.txt") {
			t.Errorf("job = %+v, %v, %v", j, ok, err)
		}
	})
}

func TestPipDeniedProjectIsSkippedAndOthersStillMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		idx.Publish("requests", "2.31.0")
		idx.Publish("secret", "1.0.0")
		idx.Deny("secret")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig,
			"requirements.txt": "requests==2.31.0\nsecret==1.0.0\nghost==2.0.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("requests", "2.31.1")
		idx.Publish("secret", "1.0.1")
		e.tick()
		e.tick()
		head, body := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "requirements.txt")
		if want := "requests==2.31.1\nsecret==1.0.0\nghost==2.0.0\n"; got != want {
			t.Errorf("%s", got)
		}
		if strings.Contains(body, "not regenerated") {
			t.Errorf("no lock exists, none should be reported:\n%s", body)
		}
	})
}

func TestPipUpToDateOpensNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		idx.Publish("requests", "2.31.0")
		idx.Publish("requests", "3.0.0.dev1")
		idx.Publish("requests", "2.32.0")
		idx.Yank("requests", "2.32.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig, "requirements.txt": "requests==2.31.0\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("an up-to-date repo got %d PRs", n)
		}
	})
}
