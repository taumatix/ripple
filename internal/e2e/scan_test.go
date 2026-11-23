package e2e

import (
	"math/rand"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

func noise(n int) string {
	b := make([]byte, n)
	rand.New(rand.NewSource(7)).Read(b)
	return string(b)
}

func scanRepoFiles() map[string]string {
	return map[string]string{
		"go.mod":       gomod("example.com/app", "example.com/lib v1.0.0"),
		"main.go":      blankImports("example.com/lib"),
		"assets/a.bin": noise(3 << 20),
	}
}

// A scan reads the tree and the files managers match, not the 3 MiB asset nobody
// reads; the update that follows still builds on a full checkout and opens its PR.
func TestScanDownloadsOnlyWhatManagersRead(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{gitHTTP: true})
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
		e.gh.AddRepo("acme/app", scanRepoFiles())
		start(t, e, "acme/app")

		if n := len(e.deps("acme/app")); n != 1 {
			t.Fatalf("scan found %d dependencies, want 1", n)
		}
		if n := e.git.BytesServed(); n > 400<<10 {
			t.Errorf("the scan made the git server send %d bytes; the 3 MiB asset should not have been fetched", n)
		}
		if e.git.Rejected() != 0 {
			t.Errorf("%d git requests were refused for credentials", e.git.Rejected())
		}
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Fatalf("got %d PRs, want 1 (the update needs the full checkout, which the scan did not)", n)
		}
	})
}

// A host without partial clone sends everything; the scan and the update still work.
func TestScanOnHostWithoutPartialClone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{gitHTTP: true})
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
		e.gh.AddRepo("acme/app", scanRepoFiles())
		e.gh.DisablePartialClone("acme/app")
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 1 {
			t.Fatalf("scan found %d dependencies, want 1", n)
		}
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Fatalf("got %d PRs, want 1", n)
		}
	})
}

// A repo whose config file is a symlink cannot be read from the tree alone; the scan
// repeats on a full checkout and honours the file.
func TestScanFallsBackToFullCheckout(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":       gomod("example.com/app", "example.com/lib v1.0.0"),
			"main.go":      blankImports("example.com/lib"),
			"conf/ign.yml": ignoreLib,
			".ripple.yaml": "symlink:conf/ign.yml",
		})
		start(t, e, "acme/app")
		if n := e.logLines("partial scan unavailable", "repo=acme/app"); n != 1 {
			t.Errorf("fallback logged %d times, want 1\n%s", n, e.logs.String())
		}
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("ignore rule behind a symlinked config was not applied: %d PRs", n)
		}
	})
}

// Hosts differ in what they serve (docs/bounds.md): one that filters a clone but
// refuses a blob by object id cannot take the batched fetch, so the scan repeats on a
// full checkout, once, and still finds the dependency and opens the PR. One that
// serves a blob by id but ignores the filter needs no fallback: it sent everything.
// The server speaks protocol v0, the only one that enforces the by-id setting (v2
// serves any object it has).
func TestScanOnHostsWithPartialCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name             string
		filter, blobByID bool
		wantFallbacks    int
	}{
		{"filter without blob by id", true, false, 1},
		{"blob by id without filter", false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := newEnvOpts(t, st, defaultLimits(), opts{gitHTTP: true})
				stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
				e.gh.AddRepo("acme/app", scanRepoFiles())
				e.gh.SetPartialClone("acme/app", tc.filter, tc.blobByID)
				e.git.SpeakProtocolV0()
				start(t, e, "acme/app")
				if n := len(e.deps("acme/app")); n != 1 {
					t.Fatalf("scan found %d dependencies, want 1", n)
				}
				if n := e.logLines("partial scan unavailable", "repo=acme/app"); n != tc.wantFallbacks {
					t.Errorf("fallback logged %d times, want %d\n%s", n, tc.wantFallbacks, e.logs.String())
				}
				e.release()
				if n := len(e.gh.PRs("acme/app")); n != 1 {
					t.Fatalf("got %d PRs, want 1", n)
				}
			})
		})
	}
}
