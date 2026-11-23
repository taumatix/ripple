package config

import (
	"strings"
	"testing"
)

func TestCargoEcosystem(t *testing.T) {
	r, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: cargo\n    cooldown: 3d\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := r.UpdateFor("cargo", "crates/x/Cargo.toml")
	if !ok || u.Directory != "/" {
		t.Fatalf("UpdateFor = %+v, %v", u, ok)
	}
}

func TestCargoRejectsForeignKeys(t *testing.T) {
	_, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: cargo\n    tidy: minimal\n"))
	if err == nil || !strings.Contains(err.Error(), "does not apply to ecosystem: cargo") {
		t.Errorf("err = %v", err)
	}
}
