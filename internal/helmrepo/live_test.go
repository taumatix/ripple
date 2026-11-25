//go:build live

package helmrepo

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read a real chart repository's index.yaml anonymously and read only (go test
// -tags live; the live workflow), through the production Client: kubernetes.github.io/
// ingress-nginx (about 190 KB), whose chart ingress-nginx 4.0.1 is a published release.
// Not covered: the deprecated flag (the one repository where it is common, charts.helm.sh/
// stable, is a 10 MB index, too heavy to fetch daily) and OCI chart registries.

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveIndexAndReleases(t *testing.T) {
	ix, err := liveClient().Index(context.Background(), NormalizeURL("https://kubernetes.github.io/ingress-nginx"))
	if err != nil {
		t.Fatal(err)
	}
	vs := ix.Entries["ingress-nginx"]
	if len(vs) < 20 {
		t.Fatalf("%d versions of ingress-nginx", len(vs))
	}
	var seen bool
	for _, v := range vs {
		if v.Version == "4.0.1" {
			seen = true
		}
	}
	if !seen || !slices.Contains(ix.Releases("ingress-nginx"), "4.0.1") {
		t.Errorf("4.0.1 present=%v", seen)
	}
}

func TestLiveMissingRepository(t *testing.T) {
	_, err := liveClient().Index(context.Background(), NormalizeURL("https://kubernetes.github.io/ripple-no-such-repo"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v, want ErrNotFound", err)
	}
}
