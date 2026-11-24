package github_test

import (
	"context"
	"errors"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

// noise is incompressible, so its size on the wire is its size.
func noise(n int) string {
	b := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(b)
	return string(b)
}

func scanRepo(t *testing.T, files map[string]string, mutate func(*testutil.GitHub)) (*testutil.GitHTTP, *github.Client) {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/r", files)
	if mutate != nil {
		mutate(gh)
	}
	remote := gh.ServeGit()
	return remote, &github.Client{APIBase: gh.APIURL, GitBase: remote.URL, Tokens: gh.Auth, WorkDir: t.TempDir(), GoSumDB: "off", GoProxy: "off"}
}

func scanCheckout(t *testing.T, c *github.Client) platform.Workspace {
	t.Helper()
	ws, err := c.Checkout(platform.WithScanOnly(context.Background()), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func walk(t *testing.T, ws platform.Workspace) []string {
	t.Helper()
	var got []string
	if err := ws.Walk(func(p string) error { got = append(got, p); return nil }); err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	return got
}

// A scan reads a tree listing and the few files managers match: a 3 MB file nothing
// matches is never downloaded, and the server (which demands the installation token
// on every request) never refuses a lazy fetch.
func TestScanOnlyDownloadsOnlyWhatIsRead(t *testing.T) {
	remote, c := scanRepo(t, map[string]string{
		"go.mod":           "module x\n\ngo 1.22\n",
		"data/big.bin":     noise(3 << 20),
		"vendor/v/v.go":    "package v\n",
		"node_modules/n.j": "x\n",
		"link":             "symlink:go.mod",
		"sub/a.txt":        "a\n",
		".ripple.yaml":     "go-toolchain: directive\n",
	}, nil)
	ws := scanCheckout(t, c)

	if got, want := walk(t, ws), []string{".ripple.yaml", "data/big.bin", "go.mod", "sub/a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Walk = %v, want %v (symlinks, vendor and node_modules are not files to scan)", got, want)
	}
	// A path no prefetch asked for is still readable, one blob at a time.
	if b, err := ws.ReadFile(".ripple.yaml"); err != nil || string(b) != "go-toolchain: directive\n" {
		t.Fatalf("ReadFile(.ripple.yaml) = %q, %v", b, err)
	}
	if _, err := ws.ReadFile(".github/ripple.yml"); !errors.Is(err, platform.ErrNotExist) {
		t.Errorf("absent file: err = %v, want ErrNotExist", err)
	}
	pf, ok := ws.(platform.Prefetcher)
	if !ok {
		t.Fatal("a scan-only workspace must implement platform.Prefetcher")
	}
	if err := pf.Prefetch(context.Background(), []string{"go.mod", "sub/a.txt"}); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"go.mod": "module x\n\ngo 1.22\n", "sub/a.txt": "a\n"} {
		if b, err := ws.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("ReadFile(%s) = %q, %v", p, b, err)
		}
	}
	if n := remote.BytesServed(); n > 300<<10 {
		t.Errorf("server sent %d bytes for a scan of a repo whose only large file nothing reads; want well under the 3 MiB file", n)
	}
	if remote.Rejected() != 0 {
		t.Errorf("%d requests refused for credentials: a lazy fetch ran without the token", remote.Rejected())
	}
}

// Paths git's sparse patterns would mangle are read one by one instead of skipped.
func TestScanOnlyReadsPathsWithPatternCharacters(t *testing.T) {
	_, c := scanRepo(t, map[string]string{"go.mod": "module x\n", "a*b[1].txt": "star\n", "!bang.txt": "bang\n", "#hash.txt": "hash\n"}, nil)
	ws := scanCheckout(t, c)
	paths := walk(t, ws)
	if err := ws.(platform.Prefetcher).Prefetch(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"a*b[1].txt": "star\n", "!bang.txt": "bang\n", "#hash.txt": "hash\n", "go.mod": "module x\n"} {
		if b, err := ws.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("ReadFile(%s) = %q, %v", p, b, err)
		}
	}
}

// A host that does not implement partial clone sends everything; the scan still works.
func TestScanOnlyWorksWhenServerIgnoresTheFilter(t *testing.T) {
	_, c := scanRepo(t, map[string]string{"go.mod": "module x\n", "sub/a.txt": "a\n"}, func(g *testutil.GitHub) { g.DisablePartialClone("acme/r") })
	ws := scanCheckout(t, c)
	if err := ws.(platform.Prefetcher).Prefetch(context.Background(), []string{"go.mod"}); err != nil {
		t.Fatal(err)
	}
	if b, err := ws.ReadFile("go.mod"); err != nil || string(b) != "module x\n" {
		t.Errorf("ReadFile = %q, %v", b, err)
	}
}

// The scan checkout is for reading; nothing may be committed from it.
func TestScanOnlyWorkspaceRefusesWrites(t *testing.T) {
	_, c := scanRepo(t, map[string]string{"go.mod": "module x\n"}, nil)
	ws := scanCheckout(t, c)
	if err := ws.WriteFile("go.mod", []byte("x")); err == nil {
		t.Error("WriteFile on a scan-only workspace succeeded")
	}
	if _, err := ws.CommitAndPush(context.Background(), "ripple/x", "m"); err == nil {
		t.Error("CommitAndPush on a scan-only workspace succeeded")
	}
	if _, err := ws.RunGo(context.Background(), ".", "mod", "tidy"); err == nil {
		t.Error("RunGo on a scan-only workspace succeeded")
	}
	if err := ws.Reset(context.Background()); err == nil {
		t.Error("Reset on a scan-only workspace succeeded")
	}
}

func uploadPacks(remote *testutil.GitHTTP) int {
	n := 0
	for _, u := range remote.RequestURLs() {
		if strings.HasSuffix(u, "/git-upload-pack") {
			n++
		}
	}
	return n
}

// Prefetch asks for every blob by object id in one fetch, whatever the path looks
// like, and the reads that follow are served from the clone with no request.
func TestScanOnlyPrefetchIsOneFetchByObjectID(t *testing.T) {
	remote, c := scanRepo(t, map[string]string{
		"go.mod": "module x\n", "sub/a.txt": "a\n", "a*b[1].txt": "star\n", "#hash.txt": "hash\n", "big.bin": noise(2 << 20),
	}, nil)
	ws := scanCheckout(t, c)
	paths := []string{"go.mod", "sub/a.txt", "a*b[1].txt", "#hash.txt"}
	if _, err := ws.ReadFile("go.mod"); err != nil { // the config read before the walk
		t.Fatal(err)
	}
	before := uploadPacks(remote)
	if err := ws.(platform.Prefetcher).Prefetch(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if got := uploadPacks(remote) - before; got != 1 {
		t.Errorf("Prefetch of %d paths made %d fetches, want 1", len(paths), got)
	}
	before = uploadPacks(remote)
	for _, p := range paths {
		if _, err := ws.ReadFile(p); err != nil {
			t.Errorf("ReadFile(%s): %v", p, err)
		}
	}
	if got := uploadPacks(remote) - before; got != 0 {
		t.Errorf("reads after Prefetch made %d fetches, want 0", got)
	}
	if n := remote.BytesServed(); n > 300<<10 {
		t.Errorf("server sent %d bytes; the 2 MiB file nothing reads must not be downloaded", n)
	}
}

// A host that refuses shallow clients still gets a checkout: Checkout falls back to a
// whole clone after the shallow attempts fail.
func TestCheckoutFallsBackToFullCloneWhenShallowRefused(t *testing.T) {
	remote, c := scanRepo(t, map[string]string{"go.mod": "module x\n\ngo 1.22\n", "a.txt": "a"}, nil)
	remote.RefuseShallow()
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: "acme/r", InstallationID: 1, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	b, err := ws.ReadFile("a.txt")
	if err != nil || string(b) != "a" {
		t.Fatalf("read a.txt: %q %v", b, err)
	}
}
