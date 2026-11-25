//go:build live

package maven

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read repo.maven.apache.org/maven2 anonymously and read only (go test -tags
// live; the live workflow), through the production Client: maven-metadata.xml for
// org.apache.commons:commons-lang3, whose 3.12.0 is immutable (Central never deletes). Not
// covered: other repositories (a private mirror, Google's), which need their own base URL.

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
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
