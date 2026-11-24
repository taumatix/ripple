package cratesindex_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taumatix/ripple/internal/cratesindex"
	"github.com/taumatix/ripple/internal/testutil"
)

// The literal paths are the examples of the index documentation (docs/vendor/cargo):
// "cargo" lives at ca/rg/cargo.
func TestPathFollowsTheDocumentedPrefixRules(t *testing.T) {
	for name, want := range map[string]string{
		"a": "1/a", "ab": "2/ab", "abc": "3/a/abc", "cargo": "ca/rg/cargo", "Serde_JSON": "se/rd/serde_json",
	} {
		if got := cratesindex.Path(name); got != want {
			t.Errorf("Path(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCrateReadsVersionsAndYanks(t *testing.T) {
	idx := testutil.NewCratesIndex(t)
	for _, v := range []string{"1.0.1", "1.0.2", "1.1.0-rc.1", "1.0.9"} {
		idx.Publish("serde", v)
	}
	idx.Yank("serde", "1.0.9")
	c := &cratesindex.Client{BaseURL: idx.URL}
	cr, err := c.Crate(context.Background(), "serde")
	if err != nil || len(cr.Versions) != 4 {
		t.Fatalf("Crate = %+v, %v", cr, err)
	}
	if got := cr.Newest("1.0.1"); got != "1.0.2" {
		t.Errorf("Newest = %q: not the prerelease, not the yanked one", got)
	}
	if got := cr.Newest("1.0.2"); got != "" {
		t.Errorf("Newest = %q, want none", got)
	}
	for _, r := range idx.Requests() {
		if r.Authorization != "" || r.Path != "/se/rd/serde" {
			t.Errorf("request %+v: anonymous, at the documented path", r)
		}
	}
}

func TestCrateErrors(t *testing.T) {
	idx := testutil.NewCratesIndex(t)
	idx.Publish("secret", "1.0.0")
	idx.Deny("secret")
	c := &cratesindex.Client{BaseURL: idx.URL}
	if _, err := c.Crate(context.Background(), "ghost"); !errors.Is(err, cratesindex.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Crate(context.Background(), "secret"); !errors.Is(err, cratesindex.ErrDenied) {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Crate(context.Background(), "../etc"); err == nil {
		t.Error("a path-like name must be refused")
	}
}

func TestCrateReadsRustVersion(t *testing.T) {
	idx := testutil.NewCratesIndex(t)
	idx.Publish("serde", "1.0.1")
	idx.PublishMSRV("serde", "1.0.2", "1.70")
	cr, err := (&cratesindex.Client{BaseURL: idx.URL}).Crate(context.Background(), "serde")
	if err != nil || len(cr.Versions) != 2 {
		t.Fatalf("Crate = %+v, %v", cr, err)
	}
	if cr.Versions[0].RustVersion != "" || cr.Versions[1].RustVersion != "1.70" {
		t.Errorf("rust_version = %q, %q", cr.Versions[0].RustVersion, cr.Versions[1].RustVersion)
	}
}
