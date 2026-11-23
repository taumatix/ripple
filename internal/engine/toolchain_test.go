package engine

import "testing"

func TestToolchainTarget(t *testing.T) {
	// Two supported releases, as the feed gives them: 1.27.4 and 1.26.11.
	latest, oldest := latestPerMinor([]string{"v1.27.4", "v1.26.11", "v1.26.9"})
	if oldest != "v1.26" || latest["v1.26"] != "v1.26.11" {
		t.Fatalf("latest=%v oldest=%q", latest, oldest)
	}
	for _, c := range []struct{ name, pkg, cur, want string }{
		{"toolchain takes its minor's newest patch", "toolchain", "v1.26.3", "v1.26.11"},
		{"toolchain already there", "toolchain", "v1.26.11", ""},
		{"toolchain on the newer supported minor", "toolchain", "v1.27.1", "v1.27.4"},
		{"toolchain never moves back", "toolchain", "v1.27.9", ""},
		{"unsupported toolchain moves to the oldest supported minor", "toolchain", "v1.24.2", "v1.26.11"},
		{"toolchain newer than anything supported is left alone", "toolchain", "v1.28.0", ""},
		{"go directive of a supported minor is left alone", "go", "v1.26", ""},
		{"go directive with a patch of a supported minor is left alone", "go", "v1.26.0", ""},
		{"go directive past end of support moves to the oldest supported minor", "go", "v1.24", "v1.26.11"},
		{"go directive newer than anything supported is left alone", "go", "v1.28", ""},
		{"an unknown package is left alone", "other", "v1.24", ""},
	} {
		if got := ToolchainTarget(c.pkg, c.cur, latest, oldest); got != c.want {
			t.Errorf("%s: ToolchainTarget(%s, %s) = %q, want %q", c.name, c.pkg, c.cur, got, c.want)
		}
	}
	if got := ToolchainTarget("go", "v1.24", nil, ""); got != "" {
		t.Errorf("an empty list must propose nothing, got %q", got)
	}
}
