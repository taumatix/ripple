package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const composerJSON = `{
    "name": "acme/app",
    "require": {
        "php": "^8.1",
        "ext-json": "*",
        "monolog/monolog": "^2.9.0",
        "symfony/console": "v5.4.1",
        "guzzlehttp/guzzle": "~7.5.0",   
        "two/seg": "^1.2",
        "range/a": ">=1.0 <2.0",
        "range/b": "^1.0.0 || ^2.0.0",
        "dev/branch": "dev-main",
        "pre/release": "1.0.0-beta1",
        "star/x": "1.*",
        "esc/aped": "1.0.0",
        "obj/value": {"a": "1.0.0"},
        "exact/pkg": "3.4.5"
    },
    "require-dev": {
        "phpunit/phpunit": "9.5.0",
        "monolog/monolog": "^2.9.0"
    },
    "extra": {"require": {"not/read": "1.0.0"}},
    "suggest": {"nope/nope": "1.0.0"}
}
`

func TestComposerExtract(t *testing.T) {
	deps, err := Composer{}.Extract("composer.json", []byte(composerJSON))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourcePackagist || d.Manager != "composer" {
			t.Errorf("%+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "monolog/monolog@^2.9.0 symfony/console@v5.4.1 guzzlehttp/guzzle@~7.5.0 esc/aped@1.0.0 exact/pkg@3.4.5 phpunit/phpunit@9.5.0"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestComposerRepositoriesLeaveFileAlone(t *testing.T) {
	for _, repos := range []string{`[{"type":"composer","url":"https://repo.example.com"}]`, `{"packagist.org": false}`} {
		doc := `{"repositories": ` + repos + `, "require": {"a/b": "1.0.0"}}`
		if deps, _ := (Composer{}).Extract("composer.json", []byte(doc)); len(deps) != 0 {
			t.Errorf("%s: %+v", repos, deps)
		}
	}
	for _, doc := range []string{`{"repositories": [], "require": {"a/b": "1.0.0"}}`, `{"require": {"a/b": "1.0.0"}}`} {
		if deps, _ := (Composer{}).Extract("composer.json", []byte(doc)); len(deps) != 1 {
			t.Errorf("%s: %+v", doc, deps)
		}
	}
	if deps, _ := (Composer{}).Extract("composer.json", []byte(`not json`)); len(deps) != 0 {
		t.Errorf("%+v", deps)
	}
}

func TestComposerApply(t *testing.T) {
	dep := model.Dependency{File: "composer.json", Package: "symfony/console", Version: "v5.4.1"}
	out, err := Composer{}.Apply([]byte(composerJSON), dep, "", "v5.4.9")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(composerJSON, `"v5.4.1"`, `"v5.4.9"`, 1); string(out) != want {
		t.Errorf("only the digits move:\n%s", out)
	}
	dep = model.Dependency{File: "composer.json", Package: "monolog/monolog", Version: "^2.9.0"}
	out, err = Composer{}.Apply([]byte(composerJSON), dep, "", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(composerJSON, `"^2.9.0"`, `"^3.0.0"`); string(out) != want {
		t.Errorf("require and require-dev both move:\n%s", out)
	}
	if _, err := (Composer{}).Apply([]byte(composerJSON), model.Dependency{File: "composer.json", Package: "exact/pkg", Version: "3.4.4"}, "", "4.0.0"); !errors.Is(err, ErrStale) {
		t.Errorf("a changed version is stale, got %v", err)
	}
	if _, err := (Composer{}).Apply([]byte(composerJSON), model.Dependency{Package: "exact/pkg", Version: "3.4.5"}, "other/x", "4.0.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("rename: %v", err)
	}
	if _, err := (Composer{}).Apply([]byte(composerJSON), model.Dependency{Package: "exact/pkg", Version: "3.4.5"}, "", "4.0.0-rc1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("prerelease target: %v", err)
	}
}

func TestComposerMatches(t *testing.T) {
	c := Composer{Dir: "/app"}
	for p, want := range map[string]bool{"app/composer.json": true, "app/sub/composer.json": true, "composer.json": false, "app/composer.lock": false} {
		if c.Matches(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
