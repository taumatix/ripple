package config

import (
	"strings"
	"testing"
)

func TestGitHubActionsEcosystem(t *testing.T) {
	r, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: gomod\n  - ecosystem: github-actions\n    cooldown: 3d\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := r.UpdateFor("github-actions", ".github/workflows/ci.yml")
	if !ok || u.Directory != "/" {
		t.Fatalf("UpdateFor = %+v, %v; want the entry with directory /", u, ok)
	}
	if r.ManagerName(1) != "github-actions" {
		t.Errorf("ManagerName = %q", r.ManagerName(1))
	}
}

func TestGitHubActionsRejectsForeignKeys(t *testing.T) {
	_, err := Parse([]byte("version: 1\nupdates:\n  - ecosystem: github-actions\n    tidy: minimal\n"))
	if err == nil || !strings.Contains(err.Error(), "does not apply to ecosystem: github-actions") {
		t.Errorf("err = %v", err)
	}
}
