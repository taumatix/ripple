//go:build live

package packagist

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read repo.packagist.org's p2 metadata anonymously and read only (go test -tags
// live; the live workflow), through the production Client. monolog/monolog has both "v"
// prefixed and bare tags, dev branches and prereleases over its life. Not covered: abandoned
// packages (the field is not read).

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveVersionsAndReleases(t *testing.T) {
	vs, err := liveClient().Versions(context.Background(), "monolog/monolog")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) < 50 {
		t.Fatalf("%d versions", len(vs))
	}
	rel := Releases(vs)
	for _, want := range []string{"2.0.0", "3.0.0"} {
		if !slices.Contains(rel, want) {
			t.Errorf("Releases lacks %s (raw has it: %v or v-prefixed: %v)", want, slices.Contains(vs, want), slices.Contains(vs, "v"+want))
		}
	}
	for _, r := range rel {
		if r == "" || r[0] == 'v' || len(r) < 5 {
			t.Errorf("Releases lists %q, want bare X.Y.Z", r)
		}
	}
}

func TestLiveMissingPackage(t *testing.T) {
	if _, err := liveClient().Versions(context.Background(), "ripple-no-such/package-zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want ErrNotFound", err)
	}
}
