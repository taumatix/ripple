package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run([]string{"version"}, &out, &errw, func(string) string { return "" }); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "ripple ") || len(got) == len("ripple ") {
		t.Fatalf("output %q, want %q followed by a version", got, "ripple ")
	}
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run([]string{"nope"}, &out, &errw, func(string) string { return "" }); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errw.String(), "ripple version") {
		t.Fatalf("usage does not list the version command: %q", errw.String())
	}
}

func inDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

func validateCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(append([]string{"config", "validate"}, args...), &out, &errw, func(string) string { return "" })
	return code, out.String(), errw.String()
}

const okFile = "updates:\n  - ecosystem: gomod\n"

func TestValidateDefaultsToRootFile(t *testing.T) {
	inDir(t, map[string]string{".ripple.yaml": okFile})
	code, out, errw := validateCmd(t)
	if code != 0 || out != ".ripple.yaml ok: 1 update entry\n" || errw != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errw)
	}
}

func TestValidateFallsBackToDeprecatedAlias(t *testing.T) {
	inDir(t, map[string]string{".github/ripple.yml": okFile})
	code, out, errw := validateCmd(t)
	want := ".github/ripple.yml ok: 1 update entry\nwarning: config file .github/ripple.yml is deprecated; move it to .ripple.yaml (the alias will not be removed before v0.3.0)\n"
	if code != 0 || out != want || errw != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errw)
	}
}

func TestValidateBothFilesNewWins(t *testing.T) {
	inDir(t, map[string]string{".ripple.yaml": okFile, ".github/ripple.yml": "updates:\n  - ecosystem: gomod\n    bogus: 1\n"})
	code, out, errw := validateCmd(t)
	want := ".ripple.yaml ok: 1 update entry\nwarning: both .ripple.yaml and .github/ripple.yml exist; using .ripple.yaml and ignoring .github/ripple.yml, delete it\n"
	if code != 0 || out != want || errw != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errw)
	}
}

func TestValidateNoFile(t *testing.T) {
	inDir(t, nil)
	code, out, _ := validateCmd(t)
	if code != 0 || !strings.HasPrefix(out, ".ripple.yaml does not exist: the defaults apply") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestValidateExplicitFileIsUsedAsGiven(t *testing.T) {
	inDir(t, map[string]string{"x.yaml": okFile, ".ripple.yaml": "bogus: 1\n"})
	code, out, _ := validateCmd(t, "x.yaml")
	if code != 0 || out != "x.yaml ok: 1 update entry\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestEventsCommand(t *testing.T) {
	noenv := func(string) string { return "" }
	var out, errw bytes.Buffer
	if code := run([]string{"events", "list"}, &out, &errw, noenv); code != 0 || !strings.Contains(out.String(), "no dead events") {
		t.Fatalf("list: exit %d, out %q, stderr %q", code, out.String(), errw.String())
	}
	for _, args := range [][]string{{"events"}, {"events", "requeue"}, {"events", "list", "x"}, {"events", "frob"}, {"events", "purge", "nonsense"}} {
		errw.Reset()
		if code := run(args, &out, &errw, noenv); code != 2 {
			t.Errorf("%v: exit %d, want 2 (%q)", args, code, errw.String())
		}
	}
	errw.Reset()
	if code := run([]string{"events", "requeue", "missing"}, &out, &errw, noenv); code != 1 || !strings.Contains(errw.String(), "not a dead event") {
		t.Fatalf("requeue missing: exit %d, stderr %q", code, errw.String())
	}
}
