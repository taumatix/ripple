package pypi_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/taumatix/ripple/internal/pypi"
)

// testdata/pep440.json is generated from the `packaging` library by
// scripts/gen-pep440-vectors.py: every version with its dense rank in packaging's order
// (equal versions share one), the strings packaging rejects, and, for the final and
// post releases, whether packaging's SpecifierSet contains them. The tests read the
// committed table, so CI needs no Python.
type pep440Table struct {
	Packaging string `json:"packaging"`
	Versions  []struct {
		In   string `json:"in"`
		Norm string `json:"norm"`
		Rank int    `json:"rank"`
	} `json:"versions"`
	Invalid    []string `json:"invalid"`
	Specifiers []struct {
		Spec    string `json:"spec"`
		Version string `json:"version"`
		OK      bool   `json:"ok"`
	} `json:"specifiers"`
}

func loadPEP440(t *testing.T) pep440Table {
	t.Helper()
	raw, err := os.ReadFile("testdata/pep440.json")
	if err != nil {
		t.Fatal(err)
	}
	var tab pep440Table
	if err := json.Unmarshal(raw, &tab); err != nil {
		t.Fatal(err)
	}
	if len(tab.Versions) < 300 || len(tab.Specifiers) < 500 {
		t.Fatalf("table from packaging %s is too small: %d versions, %d specifier cases", tab.Packaging, len(tab.Versions), len(tab.Specifiers))
	}
	return tab
}

func TestOrderingAgreesWithPackaging(t *testing.T) {
	tab := loadPEP440(t)
	parsed := make([]pypi.Version, len(tab.Versions))
	for i, v := range tab.Versions {
		p, ok := pypi.Parse(v.In)
		if !ok {
			t.Fatalf("packaging %s accepts %q, Parse does not", tab.Packaging, v.In)
		}
		parsed[i] = p
	}
	bad := 0
	for i, a := range tab.Versions {
		for j, b := range tab.Versions {
			want := 0
			switch {
			case a.Rank < b.Rank:
				want = -1
			case a.Rank > b.Rank:
				want = 1
			}
			if got := pypi.Compare(parsed[i], parsed[j]); got != want {
				if bad++; bad <= 15 {
					t.Errorf("Compare(%q, %q) = %d, packaging %s says %d", a.In, b.In, got, tab.Packaging, want)
				}
			}
		}
	}
	if bad > 15 {
		t.Errorf("... %d disagreements in all", bad)
	}
}

func TestParseRejectsWhatPackagingRejects(t *testing.T) {
	tab := loadPEP440(t)
	for _, in := range tab.Invalid {
		if _, ok := pypi.Parse(in); ok {
			t.Errorf("Parse(%q) accepted a string packaging %s rejects", in, tab.Packaging)
		}
	}
}

func TestSatisfiesAgreesWithPackaging(t *testing.T) {
	tab := loadPEP440(t)
	bad := 0
	for _, c := range tab.Specifiers {
		v, ok := pypi.Parse(c.Version)
		if !ok {
			t.Fatalf("unparsable %q", c.Version)
		}
		got, known := pypi.Satisfies(c.Spec, v)
		if !known || got != c.OK {
			if bad++; bad <= 15 {
				t.Errorf("Satisfies(%q, %q) = %v (known %v), packaging %s says %v", c.Spec, c.Version, got, known, tab.Packaging, c.OK)
			}
		}
	}
	if bad > 15 {
		t.Errorf("... %d disagreements in all", bad)
	}
}
