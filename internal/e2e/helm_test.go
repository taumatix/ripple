package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const helmConfig = "updates:\n  - ecosystem: helm\n"

func helmEnv(t *testing.T, st store.Store) (*env, *testutil.HelmRepo) {
	repo := testutil.NewHelmRepo(t)
	return newEnvOpts(t, st, defaultLimits(), opts{helm: repo}), repo
}

// helmChart is a Chart.yaml with one dependency of each shape ripple reads or leaves alone.
func helmChart(repo string) string {
	return `apiVersion: v2
name: app
version: 0.1.0   # the chart's own version is never a dependency
dependencies:
  - name: postgresql
    version: 12.1.0
    repository: ` + repo + `
  - name: redis
    version: "~17.3.1"   # keeps its operator
    repository: "` + repo + `/"
  - name: wild
    version: ">=1.0.0 <2.0.0"
    repository: ` + repo + `
  - name: local
    version: 0.1.0
    repository: file://../local
  - name: aliased
    version: 1.0.0
    repository: "@stable"
  - name: oci
    version: 1.0.0
    repository: oci://registry.example/charts
  - name: plain-http
    version: 1.0.0
    repository: http://charts.example.com
`
}

func TestHelmDependencyBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, repo := helmEnv(t, st)
		for _, c := range []string{"postgresql", "redis", "wild", "local", "aliased", "oci", "plain-http"} {
			repo.Publish(c, "1.0.0")
		}
		repo.Publish("postgresql", "12.1.0")
		repo.Publish("redis", "17.3.1")
		manifest := helmChart(repo.URL)
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": helmConfig, "Chart.yaml": manifest, "Chart.lock": "digest: sha256:00\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"Chart.yaml": manifest})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 2 {
			t.Fatalf("postgresql and redis are tracked (not a range, a local chart, an alias, OCI or plain http), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		repo.Publish("postgresql", "12.2.0")
		repo.Publish("postgresql", "12.3.0")
		repo.Deprecate("postgresql", "12.3.0")
		repo.Publish("postgresql", "13.0.0-rc.1") // a prerelease: ignored
		repo.Publish("redis", "17.3.9")
		repo.Publish("local", "9.0.0") // never read: a file:// dependency
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per chart (postgresql, redis), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "Chart.yaml")
			from, to := "", ""
			switch {
			case strings.Contains(pr.Head, "postgresql"):
				from, to = "version: 12.1.0", "version: 12.2.0"
			case strings.Contains(pr.Head, "redis"):
				from, to = `"~17.3.1"`, `"~17.3.9"`
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(manifest, from, to, 1); got != want {
				t.Errorf("%s: only that version moves, layout and comments kept:\n%s", pr.Head, got)
			}
			lock, _ := e.gh.FileAt("acme/app", pr.Head, "Chart.lock")
			if lock != "digest: sha256:00\n" {
				t.Errorf("%s: Chart.lock must be left alone, got %q", pr.Head, lock)
			}
			for _, w := range []string{"Chart.lock not regenerated", "`Chart.lock`", "helm dependency update"} {
				if !strings.Contains(pr.Body, w) {
					t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, pr.Body)
				}
			}
			if !strings.HasPrefix(pr.Head, "ripple/helm/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range repo.Requests() {
			if r.Authorization != "" || r.Path != "/index.yaml" {
				t.Errorf("request %+v: anonymous, and only ever for index.yaml", r)
			}
		}
		if n := len(e.gh.PRs("acme/optout")); n != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestHelmRequirementsYamlAndOneIndexRead(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, repo := helmEnv(t, st)
		repo.Publish("a", "1.0.0")
		repo.Publish("b", "2.0.0")
		req := "dependencies:\n- name: a\n  version: 1.0.0\n  repository: " + repo.URL + "\n- name: b\n  version: 2.0.0\n  repository: " + repo.URL + "\n"
		e.gh.AddRepo("acme/old", map[string]string{".ripple.yaml": helmConfig, "charts/x/requirements.yaml": req, "charts/x/requirements.lock": "digest: x\n"})
		start(t, e, "acme/old")
		e.clock.Advance(time.Minute)
		repo.Publish("a", "1.1.0")
		before := len(repo.Requests())
		e.tick()
		if got := len(repo.Requests()) - before; got != 1 {
			t.Errorf("two charts of one repository are one index read per poll, got %d", got)
		}
		head, body := onePR(t, e, "acme/old", false)
		got, _ := e.gh.FileAt("acme/old", head, "charts/x/requirements.yaml")
		if want := strings.Replace(req, "version: 1.0.0", "version: 1.1.0", 1); got != want {
			t.Errorf("%s", got)
		}
		if !strings.Contains(body, "`charts/x/requirements.lock`") {
			t.Errorf("the lock beside requirements.yaml is not reported:\n%s", body)
		}
	})
}

func TestHelmDeniedRepositoryIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, repo := helmEnv(t, st)
		repo.Publish("a", "1.0.0")
		repo.Deny()
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": helmConfig,
			"Chart.yaml": "dependencies:\n- name: a\n  version: 1.0.0\n  repository: " + repo.URL + "\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("a repository that refuses anonymous reads opened %d PRs", n)
		}
	})
}
