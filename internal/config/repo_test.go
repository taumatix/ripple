package config

import (
	"errors"
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
		"bad ecosystem":      "updates:\n  - ecosystem: npm\n",
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
	if !strings.Contains(err.Error(), "line 3:") || !strings.Contains(err.Error(), ".github/ripple.yml") {
		t.Fatal(err)
	}
}
