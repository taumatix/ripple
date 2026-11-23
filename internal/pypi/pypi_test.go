package pypi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taumatix/ripple/internal/pypi"
	"github.com/taumatix/ripple/internal/testutil"
)

// Orderings are the PEP 440 examples: dev < a < b < rc < final < post, epoch first,
// missing release segments are zero, spelling variants normalize.
func TestPEP440Ordering(t *testing.T) {
	// 1.0a2.dev3 sorts before 1.0a2 but after 1.0a1.
	ordered := []string{"1.0.dev1", "1.0a1", "1.0a2.dev3", "1.0a2", "1.0b1", "1.0rc1", "1.0", "1.0.post1", "1.0.post2", "1.1.dev1", "1.1", "1.10", "2!0.1"}
	for i := 0; i+1 < len(ordered); i++ {
		a, ok1 := pypi.Parse(ordered[i])
		b, ok2 := pypi.Parse(ordered[i+1])
		if !ok1 || !ok2 {
			t.Fatalf("unparsable %q or %q", ordered[i], ordered[i+1])
		}
		if pypi.Compare(a, b) >= 0 || pypi.Compare(b, a) <= 0 {
			t.Errorf("%s should sort before %s", ordered[i], ordered[i+1])
		}
	}
	eq := [][2]string{{"1.0", "1.0.0"}, {"1.0RC1", "1.0c1"}, {"v1.0-alpha.2", "1.0a2"}, {"1.0-1", "1.0.post1"}, {"1.0.rev3", "1.0post3"}}
	for _, p := range eq {
		a, _ := pypi.Parse(p[0])
		b, _ := pypi.Parse(p[1])
		if pypi.Compare(a, b) != 0 {
			t.Errorf("%s != %s", p[0], p[1])
		}
	}
	if _, ok := pypi.Parse("not-a-version"); ok {
		t.Error("garbage parsed")
	}
}

func TestProjectNewestSkipsPrereleasesYankedAndFileless(t *testing.T) {
	idx := testutil.NewPyPI(t)
	for _, v := range []string{"1.0.1", "1.0.2", "1.1.0rc1", "1.0.9", "1.0.10", "1.0.11.dev1", "1.0.12+local", "1.0.13"} {
		idx.Publish("Some_Project", v)
	}
	idx.Yank("Some_Project", "1.0.13")
	idx.PublishEmpty("Some_Project", "1.0.14")
	c := &pypi.Client{BaseURL: idx.URL}
	p, err := c.Project(context.Background(), "Some_Project")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Newest("1.0.1"); got != "1.0.10" { // numeric, not lexical (1.0.9 < 1.0.10)
		t.Errorf("Newest = %q", got)
	}
	if got := p.Newest("1.0.10"); got != "" {
		t.Errorf("Newest = %q, want none", got)
	}
	for _, r := range idx.Requests() {
		if r.Authorization != "" || r.Path != "/pypi/some-project/json" {
			t.Errorf("request %+v: anonymous, at the normalized path", r)
		}
	}
}

func TestProjectErrors(t *testing.T) {
	idx := testutil.NewPyPI(t)
	idx.Publish("secret", "1.0")
	idx.Deny("secret")
	c := &pypi.Client{BaseURL: idx.URL}
	if _, err := c.Project(context.Background(), "ghost"); !errors.Is(err, pypi.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Project(context.Background(), "secret"); !errors.Is(err, pypi.ErrDenied) {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Project(context.Background(), "../etc"); err == nil {
		t.Error("a path-like name must be refused")
	}
}
