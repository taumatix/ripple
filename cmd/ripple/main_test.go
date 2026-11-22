package main

import (
	"bytes"
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
