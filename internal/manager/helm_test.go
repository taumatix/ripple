package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const helmFixture = `apiVersion: v2
name: app
version: 1.0.0
dependencies:
  - name: pg
    version: 12.1.0
    repository: https://charts.example.com/
  - {name: flow, version: 1.0.0, repository: https://charts.example.com}
  - name: quoted
    repository: 'https://charts.example.com'
    version: "^v2.0.1" # keep
    condition: quoted.enabled
    tags:
      - db
  - name: pg
    version: 12.1.0
    repository: https://charts.example.com
  - name: range
    version: ">=1.0.0"
    repository: https://charts.example.com
  - name: nover
    repository: https://charts.example.com
  - name: local
    version: 1.0.0
    repository: file://../x
maintainers:
  - name: pg
    version: 9.9.9
    repository: https://other.example.com
`

func TestHelmExtract(t *testing.T) {
	deps, err := Helm{}.Extract("Chart.yaml", []byte(helmFixture))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceHelm || d.Manager != "helm" {
			t.Errorf("%+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "https://charts.example.com/pg@12.1.0 https://charts.example.com/quoted@^v2.0.1"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v\nwant %s (the flow item, range, no version, file:// and a later top-level key are not read; the duplicate pg is one dependency)", got, want)
	}
}

func TestHelmApplyEditsOnlyTheVersionBytes(t *testing.T) {
	pg := model.Dependency{File: "Chart.yaml", Package: "https://charts.example.com/pg", Version: "12.1.0"}
	out, err := Helm{}.Apply([]byte(helmFixture), pg, "", "12.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(helmFixture, "version: 12.1.0", "version: 12.4.0"); string(out) != want {
		t.Errorf("both entries naming the pair move, nothing else:\n%s", out)
	}
	q := model.Dependency{File: "Chart.yaml", Package: "https://charts.example.com/quoted", Version: "^v2.0.1"}
	out, err = Helm{}.Apply([]byte(helmFixture), q, "", "2.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(helmFixture, `"^v2.0.1" # keep`, `"^v2.3.0" # keep`, 1); string(out) != want {
		t.Errorf("operator, v prefix, quotes and comment kept:\n%s", out)
	}
}

func TestHelmApplyRefusals(t *testing.T) {
	d := model.Dependency{File: "Chart.yaml", Package: "https://charts.example.com/pg", Version: "12.0.0"}
	if _, err := (Helm{}).Apply([]byte(helmFixture), d, "", "12.4.0"); !errors.Is(err, ErrStale) {
		t.Errorf("a version no longer in the file is stale, got %v", err)
	}
	d.Version = "12.1.0"
	if _, err := (Helm{}).Apply([]byte(helmFixture), d, "https://charts.example.com/other", "12.4.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a rename is unsupported, got %v", err)
	}
	if _, err := (Helm{}).Apply([]byte(helmFixture), d, "", "13.0.0-rc.1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a prerelease target is refused, got %v", err)
	}
}

func TestHelmMatches(t *testing.T) {
	h := Helm{Dir: "/charts"}
	for p, want := range map[string]bool{"charts/a/Chart.yaml": true, "charts/a/requirements.yaml": true, "Chart.yaml": false, "charts/a/values.yaml": false} {
		if h.Matches(p) != want {
			t.Errorf("Matches(%q) != %v", p, want)
		}
	}
}

func TestHelmPackageSplit(t *testing.T) {
	r, c, ok := HelmSplit(HelmPackage("https://h.example.com/a/b", "chart"))
	if !ok || r != "https://h.example.com/a/b" || c != "chart" {
		t.Errorf("%q %q %v", r, c, ok)
	}
	if _, _, ok := HelmSplit("nginx"); ok {
		t.Error("a bare name is no helm package")
	}
}
