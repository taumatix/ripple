//go:build live

package maven

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read repo.maven.apache.org/maven2 anonymously and read only (go test -tags
// live; the live workflow), through the production Client: maven-metadata.xml for
// org.apache.commons:commons-lang3, whose 3.12.0 is immutable (Central never deletes). Not
// covered: other repositories (a private mirror, Google's), which need their own base URL.

// Maven Central answers 429, with no Retry-After, to a busy shared address such as a GitHub-hosted
// runner (seen on 2026-11-25), so these tests wait out a longer backoff than the default before
// the 429 is reported as the failure it is.
func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent, Retry: httpretry.Policy{MaxAttempts: 6, MaxWait: time.Minute}}
}

func TestLiveVersionsAndReleases(t *testing.T) {
	vs, err := liveClient().Versions(context.Background(), "org.apache.commons:commons-lang3")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(vs, "3.12.0") || len(vs) < 20 {
		t.Fatalf("%d versions, 3.12.0 present=%v", len(vs), slices.Contains(vs, "3.12.0"))
	}
	if rel := Releases(vs); !slices.Contains(rel, "3.12.0") {
		t.Errorf("Releases lacks 3.12.0: %v", rel)
	}
}

func TestLiveMissingArtifact(t *testing.T) {
	if _, err := liveClient().Versions(context.Background(), "org.apache.commons:ripple-no-such-artifact"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want ErrNotFound", err)
	}
}
