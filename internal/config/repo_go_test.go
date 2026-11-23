package config

import (
	"strings"
	"testing"
)

func TestCgoAndPrivateModulesKeys(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    cgo: true\n    private-modules: [\"*.corp.example.com\", example.com/priv/*]\n"))
	if err != nil {
		t.Fatal(err)
	}
	u := c.Updates[0]
	if !u.CGO || len(u.PrivateModules) != 2 || u.PrivateModules[0] != "*.corp.example.com" {
		t.Fatalf("%+v", u)
	}
	// The default stays what it was: cgo off, nothing private.
	d, err := Parse(nil)
	if err != nil || d.Updates[0].CGO || d.Updates[0].PrivateModules != nil {
		t.Fatalf("default: %+v %v", d, err)
	}
}

func TestPrivateModulesAndCgoRejected(t *testing.T) {
	const gm = "updates:\n  - ecosystem: gomod\n    private-modules: "
	for name, in := range map[string]string{
		"everything":      gm + "[\"*\"]\n",
		"only wildcards":  gm + "[\"*.*\"]\n",
		"no dot host":     gm + "[corp]\n",
		"empty":           gm + "[\"\"]\n",
		"comma":           gm + "[\"a.com,b.com\"]\n",
		"space":           gm + "[\"a.com b\"]\n",
		"leading slash":   gm + "[\"/a.com\"]\n",
		"too many":        gm + "[" + strings.Repeat("a.com/x, ", 21) + "a.com/y]\n",
		"scalar":          gm + "a.com\n",
		"regex ecosystem": "updates:\n  - ecosystem: regex\n    private-modules: [a.com]\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), "private-modules") {
			t.Errorf("%s: want a private-modules problem, got %v", name, err)
		}
	}
	if _, err := Parse([]byte("updates:\n  - ecosystem: regex\n    cgo: true\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n")); err == nil || !strings.Contains(err.Error(), "cgo") {
		t.Errorf("cgo with regex: %v", err)
	}
	if _, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    cgo: maybe\n")); err == nil {
		t.Error("cgo: maybe accepted")
	}
}

func TestSecurityPseudoVersionsIsGomodOnlyAndOffByDefault(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    security-pseudo-versions: true\n"))
	if err != nil || !c.Updates[0].SecurityPseudoVersions {
		t.Fatalf("err=%v %+v", err, c)
	}
	c, err = Parse([]byte("updates:\n  - ecosystem: gomod\n"))
	if err != nil || c.Updates[0].SecurityPseudoVersions {
		t.Fatalf("default: err=%v", err)
	}
	if _, err := Parse([]byte("updates:\n  - ecosystem: regex\n    security-pseudo-versions: true\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n")); err == nil || !strings.Contains(err.Error(), "security-pseudo-versions") {
		t.Errorf("with regex: %v", err)
	}
}

func TestSecurityFromPseudoVersionsIsGomodOnlyAndOffByDefault(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    security-from-pseudo-versions: true\n"))
	if err != nil || !c.Updates[0].SecurityFromPseudoVersions {
		t.Fatalf("err=%v %+v", err, c)
	}
	c, err = Parse([]byte("updates:\n  - ecosystem: gomod\n"))
	if err != nil || c.Updates[0].SecurityFromPseudoVersions {
		t.Fatalf("default: err=%v", err)
	}
	if _, err := Parse([]byte("updates:\n  - ecosystem: regex\n    security-from-pseudo-versions: true\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n")); err == nil || !strings.Contains(err.Error(), "security-from-pseudo-versions") {
		t.Errorf("with regex: %v", err)
	}
}

func TestSecurityBypassIsGomodOnlyValidatedAndOffByDefault(t *testing.T) {
	c, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    security-bypass: [open-pull-requests-limit, schedule]\n"))
	if err != nil || !c.Updates[0].Bypasses(BypassLimit) || !c.Updates[0].Bypasses(BypassSchedule) {
		t.Fatalf("err=%v %+v", err, c)
	}
	c, err = Parse([]byte("updates:\n  - ecosystem: gomod\n"))
	if err != nil || c.Updates[0].Bypasses(BypassLimit) || c.Updates[0].Bypasses(BypassSchedule) {
		t.Fatalf("default: err=%v", err)
	}
	if _, err := Parse([]byte("updates:\n  - ecosystem: gomod\n    security-bypass: [cooldown]\n")); err == nil || !strings.Contains(err.Error(), "security-bypass") {
		t.Errorf("unknown value: %v", err)
	}
	for _, eco := range []string{"docker", "github-actions"} {
		if _, err := Parse([]byte("updates:\n  - ecosystem: " + eco + "\n    security-bypass: [schedule]\n")); err == nil || !strings.Contains(err.Error(), "security-bypass") {
			t.Errorf("with %s: %v", eco, err)
		}
	}
	if _, err := Parse([]byte("updates:\n  - ecosystem: regex\n    security-bypass: [schedule]\n    files: [Makefile]\n    patterns: ['(?P<name>a) (?P<version>b)']\n")); err == nil || !strings.Contains(err.Error(), "security-bypass") {
		t.Errorf("with regex: %v", err)
	}
}
