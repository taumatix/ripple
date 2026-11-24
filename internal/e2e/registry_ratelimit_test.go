package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// A package registry that answers a poll with 429 and Retry-After does not cost the watcher
// its update: the client waits the named time and the same tick opens the PR. Without the wait
// the read is an error, the watcher skips the package, and the PR would only come a poll later.
func TestNpmWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		reg.Publish("left-pad", "1.2.3")
		reg.Publish("@types/node", "20.1.0")
		reg.Publish("jest", "29.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": npmPackageJSON, "package-lock.json": "{}\n"})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		reg.Publish("left-pad", "1.5.0")
		reg.Refuse("left-pad", 1, 429, "0")
		e.tick()
		if reg.Refused() != 1 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d: the 429 should be waited out and left-pad bumped in the same tick", reg.Refused(), len(e.gh.PRs("acme/app")))
		}
	})
}

func TestCargoWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		idx.Publish("tokio", "1.2.3")
		idx.Publish("pretty_assertions", "1.4.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.toml": cargoManifest, "Cargo.lock": "# lock\n"})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.200")
		idx.Refuse("serde", 1, 429, "0")
		e.tick()
		if idx.Refused() != 1 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d", idx.Refused(), len(e.gh.PRs("acme/app")))
		}
	})
}

func TestPipWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		for _, p := range [][2]string{{"requests", "2.31.0"}, {"flask-login", "0.6.2"}, {"click", "8.0"}} {
			idx.Publish(p[0], p[1])
		}
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig, "requirements.txt": pipReqs,
			"svc/pyproject.toml": pipPyproject, "poetry.lock": "# lock\n"})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		idx.Publish("click", "8.1.7")
		idx.Refuse("/pypi/click/", 1, 429, "0")
		e.tick()
		if idx.Refused() != 1 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d", idx.Refused(), len(e.gh.PRs("acme/app")))
		}
	})
}

// A container registry that limits the tag list is waited out, and an image with a long
// history is read from its floor (`last=`) rather than from the first page, with the same bump.
func TestDockerWatcherSurvivesA429AndReadsFromTheFloor(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnv(t, st)
		for i := 0; i < 30; i++ {
			reg.AddTag("library/nginx", fmt.Sprintf("0.%d.0", i))
		}
		reg.AddTag("library/nginx", "1.25")
		reg.PageSize(5)
		df := "FROM nginx:1.25 AS web\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": df})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		reg.AddTag("library/nginx", "1.26")
		reg.Refuse("/tags/list", 1, 429, "0")
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		if got, _ := e.gh.FileAt("acme/app", head, "Dockerfile"); got != "FROM nginx:1.26 AS web\n" {
			t.Errorf("Dockerfile:\n%s", got)
		}
		if reg.Refused() != 1 {
			t.Errorf("the 429 was not met: %d", reg.Refused())
		}
		// 31 old tags at 5 a page is 7 pages (each asked twice: the 401 challenge, then the
		// token), so even one full read would be 14 requests; two floored reads are a few.
		n := 0
		for _, r := range reg.Requests() {
			if strings.Contains(r, "tags/list") {
				n++
			}
		}
		if n == 0 || n > 8 {
			t.Errorf("%d tag-list requests: the image was read through its old pages", n)
		}
	})
}
