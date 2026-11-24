package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// hubListings counts the fake registry's Docker Hub tag-listing requests and the lexical
// tags/list ones.
func hubListings(reg interface{ Requests() []string }) (hub, lexical int) {
	for _, r := range reg.Requests() {
		switch {
		case strings.Contains(r, "/v2/namespaces/"):
			hub++
		case strings.HasSuffix(r, "/tags/list"):
			lexical++
		}
	}
	return
}

// A long-lived image whose history fills many Hub pages: with the opt-in the poll reads the
// newest page or two, not every page, and still finds the update.
func TestDockerHubNewestReadsOnlyTheNewestPages(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnvOpts(t, st, true)
		for i := 0; i < 450; i++ {
			reg.AddTag("library/nginx", fmt.Sprintf("0.%d.0", i)) // old history, pushed first
		}
		reg.AddTag("library/nginx", "1.25")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": "FROM nginx:1.25\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("library/nginx", "1.26")
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		if got, _ := e.gh.FileAt("acme/app", head, "Dockerfile"); got != "FROM nginx:1.26\n" {
			t.Errorf("the update was not found:\n%s", got)
		}
		hub, lexical := hubListings(reg)
		// 451 tags is 5 pages of 100; reading the newest two per poll is 2 requests, not 5.
		if hub == 0 || lexical != 0 || hub > 6 {
			t.Errorf("want only a few Hub pages and no lexical listing over the polls, got hub=%d lexical=%d", hub, lexical)
		}
	})
}

// A repository the Hub does not list (not on docker.io) and a Hub that does not honour the
// ordering both fall back to the registry's own list, with the same result.
func TestDockerHubNewestFallsBack(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnvOpts(t, st, true)
		reg.AddTag("org/img", "v2.3.4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": "FROM ghcr.io/org/img:v2.3.4\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.AddTag("org/img", "v2.4.0")
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		if got, _ := e.gh.FileAt("acme/app", head, "Dockerfile"); got != "FROM ghcr.io/org/img:v2.4.0\n" {
			t.Errorf("ghcr must be read through the registry API:\n%s", got)
		}
		if hub, lexical := hubListings(reg); hub != 0 || lexical == 0 {
			t.Errorf("a non-Hub registry must never be asked for Hub's listing: hub=%d lexical=%d", hub, lexical)
		}
	})
}

// Without the opt-in nothing changes: the registry's lexical list is read.
func TestDockerHubNewestIsOffByDefault(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := dockerEnvOpts(t, st, false)
		reg.AddTag("library/nginx", "1.25")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": dockerConfig, "Dockerfile": "FROM nginx:1.25\n"})
		start(t, e, "acme/app")
		if hub, lexical := hubListings(reg); hub != 0 || lexical == 0 {
			t.Errorf("hub=%d lexical=%d", hub, lexical)
		}
	})
}
