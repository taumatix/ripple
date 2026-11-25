//go:build live

package rubygems

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read rubygems.org's versions API anonymously and read only (go test -tags
// live; the live workflow), through the production Client. Fixtures: rack 2.2.8 and
// 3.0.0.rc1 are published for good; the platform field is checked with nokogiri, which
// ships x86_64-linux and java builds beside the ruby one. Not covered: yanked gems (the API
// simply omits them).

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveVersionsAndReleases(t *testing.T) {
	vs, err := liveClient().Versions(context.Background(), "rack")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) < 50 {
		t.Fatalf("%d versions", len(vs))
	}
	var rc, plain bool
	for _, v := range vs {
		if v.Number == "3.0.0.rc1" && v.Prerelease {
			rc = true
		}
		if v.Number == "2.2.8" && !v.Prerelease {
			plain = true
		}
	}
	if !rc || !plain {
		t.Errorf("3.0.0.rc1 prerelease=%v, 2.2.8 plain=%v", rc, plain)
	}
	rel := Releases(vs)
	if !slices.Contains(rel, "2.2.8") || slices.Contains(rel, "3.0.0.rc1") {
		t.Errorf("Releases: 2.2.8 present=%v, rc1 absent=%v", slices.Contains(rel, "2.2.8"), !slices.Contains(rel, "3.0.0.rc1"))
	}
}

func TestLivePlatformBuildsAreOneRelease(t *testing.T) {
	vs, err := liveClient().Versions(context.Background(), "nokogiri")
	if err != nil {
		t.Fatal(err)
	}
	platforms := map[string]bool{}
	for _, v := range vs {
		platforms[v.Platform] = true
	}
	if !platforms["ruby"] || len(platforms) < 2 {
		t.Fatalf("platform field not read: %v", platforms)
	}
	seen := map[string]bool{}
	for _, r := range Releases(vs) {
		if seen[r] {
			t.Fatalf("release %s listed twice", r)
		}
		seen[r] = true
	}
}

func TestLiveMissingGem(t *testing.T) {
	if _, err := liveClient().Versions(context.Background(), "ripple-no-such-gem-zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want ErrNotFound", err)
	}
}
