package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const gemfile = `source "https://rubygems.org"
ruby "3.3.0"

gem "rails", "7.1.2"
gem 'nokogiri', '~> 1.15.4', require: false   # keeps its operator
gem "pg", "= 1.5.4", group: :production
gem "two", "~> 1.2"
gem "range", ">= 1.0", "< 2.0"
gem "range2", "1.0.0", "< 2.0"
gem "bare"
gem "pre", "1.0.0.rc1"
gem "four", "1.2.3.4"
gem "fromgit", "1.0.0", git: "https://example.com/x.git"
gem "fromgit2", "1.0.0", :git => "https://example.com/x.git"
gem "cont", "1.0.0",
    path: "../x"
gem "split", "1.0.0",
group: :x
group :test do
  gem "rspec", "3.12.0"
end
git "https://example.com/y.git" do
  gem "inblock", "1.0.0"
end
gem "after", "2.0.0"
`

func TestBundlerExtract(t *testing.T) {
	deps, err := Bundler{}.Extract("Gemfile", []byte(gemfile))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceRubygems || d.Manager != "bundler" {
			t.Errorf("%+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "rails@7.1.2 nokogiri@~> 1.15.4 pg@= 1.5.4 rspec@3.12.0 after@2.0.0"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestBundlerOtherSourceReadsNothing(t *testing.T) {
	for _, src := range []string{"https://gems.example.com", "http://rubygems.org"} {
		deps, _ := Bundler{}.Extract("Gemfile", []byte("source \""+src+"\"\ngem \"a\", \"1.0.0\"\n"))
		if len(deps) != 0 {
			t.Errorf("%s: %+v", src, deps)
		}
	}
	if deps, _ := (Bundler{}).Extract("Gemfile", []byte("source 'https://rubygems.org/'\ngem 'a', '1.0.0'\n")); len(deps) != 1 {
		t.Errorf("a trailing slash is still rubygems.org: %+v", deps)
	}
}

func TestBundlerApply(t *testing.T) {
	dep := model.Dependency{File: "Gemfile", Package: "nokogiri", Version: "~> 1.15.4"}
	out, err := Bundler{}.Apply([]byte(gemfile), dep, "", "v1.16.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(gemfile, "~> 1.15.4", "~> 1.16.0", 1); string(out) != want {
		t.Errorf("only the digits move:\n%s", out)
	}
	if _, err := (Bundler{}).Apply([]byte(gemfile), model.Dependency{File: "Gemfile", Package: "rails", Version: "7.1.1"}, "", "7.2.0"); !errors.Is(err, ErrStale) {
		t.Errorf("a changed version is stale, got %v", err)
	}
	if _, err := (Bundler{}).Apply([]byte(gemfile), model.Dependency{Package: "rails", Version: "7.1.2"}, "other", "7.2.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("rename: %v", err)
	}
	if _, err := (Bundler{}).Apply([]byte(gemfile), model.Dependency{Package: "rails", Version: "7.1.2"}, "", "7.2.0.rc1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("prerelease target: %v", err)
	}
}

func TestBundlerMatches(t *testing.T) {
	b := Bundler{Dir: "/app"}
	for p, want := range map[string]bool{"app/Gemfile": true, "app/sub/Gemfile": true, "Gemfile": false, "app/Gemfile.lock": false, "app/x.gemspec": false} {
		if b.Matches(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
