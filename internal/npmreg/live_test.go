//go:build live

package npmreg

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read registry.npmjs.org anonymously and read only (go test -tags live; the
// live workflow). Fixtures are long-stable facts: left-pad's releases are all deprecated,
// @types/ms is a scoped package, typescript publishes many prereleases.

func liveClient() (*Client, *livetest.Transport) {
	hc, tr := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}, tr
}

func TestLiveDeprecatedVersionsAreNeverProposed(t *testing.T) {
	c, _ := liveClient()
	p, err := c.Packument(context.Background(), "left-pad")
	if err != nil {
		t.Fatal(err)
	}
	if p.Latest != "1.3.0" {
		t.Fatalf("latest dist-tag %q, want 1.3.0", p.Latest)
	}
	dep := 0
	for _, v := range p.Versions {
		if v.Version == "1.3.0" && !v.Deprecated {
			t.Error("1.3.0 is deprecated on the registry; the abbreviated document's deprecated field was not read")
		}
		if v.Deprecated {
			dep++
		}
	}
	if dep == 0 {
		t.Fatal("no deprecated version read from a package whose releases are all deprecated")
	}
	for _, v := range p.Releases() {
		for _, pv := range p.Versions {
			if pv.Version == v && pv.Deprecated {
				t.Errorf("Releases lists deprecated %s", v)
			}
		}
	}
}

func TestLiveScopedPackageAndMissingOnes(t *testing.T) {
	c, _ := liveClient()
	p, err := c.Packument(context.Background(), "@types/ms")
	if err != nil {
		t.Fatalf("scoped name: %v", err)
	}
	if p.Latest == "" || len(p.Versions) == 0 {
		t.Fatalf("scoped packument empty: %+v", p.Latest)
	}
	for _, name := range []string{"ripple-no-such-package-zz9", "@ripple-no-such-scope/zz9"} {
		if _, err := c.Packument(context.Background(), name); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", name, err)
		}
	}
}

func TestLiveNewestSkipsPrereleasesAndStaysAtOrBelowLatest(t *testing.T) {
	c, _ := liveClient()
	p, err := c.Packument(context.Background(), "typescript")
	if err != nil {
		t.Fatal(err)
	}
	pre := 0
	for _, v := range p.Versions {
		if semver.Prerelease("v"+v.Version) != "" {
			pre++
		}
	}
	if pre == 0 {
		t.Fatal("typescript lists no prerelease; the fixture no longer shows what it should")
	}
	n := p.Newest("4.0.0")
	if n == "" || semver.Prerelease("v"+n) != "" || semver.Compare("v"+n, "v"+p.Latest) > 0 {
		t.Errorf("Newest(4.0.0) = %q with latest %q", n, p.Latest)
	}
	if got := p.Newest(p.Latest); got != "" {
		t.Errorf("Newest(latest) = %q, want none", got)
	}
}
