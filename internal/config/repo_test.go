package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"3d": 72 * time.Hour, "12h": 12 * time.Hour, "90m": 90 * time.Minute, "1d12h": 36 * time.Hour, "0": 0} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "3", "3 d", "-1d", "1w", "400d", "d"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestEmptyFilesGetDefaults(t *testing.T) {
	for _, in := range []string{"", "\n", "# only a comment\n", "{}", "---\n"} {
		c, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if len(c.Updates) != 1 || c.Updates[0].Ecosystem != "gomod" || c.Updates[0].Directory != "/" {
			t.Fatalf("%q: %+v", in, c)
		}
	}
}

func TestExampleAndDefaultFileAreValid(t *testing.T) {
	c, err := Parse([]byte(Example))
	if err != nil {
		t.Fatal(err)
	}
	u := c.Updates[0]
	if c.OpenPRLimit != 3 || u.Cooldown.Duration != 7*24*time.Hour || u.Automerge != "patch" || len(u.Groups) != 1 || u.Groups[0].Name != "aws" ||
		len(u.Ignore) != 2 || u.Schedule.Timezone != "Europe/Paris" || u.Ignore[1].UpdateTypes[0] != "major" {
		t.Fatalf("%+v", c)
	}
	if _, err := Parse(DefaultFile()); err != nil {
		t.Fatal(err)
	}
}

func problems(t *testing.T, in string) []Problem {
	t.Helper()
	_, err := Parse([]byte(in))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	return ve.Problems
}

func TestUnknownKeyNamesKeyAndLine(t *testing.T) {
	ps := problems(t, "version: 1\nupdates:\n  - ecosystem: gomod\n    cooldwn: 3d\n    automerge: patch\npackageRules: []\n")
	if len(ps) != 2 {
		t.Fatalf("%+v", ps)
	}
	if ps[0].Line != 4 || !strings.Contains(ps[0].Msg, `"cooldwn"`) || !strings.Contains(ps[0].Msg, `did you mean "cooldown"`) {
		t.Errorf("%+v", ps[0])
	}
	if ps[1].Line != 6 || !strings.Contains(ps[1].Msg, `"packageRules"`) {
		t.Errorf("%+v", ps[1])
	}
}

func TestValidationRejects(t *testing.T) {
	cases := map[string]string{
		"missing ecosystem":  "updates:\n  - directory: /\n",
		"bad ecosystem":      "updates:\n  - ecosystem: maven\n",
		"bad automerge":      "updates:\n  - ecosystem: gomod\n    automerge: always\n",
		"bad cooldown":       "updates:\n  - ecosystem: gomod\n    cooldown: soon\n",
		"traversal":          "updates:\n  - ecosystem: gomod\n    directory: /../etc\n",
		"relative directory": "updates:\n  - ecosystem: gomod\n    directory: svc\n",
		"bad day":            "updates:\n  - ecosystem: gomod\n    schedule:\n      days: [funday]\n",
		"half window":        "updates:\n  - ecosystem: gomod\n    schedule:\n      from: \"22:00\"\n",
		"bad time":           "updates:\n  - ecosystem: gomod\n    schedule:\n      from: \"25:00\"\n      to: \"26:00\"\n",
		"bad tz":             "updates:\n  - ecosystem: gomod\n    schedule:\n      timezone: Mars/Base\n",
		"ignore no dep":      "updates:\n  - ecosystem: gomod\n    ignore:\n      - update-types: [major]\n",
		"bad type":           "updates:\n  - ecosystem: gomod\n    ignore:\n      - dependency: a\n        update-types: [huge]\n",
		"group no patterns":  "updates:\n  - ecosystem: gomod\n    groups:\n      g: {}\n",
		"empty updates":      "updates: []\n",
		"bad version":        "version: 2\n",
		"bad limit":          "open-pull-requests-limit: 0\n",
		"regex w/o files":    "updates:\n  - ecosystem: regex\n    patterns: ['(?P<name>a) (?P<version>b)']\n",
		"regex no version":   "updates:\n  - ecosystem: regex\n    files: [Makefile]\n    patterns: ['(?P<name>a)']\n",
		"regex bad re":       "updates:\n  - ecosystem: regex\n    files: [Makefile]\n    patterns: ['(a']\n",
		"regex lookahead":    "updates:\n  - ecosystem: regex\n    files: [Makefile]\n    patterns: ['(?=a)(?P<name>a) (?P<version>b)']\n",
		"gomod with files":   "updates:\n  - ecosystem: gomod\n    files: [x]\n",
		"not a mapping":      "- a\n- b\n",
		"yaml syntax":        "updates: [\n",
		"wrong type":         "updates:\n  - ecosystem: gomod\n    ignore: yes\n",
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRegexUpdateAccepted(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: regex\n    files: ['Makefile', 'tools/*.mk']\n    patterns: ['# go:(?P<name>\\S+) (?P<version>v\\S+)']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ManagerName(0) != "regex#0" {
		t.Fatal(c.ManagerName(0))
	}
	u, ok := c.UpdateFor("regex#0", "Makefile")
	if !ok || u != &c.Updates[0] {
		t.Fatal("UpdateFor regex")
	}
}

func TestUpdateForDeepestDirectory(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    directory: /\n  - ecosystem: gomod\n    directory: /svc\n    automerge: patch\n"))
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := c.UpdateFor("gomod", "svc/api/go.mod"); u != &c.Updates[1] {
		t.Fatal("want /svc")
	}
	if u, _ := c.UpdateFor("gomod", "go.mod"); u != &c.Updates[0] {
		t.Fatal("want /")
	}
	if u, _ := c.UpdateFor("gomod", "svcx/go.mod"); u != &c.Updates[0] {
		t.Fatal("svcx is not below /svc")
	}
}

func TestErrorFormat(t *testing.T) {
	_, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    nope: 1\n"))
	if !strings.Contains(err.Error(), "line 3:") || !strings.Contains(err.Error(), ".ripple.yaml") {
		t.Fatal(err)
	}
}

func TestFileNames(t *testing.T) {
	if FileName != ".ripple.yaml" || LegacyFileName != ".github/ripple.yml" {
		t.Fatalf("FileName=%q LegacyFileName=%q", FileName, LegacyFileName)
	}
}

func readerOf(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if c, ok := files[name]; ok {
			return []byte(c), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		files                  map[string]string
		file, data             string
		legacy, shadowed, none bool
	}{
		{"none", nil, "", "", false, false, true},
		{"new only", map[string]string{".ripple.yaml": "N"}, ".ripple.yaml", "N", false, false, false},
		{"old only", map[string]string{".github/ripple.yml": "O"}, ".github/ripple.yml", "O", true, false, false},
		{"both: new wins", map[string]string{".ripple.yaml": "N", ".github/ripple.yml": "O"}, ".ripple.yaml", "N", false, true, false},
		{"empty new file still wins", map[string]string{".ripple.yaml": "", ".github/ripple.yml": "O"}, ".ripple.yaml", "", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Resolve(readerOf(tc.files))
			if err != nil {
				t.Fatal(err)
			}
			if s.Name != tc.file || string(s.Data) != tc.data || s.Legacy != tc.legacy || s.Shadowed != tc.shadowed || s.Exists() == tc.none {
				t.Fatalf("%+v", s)
			}
		})
	}
}

func TestWarningText(t *testing.T) {
	if got, want := LegacyWarning, "config file .github/ripple.yml is deprecated; move it to .ripple.yaml (the alias will not be removed before v0.3.0)"; got != want {
		t.Errorf("LegacyWarning = %q", got)
	}
	if got, want := ShadowedWarning, "both .ripple.yaml and .github/ripple.yml exist; using .ripple.yaml and ignoring .github/ripple.yml, delete it"; got != want {
		t.Errorf("ShadowedWarning = %q", got)
	}
}

func TestParseNamedReportsTheFile(t *testing.T) {
	_, err := ParseNamed(".github/ripple.yml", []byte("updates:\n  - ecosystem: gomod\n    nope: 1\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "invalid .github/ripple.yml:") {
		t.Fatal(err)
	}
	_, err = Parse([]byte("updates:\n  - ecosystem: gomod\n    nope: 1\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "invalid .ripple.yaml:") {
		t.Fatal(err)
	}
}

func TestGoToolchainKey(t *testing.T) {
	for _, v := range []string{"toolchain", "directive"} {
		c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    go-toolchain: " + v + "\n"))
		if err != nil || c.Updates[0].GoToolchain != v {
			t.Errorf("%s: got %+v, %v", v, c, err)
		}
	}
	if c, err := Parse(nil); err != nil || c.Updates[0].GoToolchain != "" {
		t.Errorf("off by default: %+v, %v", c, err)
	}
	for name, in := range map[string]string{
		"unknown value": "updates:\n  - ecosystem: gomod\n    go-toolchain: latest\n",
		"regex":         "updates:\n  - ecosystem: regex\n    go-toolchain: toolchain\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), "go-toolchain") {
			t.Errorf("%s: want a go-toolchain problem, got %v", name, err)
		}
	}
}

func TestGoModResidualKeys(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    directives: extended\n    tidy: minimal\n"))
	if err != nil || c.Updates[0].Directives != "extended" || c.Updates[0].Tidy != "minimal" {
		t.Fatalf("got %+v, %v", c, err)
	}
	if c, err := Parse(nil); err != nil || c.Updates[0].Directives != "" || c.Updates[0].Tidy != "" {
		t.Errorf("off by default: %+v, %v", c, err)
	}
	for key, in := range map[string]string{
		"directives":         "updates:\n  - ecosystem: gomod\n    directives: all\n",
		"tidy":               "updates:\n  - ecosystem: gomod\n    tidy: none\n",
		"directives is only": "updates:\n  - ecosystem: regex\n    directives: extended\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n",
		"tidy is only":       "updates:\n  - ecosystem: regex\n    tidy: minimal\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), strings.Fields(key)[0]) {
			t.Errorf("%s: want a problem naming the key, got %v", key, err)
		}
	}
}

func TestCloseSupersededDefaultsOnAndCanBeSwitchedOff(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{{"", true}, {"version: 1\n", true}, {"close-superseded-pull-requests: true\n", true}, {"close-superseded-pull-requests: false\n", false}} {
		r, err := Parse([]byte(c.file))
		if err != nil {
			t.Fatalf("%q: %v", c.file, err)
		}
		if got := r.ClosesSuperseded(); got != c.want {
			t.Errorf("%q: ClosesSuperseded = %v, want %v", c.file, got, c.want)
		}
	}
	if _, err := Parse([]byte("close-superseded-pull-requests: maybe\n")); err == nil {
		t.Error("a non-boolean value must be an error")
	}
}

func TestBackfillNewDependenciesDefaultsOn(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{{"", true}, {"version: 1\n", true}, {"backfill-new-dependencies: true\n", true}, {"backfill-new-dependencies: false\n", false}} {
		r, err := Parse([]byte(c.file))
		if err != nil {
			t.Fatalf("%q: %v", c.file, err)
		}
		if got := r.Backfills(); got != c.want {
			t.Errorf("%q: Backfills = %v, want %v", c.file, got, c.want)
		}
	}
	if _, err := Parse([]byte("backfill-new-dependencies: maybe\n")); err == nil {
		t.Error("a non-boolean value must be an error")
	}
}

func TestDependencyDashboardIsOptIn(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{{"", false}, {"version: 1\n", false}, {"dependency-dashboard: true\n", true}, {"dependency-dashboard: false\n", false}} {
		r, err := Parse([]byte(c.file))
		if err != nil {
			t.Fatalf("%q: %v", c.file, err)
		}
		if r.DependencyDashboard != c.want {
			t.Errorf("%q: DependencyDashboard = %v, want %v", c.file, r.DependencyDashboard, c.want)
		}
	}
	if _, err := Parse([]byte("dependency-dashboard: maybe\n")); err == nil {
		t.Error("a non-boolean value must be an error")
	}
}
