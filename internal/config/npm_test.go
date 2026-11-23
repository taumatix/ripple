package config

import (
	"strings"
	"testing"
)

func TestNpmEcosystem(t *testing.T) {
	r, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: npm\n    cooldown: 3d\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := r.UpdateFor("npm", "web/package.json")
	if !ok || u.Directory != "/" {
		t.Fatalf("UpdateFor = %+v, %v", u, ok)
	}
}

func TestNpmRejectsForeignKeys(t *testing.T) {
	_, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: npm\n    tidy: minimal\n"))
	if err == nil || !strings.Contains(err.Error(), "does not apply to ecosystem: npm") {
		t.Errorf("err = %v", err)
	}
}
