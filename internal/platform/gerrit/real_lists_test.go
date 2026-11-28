//go:build gerrit

package gerrit_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

// realClient is a Gerrit client for a fixture's project prefix, running as ripple's ordinary account.
func realClient(f *realGerrit) *gerrit.Client {
	user, pass := f.e.ripple()
	return &gerrit.Client{BaseURL: f.e.base, ProjectPrefix: f.prefix + "/", AuthorName: "ripple", AuthorEmail: authorMail,
		HTTP:        &http.Client{Timeout: 60 * time.Second},
		Credentials: platform.StaticCredential{Username: user, Secret: pass}}
}

// TestRealListRepoHeads (ROADMAP 243): "Get HEAD" then "Get Branch" answer, on a real server, the commit git
// pushed, and a second push moves it.
func TestRealListRepoHeads(t *testing.T) {
	f := &realGerrit{e: realConfig(t)}
	f.Host(t, gitws.Options{WorkDir: t.TempDir()}, time.Now)
	path := f.Path("heads")
	f.AddRepo(t, path, map[string]string{"go.mod": "module x\n"})
	c := realClient(f)

	first := f.pushFiles(t, path, map[string]string{"a.txt": "a\n"})
	heads, err := c.ListRepoHeads(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FACT ListRepoHeads (GET projects/<p>/HEAD, then branches/refs%%2Fheads%%2Fmain) for %s: %q, pushed %q", path, heads[path], first)
	if heads[path] != first {
		t.Fatalf("head = %q, want the pushed %q", heads[path], first)
	}
	second := f.pushFiles(t, path, map[string]string{"b.txt": "b\n"})
	heads, err = c.ListRepoHeads(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second == first || heads[path] != second {
		t.Fatalf("head after the second push = %q, want %q (first was %q)", heads[path], second, first)
	}
}

// TestRealArchivedListing (ROADMAP 213): a READ_ONLY project is absent from the ACTIVE listing and present in
// the READ_ONLY one, on a real server. The webhook events seen in the 10 s after the change are recorded as FACTs
// (ROADMAP 310) and not asserted: what the plugin sends for a state change is unverified.
func TestRealArchivedListing(t *testing.T) {
	f := &realGerrit{e: realConfig(t)}
	f.Host(t, gitws.Options{WorkDir: t.TempDir()}, time.Now)
	path := f.Path("archived")
	f.AddRepo(t, path, map[string]string{"go.mod": "module x\n"})
	c := realClient(f)
	names := func(rs []model.Repo) map[string]bool {
		m := map[string]bool{}
		for _, r := range rs {
			m[r.FullName] = true
		}
		return m
	}
	active, err := c.ListRepos(t.Context())
	if err != nil || !names(active)[path] {
		t.Fatalf("a fresh project must be in the ACTIVE listing: %v %v", names(active), err)
	}

	theRelay.mu.Lock()
	before := len(theRelay.got)
	theRelay.mu.Unlock()
	// a failure below must not leave the project READ_ONLY for the tests that follow
	t.Cleanup(func() { f.SetArchived(t, "", path, false) })
	f.SetArchived(t, "", path, true)
	active, err = c.ListRepos(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ro, err := c.ListArchivedRepos(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FACT READ_ONLY project %s: in the ACTIVE listing=%v, in the READ_ONLY listing=%v", path, names(active)[path], names(ro)[path])
	if names(active)[path] || !names(ro)[path] {
		t.Errorf("READ_ONLY project: active=%v read_only=%v", names(active)[path], names(ro)[path])
	}
	time.Sleep(10 * time.Second)
	theRelay.mu.Lock()
	var seen []string
	for _, d := range theRelay.got[before:] {
		seen = append(seen, d.Type+" "+d.Project+" "+d.Ref)
	}
	theRelay.mu.Unlock()
	t.Logf("FACT webhook deliveries within 10s of setting the project READ_ONLY: %d %q", len(seen), seen)

	f.SetArchived(t, "", path, false)
	active, err = c.ListRepos(t.Context())
	if err != nil || !names(active)[path] {
		t.Errorf("back to ACTIVE, the project must list again: %v %v", names(active)[path], err)
	}
}
