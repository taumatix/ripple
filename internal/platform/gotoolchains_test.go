package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeGoDir(t *testing.T, releases ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, r := range releases {
		bin := filepath.Join(dir, "go"+r, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFindGo(t *testing.T) {
	dir := fakeGoDir(t, "1.25.3", "1.25.7", "1.26.1", "1.24")
	for _, c := range []struct{ name, goLine, toolchain, want string }{
		{"the toolchain line wins and is matched exactly", "1.25", "go1.25.3", "1.25.3"},
		{"a toolchain line with no such release finds none", "1.25", "go1.25.5", ""},
		{"a go line without a patch takes the newest patch of its minor", "1.25", "", "1.25.7"},
		{"a go line with a patch takes that release or a newer one of the minor", "1.25.4", "", "1.25.7"},
		{"a go line with a patch above everything of the minor finds none", "1.25.9", "", ""},
		{"a minor with only an unpatched directory", "1.24", "", "1.24"},
		{"a minor that is not installed", "1.27", "", ""},
		{"an unreadable line finds none", "latest", "", ""},
		{"a path is not a version", "1.25", "go../../etc", ""},
	} {
		bin, rel := FindGo(dir, c.goLine, c.toolchain)
		if rel != c.want {
			t.Errorf("%s: release %q, want %q", c.name, rel, c.want)
		}
		if c.want != "" && bin != filepath.Join(dir, "go"+c.want, "bin", "go") {
			t.Errorf("%s: bin %q", c.name, bin)
		}
	}
	if bin, rel := FindGo("", "1.25", ""); bin != "" || rel != "" {
		t.Errorf("no directory configured must find nothing, got %q %q", bin, rel)
	}
	if bin, _ := FindGo(filepath.Join(dir, "missing"), "1.25", ""); bin != "" {
		t.Errorf("a missing directory must find nothing, got %q", bin)
	}
}

func TestFindGoSkipsADirectoryWithoutABinary(t *testing.T) {
	dir := fakeGoDir(t, "1.25.3")
	if err := os.MkdirAll(filepath.Join(dir, "go1.25.9"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, rel := FindGo(dir, "1.25", ""); rel != "1.25.3" {
		t.Errorf("release %q, want the one that has a binary", rel)
	}
}

func TestGoMinor(t *testing.T) {
	for in, want := range map[string]string{"1.26": "1.26", "1.26.1": "1.26", "go1.25.3": "1.25", "go1.27rc1": "1.27", "1.28beta1": "1.28", "default": "", "": "", "2.0": ""} {
		if got := GoMinor(in); got != want {
			t.Errorf("GoMinor(%q) = %q, want %q", in, got, want)
		}
	}
}
