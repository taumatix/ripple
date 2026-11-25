//go:build live

package nuget

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read api.nuget.org anonymously and read only (go test -tags live; the live
// workflow), through the production Client: the v3 service index (PackageBaseAddress
// discovery) and the flat container's version list. Newtonsoft.Json 13.0.1 and its
// 13.0.1-beta1 style prereleases are permanent. Not covered: the listed/unlisted flag (not
// read).

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveVersionsAndReleases(t *testing.T) {
	vs, err := liveClient().Versions(context.Background(), "Newtonsoft.Json")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) < 50 {
		t.Fatalf("%d versions", len(vs))
	}
	pre := false
	for _, v := range vs {
		if len(v) > 0 && slices.Contains([]byte(v), '-') {
			pre = true
		}
	}
	if !pre || !slices.Contains(vs, "13.0.1") {
		t.Errorf("prerelease seen=%v, 13.0.1 present=%v", pre, slices.Contains(vs, "13.0.1"))
	}
	rel := Releases(vs)
	if !slices.Contains(rel, "13.0.1") {
		t.Error("Releases lacks 13.0.1")
	}
	for _, r := range rel {
		if slices.Contains([]byte(r), '-') {
			t.Errorf("Releases lists prerelease %s", r)
		}
	}
}

func TestLiveMissingPackage(t *testing.T) {
	if _, err := liveClient().Versions(context.Background(), "Ripple.No.Such.Package.Zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want ErrNotFound", err)
	}
}
