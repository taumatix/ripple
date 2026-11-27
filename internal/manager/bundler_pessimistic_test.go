package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

// The meaning of the two-segment pessimistic constraint is the Gemfile man page's, section
// "Gems > Version" (docs/vendor/rubygems/gemfile.md): "~> 2.1 is identical to >= 2.1 and < 3.0".

const gemfileTwoSegment = `source "https://rubygems.org"
gem "two", "~> 1.2"
gem 'quoted', '~>  3.4'   # spaces after the operator kept
gem "floor", ">= 1.0"
gem "above", "> 1.0"
gem "one", "~> 1"
gem "dup", "~> 1.2", group: :test
gem "third", "~> 1.2.3"
`

func TestBundlerExtractTwoSegment(t *testing.T) {
	deps, err := Bundler{}.Extract("Gemfile", []byte(gemfileTwoSegment))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	// ">= 1.0" and "> 1.0" already allow every later release: neither is read. "~> 1" is read: the
	// real `bundle lock` resolves it inside 1.x (TestRealtoolBundlerOneSegment, ROADMAP 253).
	want := "two@~> 1.2 quoted@~>  3.4 one@~> 1 dup@~> 1.2 third@~> 1.2.3"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestBundlerApplyTwoSegment(t *testing.T) {
	dep := model.Dependency{File: "Gemfile", Package: "two", Version: "~> 1.2"}
	out, err := Bundler{}.Apply([]byte(gemfileTwoSegment), dep, "", "v2.1.3")
	if err != nil {
		t.Fatal(err)
	}
	// "~> 2.1" is ">= 2.1, < 3.0" and so allows 2.1.3; the patch digit is not written into a two-segment constraint.
	if want := strings.Replace(gemfileTwoSegment, `"~> 1.2"`, `"~> 2.1"`, 1); string(out) != want {
		t.Errorf("only the two digits move, got:\n%s", out)
	}

	dep = model.Dependency{File: "Gemfile", Package: "quoted", Version: "~>  3.4"}
	out, err = Bundler{}.Apply([]byte(gemfileTwoSegment), dep, "", "4.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(gemfileTwoSegment, `'~>  3.4'`, `'~>  4.0'`, 1); string(out) != want {
		t.Errorf("operator spacing kept, got:\n%s", out)
	}
}

func TestBundlerApplyTwoSegmentOnlyAMajor(t *testing.T) {
	dep := model.Dependency{File: "Gemfile", Package: "two", Version: "~> 1.2"}
	for _, nv := range []string{"1.2.0", "1.2.9", "1.9.0", "1.1.0", "0.9.0"} {
		// "~> 1.2" is ">= 1.2, < 2.0": every release of the 1.x line from 1.2 is already allowed
		// (nothing to write), and an older one is no update.
		if _, err := (Bundler{}).Apply([]byte(gemfileTwoSegment), dep, "", nv); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: want ErrUnsupported, got %v", nv, err)
		}
	}
}

func TestBundlerApplyTwoSegmentEveryOccurrence(t *testing.T) {
	src := "gem \"two\", \"~> 1.2\"\ngroup :t do\n  gem \"two\", \"~> 1.2\"\nend\n"
	dep := model.Dependency{File: "Gemfile", Package: "two", Version: "~> 1.2"}
	out, err := Bundler{}.Apply([]byte(src), dep, "", "3.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(src, "~> 1.2", "~> 3.0"); string(out) != want {
		t.Errorf("got %q", out)
	}
}

// "~> 1" is ">= 1, < 2" as real bundler resolves it (the same as "~> 1.0"): a later major is the only
// update, and the file takes that release's major alone, which that release satisfies.
func TestBundlerApplyOneSegment(t *testing.T) {
	dep := model.Dependency{File: "Gemfile", Package: "one", Version: "~> 1"}
	out, err := Bundler{}.Apply([]byte(gemfileTwoSegment), dep, "", "v2.1.3")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(gemfileTwoSegment, `"~> 1"`, `"~> 2"`, 1); string(out) != want {
		t.Errorf("only the major digit moves, got:\n%s", out)
	}
	for _, nv := range []string{"1.0.0", "1.9.9", "0.9.0"} {
		if _, err := (Bundler{}).Apply([]byte(gemfileTwoSegment), dep, "", nv); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: \"~> 1\" already allows or outdates it, want ErrUnsupported, got %v", nv, err)
		}
	}
}

func TestBundlerApplyThreeSegmentUnchanged(t *testing.T) {
	dep := model.Dependency{File: "Gemfile", Package: "third", Version: "~> 1.2.3"}
	out, err := Bundler{}.Apply([]byte(gemfileTwoSegment), dep, "", "1.4.7")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(gemfileTwoSegment, `"~> 1.2.3"`, `"~> 1.4.7"`, 1); string(out) != want {
		t.Errorf("three segments are written whole as before, got:\n%s", out)
	}
}

func TestBundlerSpecTwoSegment(t *testing.T) {
	s, ok := ParseBundlerSpec("~> 1.2")
	if !ok || s.Version != "1.2" || s.Prefix != "~> " || !s.TwoSegment() {
		t.Errorf("%+v %v", s, ok)
	}
	if s, ok := ParseBundlerSpec("~> 1"); !ok || s.Version != "1" || !s.Partial() || s.TwoSegment() {
		t.Errorf("one segment is a partial, not two-segment: %+v %v", s, ok)
	}
	for _, in := range []string{"1", "= 1", ">= 1", "~> 1.2.3.4", "~> 1.2.beta", "~> a.b"} {
		if _, ok := ParseBundlerSpec(in); ok {
			t.Errorf("%q must not parse", in)
		}
	}
	if s, ok := ParseBundlerSpec("~> 1.2.3"); !ok || s.TwoSegment() {
		t.Errorf("three segments are not two-segment: %+v", s)
	}
}
