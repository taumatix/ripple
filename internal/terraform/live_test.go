//go:build live

package terraform

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read registry.terraform.io's v1 versions endpoints anonymously and read only
// (go test -tags live; the live workflow), through the production Client: a module
// (terraform-aws-modules/vpc/aws, whose answer nests versions under "modules") and a
// provider (hashicorp/random, top-level "versions"). Not covered: the registry's
// deprecation/"warnings" fields (not read) and private registries.

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveModuleVersions(t *testing.T) {
	p, ok := ModulePackage("terraform-aws-modules/vpc/aws")
	if !ok {
		t.Fatal("module source not accepted")
	}
	vs, err := liveClient().Versions(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(vs, "3.0.0") || len(vs) < 20 || !slices.Contains(Releases(vs), "3.0.0") {
		t.Fatalf("%d versions, 3.0.0 present=%v", len(vs), slices.Contains(vs, "3.0.0"))
	}
}

func TestLiveProviderVersions(t *testing.T) {
	p, ok := ProviderPackage("hashicorp/random")
	if !ok {
		t.Fatal("provider source not accepted")
	}
	vs, err := liveClient().Versions(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(vs, "3.0.0") || !slices.Contains(Releases(vs), "3.0.0") {
		t.Fatalf("%d versions, 3.0.0 present=%v", len(vs), slices.Contains(vs, "3.0.0"))
	}
}

func TestLiveMissingModule(t *testing.T) {
	p, _ := ModulePackage("terraform-aws-modules/ripple-no-such/aws")
	if _, err := liveClient().Versions(context.Background(), p); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want ErrNotFound", err)
	}
}
