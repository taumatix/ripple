//go:build live

package pypi

import (
	"context"
	"errors"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read pypi.org's JSON API anonymously and read only (go test -tags live; the
// live workflow). Fixtures: requests 2.32.0 and 2.32.1 are yanked, requests 2.15.0 has no
// files, django publishes alpha and rc releases.

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func find(p Project, v string) (Release, bool) {
	for _, r := range p.Releases {
		if r.Version == v {
			return r, true
		}
	}
	return Release{}, false
}

func TestLiveYankedAndFilelessReleasesAreNotProposed(t *testing.T) {
	p, err := liveClient().Project(context.Background(), "requests")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"2.32.0", "2.32.1"} {
		if r, ok := find(p, v); !ok || !r.Yanked || r.Files == 0 {
			t.Errorf("requests %s: %+v found=%v, want yanked with files", v, r, ok)
		}
	}
	if r, ok := find(p, "2.15.0"); !ok || r.Files != 0 {
		t.Errorf("requests 2.15.0: %+v found=%v, want no files", r, ok)
	}
	if r, ok := find(p, "2.31.0"); !ok || r.Yanked {
		t.Errorf("requests 2.31.0: %+v, want a plain release", r)
	}
	n := p.Newest("2.31.0")
	if n == "" || n == "2.32.0" || n == "2.32.1" {
		t.Errorf("Newest(2.31.0) = %q", n)
	}
	if got := p.Newest(n); got != "" {
		t.Errorf("Newest(%s) = %q, want none", n, got)
	}
}

func TestLivePrereleasesAreNeverProposed(t *testing.T) {
	p, err := liveClient().Project(context.Background(), "django")
	if err != nil {
		t.Fatal(err)
	}
	pre := 0
	for _, r := range p.Releases {
		if v, ok := Parse(r.Version); ok && !v.IsRelease() {
			pre++
		}
	}
	if pre == 0 {
		t.Fatal("django lists no pre-release the parser recognises")
	}
	n := p.Newest("4.2.0")
	if v, ok := Parse(n); n == "" || !ok || !v.IsRelease() {
		t.Errorf("Newest(4.2.0) = %q", n)
	}
}

func TestLiveNamesNeedNoNormalisationByTheCaller(t *testing.T) {
	c := liveClient()
	for _, name := range []string{"Typing_Extensions", "zope.interface"} {
		p, err := c.Project(context.Background(), name)
		if err != nil || len(p.Releases) == 0 {
			t.Errorf("%s: %v (%d releases)", name, err, len(p.Releases))
		}
	}
	if _, err := c.Project(context.Background(), "ripple-no-such-zz9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project: %v, want ErrNotFound", err)
	}
}

// The parser must read every version the index really holds for projects with old and odd
// schemes (a corpus check, since the PEP 440 code was written from a summary).
func TestLiveEveryRealVersionParsesAndOrdersTotally(t *testing.T) {
	c := liveClient()
	for _, name := range []string{"django", "requests", "numpy", "setuptools"} {
		p, err := c.Project(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		var vs []Version
		for _, r := range p.Releases {
			v, ok := Parse(r.Version)
			if !ok {
				t.Errorf("%s %q does not parse", name, r.Version)
				continue
			}
			vs = append(vs, v)
		}
		for i := range vs {
			if Compare(vs[i], vs[i]) != 0 {
				t.Errorf("%s: a version differs from itself", name)
			}
			if i > 0 && Compare(vs[i-1], vs[i])+Compare(vs[i], vs[i-1]) != 0 {
				t.Errorf("%s: Compare is not antisymmetric", name)
			}
		}
	}
}
