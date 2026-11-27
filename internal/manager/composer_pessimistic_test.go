package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

// The meaning of the two-segment operators is Composer's versions article, sections "Tilde Version
// Range" and "Caret Version Range" (docs/vendor/packagist/composer-versions.md):
//
//	~1.2   >=1.2 <2.0.0          ^1.2.3  >=1.2.3 <2.0.0
//	^0.3   >=0.3.0 <0.4.0        ~1     the same as ~1.0

const composerTwoSegment = `{
    "require": {
        "caret/a": "^1.2",
        "tilde/b": "~1.2",
        "zero/c": "^0.3",
        "zero/tilde": "~0.3",
        "vee/d": "^v2.4",
        "one/e": "~1",
        "one/f": "^1",
        "three/g": "^1.2.3",
        "range/h": "^1.0 || ^2.0",
        "star/i": "1.2.*",
        "four/j": "^1.2.3.4"
    }
}
`

func TestComposerExtractTwoSegment(t *testing.T) {
	deps, err := Composer{}.Extract("composer.json", []byte(composerTwoSegment))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	// "~0.3", "~1" and "^1" are read since composer 2 was asked (TestRealtoolComposerConstraintMeaning);
	// a range, a wildcard and four segments are not.
	want := "caret/a@^1.2 tilde/b@~1.2 zero/c@^0.3 zero/tilde@~0.3 vee/d@^v2.4 one/e@~1 one/f@^1 three/g@^1.2.3"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestComposerSpecMoves(t *testing.T) {
	for _, tc := range []struct {
		spec, release string
		moves         bool
	}{
		{"^1.2", "1.2.0", false}, // allowed
		{"^1.2", "1.9.9", false}, // allowed: ">=1.2 <2.0.0"
		{"^1.2", "1.1.0", false}, // older
		{"^1.2", "2.0.0", true},
		{"^1.2", "3.4.1", true},
		{"~1.2", "1.8.0", false},
		{"~1.2", "2.0.0", true},
		{"^0.3", "0.3.9", false}, // "^0.3" is ">=0.3.0 <0.4.0"
		{"^0.3", "0.4.0", true},
		{"^0.3", "0.2.0", false},
		{"^0.3", "1.0.0", true},
		{"^v2.4", "2.9.0", false},
		{"^v2.4", "3.0.0", true},
		// composer: "~0.3" locks mockery 0.9.11, so it is >=0.3 <1.0
		{"~0.3", "0.9.0", false},
		{"~0.3", "0.4.0", false},
		{"~0.3", "1.0.0", true},
		// composer: "^1" and "~1" lock monolog 1.27.1, "^0" locks mockery 0.9.11
		{"^1", "1.9.0", false},
		{"~1", "1.9.0", false},
		{"^1", "2.0.0", true},
		{"~1", "2.0.0", true},
		{"^0", "0.9.0", false},
		{"^0", "1.0.0", true},
		{"^1", "0.9.0", false},
	} {
		s, ok := ParseComposerSpec(tc.spec)
		if !ok || !s.Partial() {
			t.Fatalf("%s: %+v %v", tc.spec, s, ok)
		}
		if got := s.Moves(tc.release); got != tc.moves {
			t.Errorf("%s with %s: Moves = %v, want %v", tc.spec, tc.release, got, tc.moves)
		}
	}
}

func TestComposerApplyTwoSegment(t *testing.T) {
	for _, tc := range []struct{ pkg, spec, release, from, to string }{
		{"caret/a", "^1.2", "2.1.3", `"^1.2"`, `"^2.1"`},
		{"tilde/b", "~1.2", "3.0.0", `"~1.2"`, `"~3.0"`},
		{"zero/c", "^0.3", "0.4.2", `"^0.3"`, `"^0.4"`}, // "^0.4" is ">=0.4.0 <0.5.0" and allows 0.4.2
		{"zero/c", "^0.3", "1.0.1", `"^0.3"`, `"^1.0"`},
		{"vee/d", "^v2.4", "v3.1.0", `"^v2.4"`, `"^v3.1"`},
		{"zero/tilde", "~0.3", "1.2.0", `"~0.3"`, `"~1.2"`},
		{"one/e", "~1", "3.4.5", `"~1"`, `"~3"`},
		{"one/f", "^1", "2.0.1", `"^1"`, `"^2"`},
	} {
		dep := model.Dependency{File: "composer.json", Package: tc.pkg, Version: tc.spec}
		out, err := Composer{}.Apply([]byte(composerTwoSegment), dep, "", tc.release)
		if err != nil {
			t.Fatalf("%s: %v", tc.pkg, err)
		}
		if want := strings.Replace(composerTwoSegment, tc.from, tc.to, 1); string(out) != want {
			t.Errorf("%s -> %s: only the digits move, got:\n%s", tc.pkg, tc.release, out)
		}
	}
}

func TestComposerApplyTwoSegmentRefusesWhatIsAllowed(t *testing.T) {
	for _, tc := range []struct{ pkg, spec, release string }{
		{"caret/a", "^1.2", "1.9.0"},
		{"caret/a", "^1.2", "1.1.0"},
		{"tilde/b", "~1.2", "1.2.7"},
		{"zero/c", "^0.3", "0.3.8"},
		{"zero/tilde", "~0.3", "0.9.1"},
		{"one/e", "~1", "1.9.9"},
		{"one/f", "^1", "1.0.0"},
		{"one/f", "^1", "0.9.0"},
	} {
		dep := model.Dependency{File: "composer.json", Package: tc.pkg, Version: tc.spec}
		if _, err := (Composer{}).Apply([]byte(composerTwoSegment), dep, "", tc.release); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s %s with %s: want ErrUnsupported, got %v", tc.pkg, tc.spec, tc.release, err)
		}
	}
}

func TestComposerApplyThreeSegmentUnchanged(t *testing.T) {
	dep := model.Dependency{File: "composer.json", Package: "three/g", Version: "^1.2.3"}
	out, err := Composer{}.Apply([]byte(composerTwoSegment), dep, "", "1.4.7")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(composerTwoSegment, `"^1.2.3"`, `"^1.4.7"`, 1); string(out) != want {
		t.Errorf("three segments are written whole as before, got:\n%s", out)
	}
}
