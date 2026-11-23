package config

import (
	"strings"
	"testing"
)

func TestDockerEcosystem(t *testing.T) {
	r, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: docker\n    cooldown: 3d\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := r.UpdateFor("docker", "Dockerfile")
	if !ok || u.Directory != "/" {
		t.Fatalf("UpdateFor = %+v, %v; want the entry with directory /", u, ok)
	}
}

func TestDockerRejectsForeignKeys(t *testing.T) {
	_, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: docker\n    go-toolchain: true\n"))
	if err == nil || !strings.Contains(err.Error(), "does not apply to ecosystem: docker") {
		t.Errorf("err = %v", err)
	}
}
