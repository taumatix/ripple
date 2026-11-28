//go:build live

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests pull anonymously from Docker Hub, ghcr.io and quay.io, read only (go test
// -tags live; the live workflow): tag lists, manifest digests and image config creation
// times, which go through each registry's Bearer token challenge. The fixtures are old,
// public, immutable tags whose creation year is a stable fact.

var liveImages = []struct {
	pkg, tag string
	year     int
}{
	{"docker.io/library/alpine", "3.18.0", 2023},
	{"ghcr.io/astral-sh/uv", "0.4.0", 2024},
	{"quay.io/prometheus/node-exporter", "v1.6.0", 2023},
}

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveTagsDigestAndCreatedThroughTheTokenChallenge(t *testing.T) {
	c := liveClient()
	ctx := context.Background()
	for _, im := range liveImages {
		t.Run(im.pkg, func(t *testing.T) {
			tags, err := c.Tags(ctx, im.pkg)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, tg := range tags {
				if tg == im.tag {
					found = true
				}
			}
			if !found || len(tags) < 20 {
				t.Fatalf("%d tags, %s present=%v", len(tags), im.tag, found)
			}
			d1, err := c.Digest(ctx, im.pkg, im.tag)
			if err != nil {
				t.Fatal(err)
			}
			d2, err := c.Digest(ctx, im.pkg, im.tag)
			if err != nil || d1 != d2 {
				t.Fatalf("digest not stable: %q %q (%v)", d1, d2, err)
			}
			created, err := c.Created(ctx, im.pkg, im.tag)
			if err != nil {
				t.Fatal(err)
			}
			if created.IsZero() || created.Year() != im.year || created.After(time.Now()) {
				t.Errorf("created %v, want a time in %d", created, im.year)
			}
			if n := NewestOfShape(im.tag, tags); n == "" || n == "latest" {
				t.Errorf("NewestOfShape(%s) = %q, want a newer tag of the same shape", im.tag, n)
			}
		})
	}
}

type liveHubPage struct {
	Count   int
	Results []struct {
		Name        string
		LastUpdated time.Time `json:"last_updated"`
	}
}

func liveHubGet(t *testing.T, query string) (liveHubPage, int, string) {
	t.Helper()
	hc, _ := livetest.Client()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://hub.docker.com/v2/namespaces/library/repositories/nginx/tags?"+query, nil)
	req.Header.Set("User-Agent", livetest.UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var p liveHubPage
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	return p, resp.StatusCode, string(b)
}

// ROADMAP 215: what Docker Hub's tag listing really does, since RIPPLE_REGISTRY_HUB_NEWEST was
// written from a guess. Fails loudly when Hub changes, so the option's fallback is not what
// silently hides it.
func TestLiveHubTagListingOrdering(t *testing.T) {
	newest, code, body := liveHubGet(t, "page=1&page_size=100&ordering=last_updated")
	if code != http.StatusOK || len(newest.Results) != 100 {
		t.Fatalf("ordering=last_updated: status %d, %d results, want 200 and page_size 100 honoured: %.200s", code, len(newest.Results), body)
	}
	for i := 1; i < len(newest.Results); i++ {
		if newest.Results[i].LastUpdated.After(newest.Results[i-1].LastUpdated) {
			t.Fatalf("ordering=last_updated is not newest first at %d: %s then %s", i, newest.Results[i-1].LastUpdated, newest.Results[i].LastUpdated)
		}
	}
	// The reverse spelling is the OLDEST first: sending -last_updated for newest was the bug.
	oldest, code, _ := liveHubGet(t, "page=1&page_size=100&ordering=-last_updated")
	if code != http.StatusOK || len(oldest.Results) == 0 || !oldest.Results[0].LastUpdated.Before(newest.Results[0].LastUpdated) {
		t.Errorf("ordering=-last_updated: status %d, want oldest first (first older than the newest page's first)", code)
	}
	if capped, code, _ := liveHubGet(t, "page=1&page_size=250&ordering=last_updated"); code != http.StatusOK || len(capped.Results) != 100 {
		t.Errorf("page_size=250: status %d, %d results, want the cap of 100", code, len(capped.Results))
	}
	if _, code, body := liveHubGet(t, "page=1&page_size=100&ordering=bogus"); code != http.StatusInternalServerError || !strings.Contains(body, "Invalid ordering value") {
		t.Errorf("an unknown ordering answered %d %.120s, want 500 Invalid ordering value", code, body)
	}

	// The client end to end: from a tag near the top, it reads the current page and the margin page.
	c := liveClient()
	c.HubNewest = true
	tags, ok, err := c.TagsNewest(context.Background(), "docker.io/library/nginx", []string{newest.Results[0].Name})
	if err != nil || !ok || len(tags) != 2*hubPageSize || tags[0] != newest.Results[0].Name {
		t.Fatalf("TagsNewest: ok=%v err=%v, %d tags, want 200 from the real Hub starting at %s", ok, err, len(tags), newest.Results[0].Name)
	}
}

func TestLiveMissingAndUnreadableImagesAreNamedErrors(t *testing.T) {
	c := liveClient()
	for _, pkg := range []string{"docker.io/library/ripple-no-such-image-zz9", "ghcr.io/ripple-no-such-owner/zz9", "quay.io/ripple-no-such-owner/zz9"} {
		_, err := c.Tags(context.Background(), pkg)
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v, want ErrNotFound or ErrDenied (a private image answers like a missing one)", pkg, err)
		}
	}
	if _, err := c.Digest(context.Background(), "docker.io/library/alpine", "ripple-no-such-tag-zz9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing tag: %v, want ErrNotFound", err)
	}
}
