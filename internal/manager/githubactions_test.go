package manager

import (
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const sha40 = "8e5e7e5ab8b370d6c329ec480221332ada57f0ab"

func TestGitHubActionsMatches(t *testing.T) {
	m := GitHubActions{Dir: "/"}
	for p, want := range map[string]bool{
		".github/workflows/ci.yml":          true,
		".github/workflows/ci.yaml":         true,
		".github/workflows/sub/ci.yml":      false,
		".github/workflows/README.md":       false,
		"action.yml":                        true,
		".github/actions/setup/action.yaml": true,
		"go.mod":                            false,
		"docs/ci.yml":                       false,
	} {
		if got := m.Matches(p); got != want {
			t.Errorf("Matches(%q) = %v, want %v", p, got, want)
		}
	}
	if (GitHubActions{Dir: "/sub"}).Matches(".github/workflows/ci.yml") {
		t.Error("a directory-scoped manager matched a file outside it")
	}
}

const workflow = `name: ci
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@` + sha40 + ` # v5.0.1
        with:
          go-version: stable
      - uses: "actions/cache@v4.1.2"   # keep
      - uses: some/action@main
      - uses: ./local
      - uses: docker://alpine:3.19
      - uses: actions/upload-artifact/sub/dir@v3
  b:
    uses: org/repo/.github/workflows/x.yml@v2
`

func TestGitHubActionsExtract(t *testing.T) {
	deps, err := GitHubActions{Dir: "/"}.Extract(".github/workflows/ci.yml", []byte(workflow))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceGitHubTags || d.Manager != "github-actions" || d.File != ".github/workflows/ci.yml" {
			t.Errorf("unexpected dependency fields: %+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "actions/checkout@v4 actions/setup-go@v5.0.1 actions/cache@v4.1.2 actions/upload-artifact@v3 org/repo@v2"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v\nwant %s", got, want)
	}
}

func TestGitHubActionsExtractIgnoresPinnedWithoutComment(t *testing.T) {
	deps, _ := GitHubActions{}.Extract("action.yml", []byte("runs:\n  steps:\n    - uses: a/b@"+sha40+"\n    - uses: a/c@"+sha40+" # not a version\n"))
	if len(deps) != 0 {
		t.Errorf("got %+v, want none: nothing says which tag the SHA is", deps)
	}
}

func TestGitHubActionsApplyTag(t *testing.T) {
	m := GitHubActions{}
	dep := model.Dependency{Package: "actions/checkout", Version: "v4"}
	out, err := m.Apply([]byte(workflow), dep, "", "v5")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(workflow, "actions/checkout@v4", "actions/checkout@v5", 1)
	if string(out) != want {
		t.Errorf("tag rewrite changed more than the ref:\n%s", out)
	}
	// quoted, with trailing comment, subdirectory form
	out, _ = m.Apply([]byte(workflow), model.Dependency{Package: "actions/cache", Version: "v4.1.2"}, "", "v4.2.0")
	if !strings.Contains(string(out), `- uses: "actions/cache@v4.2.0"   # keep`) {
		t.Errorf("quote or comment not preserved:\n%s", out)
	}
	out, _ = m.Apply([]byte(workflow), model.Dependency{Package: "actions/upload-artifact", Version: "v3"}, "", "v4")
	if !strings.Contains(string(out), "actions/upload-artifact/sub/dir@v4\n") {
		t.Errorf("subdirectory form not rewritten:\n%s", out)
	}
	out, _ = m.Apply([]byte(workflow), model.Dependency{Package: "org/repo", Version: "v2"}, "", "v3")
	if !strings.Contains(string(out), "org/repo/.github/workflows/x.yml@v3") {
		t.Errorf("reusable workflow not rewritten:\n%s", out)
	}
}

func TestGitHubActionsApplyPinned(t *testing.T) {
	newSHA := strings.Repeat("a", 40)
	out, err := GitHubActions{}.Apply([]byte(workflow), model.Dependency{Package: "actions/setup-go", Version: "v5.0.1"}, "", "v5.1.0@"+newSHA)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(workflow, "@"+sha40+" # v5.0.1", "@"+newSHA+" # v5.1.0", 1)
	if string(out) != want {
		t.Errorf("pinned rewrite wrong:\n%s", out)
	}
}

func TestGitHubActionsApplyMissing(t *testing.T) {
	_, err := GitHubActions{}.Apply([]byte(workflow), model.Dependency{Package: "x/y", Version: "v1"}, "", "v2")
	if err == nil {
		t.Error("want an error when the reference is not in the file")
	}
	if _, err := (GitHubActions{}).Apply([]byte(workflow), model.Dependency{Package: "actions/checkout", Version: "v4"}, "other/name", "v5"); err == nil {
		t.Error("a rename must be refused")
	}
}
