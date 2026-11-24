//go:build live

package cratesindex

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read index.crates.io anonymously and read only (go test -tags live; the live
// workflow). Fixtures: serde 1.0.95 is yanked, serde has 0.8.0-rc releases, log (3 letters)
// and cc (2 letters) live in the short-name directories.

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func has(c Crate, v string) (yanked, ok bool) {
	for _, x := range c.Versions {
		if x.Version == v {
			return x.Yanked, true
		}
	}
	return false, false
}

func TestLiveYankedAndPrereleaseVersionsAreNotProposed(t *testing.T) {
	c, err := liveClient().Crate(context.Background(), "serde")
	if err != nil {
		t.Fatal(err)
	}
	if y, ok := has(c, "1.0.95"); !ok || !y {
		t.Errorf("serde 1.0.95 yanked=%v found=%v, want yanked", y, ok)
	}
	if y, ok := has(c, "1.0.94"); !ok || y {
		t.Errorf("serde 1.0.94 yanked=%v found=%v, want a plain release", y, ok)
	}
	if _, ok := has(c, "0.8.0-rc1"); !ok {
		t.Error("serde 0.8.0-rc1 not read")
	}
	n := c.Newest("1.0.0")
	if n == "" || semver.Prerelease("v"+n) != "" || n == "1.0.95" {
		t.Errorf("Newest(1.0.0) = %q", n)
	}
	if got := c.Newest(n); got != "" {
		t.Errorf("Newest(%s) = %q, want none", n, got)
	}
}

func TestLiveIndexPathsOfEveryNameLength(t *testing.T) {
	cl := liveClient()
	for _, name := range []string{"cc", "log", "rand", "Serde_Json"} {
		c, err := cl.Crate(context.Background(), name)
		if err != nil || len(c.Versions) == 0 {
			t.Errorf("%s (%s): %v, %d versions", name, Path(name), err, len(c.Versions))
		}
	}
	if _, err := cl.Crate(context.Background(), "zzripplenope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing crate: %v, want ErrNotFound", err)
	}
}
