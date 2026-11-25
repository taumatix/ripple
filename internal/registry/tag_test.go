package registry

import "testing"

func TestParseTag(t *testing.T) {
	for _, c := range []struct {
		tag, sv, shape string
		ok             bool
	}{
		{"1.25", "v1.25", "2|-", true},
		{"1.25.3", "v1.25.3", "3|-", true},
		{"v2", "v2", "v1|-", true},
		{"3.19-alpine", "v3.19", "2|-alpine", true},
		{"1.2.3-slim-bookworm", "v1.2.3", "3|-slim-bookworm", true},
		{"latest", "", "", false},
		{"bookworm", "", "", false},
		{"1.25.3.4", "", "", false},
		{"01.2", "", "", false},
		{"22.04", "v22.4", "2|-", true},
		{"2024.01.09-rc", "v2024.1.9", "3|-rc", true},
		{"1.007", "", "", false},
		{"1.2.3@sha256", "", "", false},
		{"1.2.3_rc", "", "", false},
	} {
		sv, shape, ok := ParseTag(c.tag)
		if ok != c.ok || sv != c.sv || shape != c.shape {
			t.Errorf("ParseTag(%q) = %q %q %v, want %q %q %v", c.tag, sv, shape, ok, c.sv, c.shape, c.ok)
		}
	}
}

func TestNewestOfShapeCalendarTags(t *testing.T) {
	tags := []string{"20.04", "22.04", "22.10", "23.04", "23.10", "24.04", "22.04-slim"}
	if got := NewestOfShape("22.04", tags); got != "24.04" {
		t.Errorf("ubuntu 22.04: %q", got)
	}
	if got := NewestOfShape("22.4", []string{"22.07", "22.3"}); got != "22.07" {
		t.Errorf("padded newer than unpadded: %q", got)
	}
}

func TestNewestOfShape(t *testing.T) {
	tags := []string{"1.25", "1.26", "1.26.1", "1.26-alpine", "1.27-alpine", "2.0.0-rc1", "latest", "1.27.0"}
	if got := NewestOfShape("1.25", tags); got != "1.26" {
		t.Errorf("plain 2-component: %q", got)
	}
	if got := NewestOfShape("1.26-alpine", tags); got != "1.27-alpine" {
		t.Errorf("alpine variant stays alpine: %q", got)
	}
	if got := NewestOfShape("1.26.1", tags); got != "1.27.0" {
		t.Errorf("three components: %q", got)
	}
	if got := NewestOfShape("1.27", tags); got != "" {
		t.Errorf("nothing newer: %q", got)
	}
	if got := NewestOfShape("latest", tags); got != "" {
		t.Errorf("untracked: %q", got)
	}
}
