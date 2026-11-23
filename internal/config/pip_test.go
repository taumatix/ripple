package config

import (
	"strings"
	"testing"
)

func TestPipEcosystem(t *testing.T) {
	r, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: pip\n    cooldown: 3d\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := r.UpdateFor("pip", "svc/requirements.txt")
	if !ok || u.Directory != "/" {
		t.Fatalf("UpdateFor = %+v, %v", u, ok)
	}
}

func TestPipRejectsForeignKeys(t *testing.T) {
	_, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: pip\n    tidy: minimal\n"))
	if err == nil || !strings.Contains(err.Error(), "does not apply to ecosystem: pip") {
		t.Errorf("err = %v", err)
	}
}
