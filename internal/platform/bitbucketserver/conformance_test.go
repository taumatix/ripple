package bitbucketserver_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucketserver"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
)

func TestConformanceBitbucketServer(t *testing.T) { platformtest.Run(t, &fixture{}) }

var testSecret = []byte("conformance secret")

func newClient(b *testutil.BitbucketServer, o gitws.Options, now func() time.Time) *bitbucketserver.Client {
	return &bitbucketserver.Client{APIBase: b.APIURL, GitBase: b.GitBase, Now: now,
		Credentials: platform.StaticCredential{Username: testutil.BitbucketServerUser, Secret: testutil.BitbucketServerToken},
		WorkDir:     o.WorkDir, GoProxy: o.GoProxy, GoSumDB: o.GoSumDB, GoCacheDir: o.GoCacheDir, Sandbox: o.Sandbox}
}

// fixture drives the fake Bitbucket Server. The product has no repository-created
// event, so the host is a Lister: Register only makes the repos visible to the
// token. Pushes are signed repo:refs_changed webhooks, which carry neither the
// default branch nor the files, so the receiver asks the API for both; Calls leaves
// those two lookups out, and TestPushLookupsAreBounded pins what they cost.
type fixture struct{ b *testutil.BitbucketServer }

func (f *fixture) Host(t *testing.T, o gitws.Options, now func() time.Time) *platform.Host {
	f.b = testutil.NewBitbucketServer(t)
	return bitbucketserver.NewHost(newClient(f.b, o, now), testSecret, slog.New(slog.DiscardHandler))
}

func (f *fixture) Path(short string) string { return "ACME/" + short }
func (f *fixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.b.AddRepo(path, files)
}
func (f *fixture) Register(_ *testing.T, _ string, paths ...string) { f.b.Show(paths...) }

func (f *fixture) Push(t *testing.T, hook, path string, files map[string]string) {
	from, to := f.b.Commit(path, files)
	postPush(t, hook, testSecret, refsChanged(path, "refs/heads/main", from, to, "UPDATE"), "req-"+to)
}

func (f *fixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.b.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft, AutoMerge: p.AutoMerge, Closed: p.Closed, Comments: p.Comments})
	}
	return out
}
func (f *fixture) FileAt(path, branch, file string) (string, bool) {
	return f.b.FileAt(path, branch, file)
}
func (f *fixture) Calls(path string) int { return f.b.Calls(path) - f.b.Lookups(path) }
func (f *fixture) RateLimitNextPR() bool { f.b.RateLimitOnce = true; return true }

// refsChanged is a repo:refs_changed body shaped as in docs/vendor/bitbucket-server/event-payload.md.
func refsChanged(path, ref, from, to, typ string) []byte {
	key, slug, _ := strings.Cut(path, "/")
	b, _ := json.Marshal(map[string]any{
		"eventKey": "repo:refs_changed",
		"repository": map[string]any{"slug": slug, "scmId": "git",
			"project": map[string]any{"key": key}},
		"changes": []map[string]any{{"ref": map[string]any{"id": ref, "type": "BRANCH"}, "refId": ref,
			"fromHash": from, "toHash": to, "type": typ}},
	})
	return b
}

func postPush(t *testing.T, hook string, secret, body []byte, id string) {
	t.Helper()
	resp := post(t, hook, secret, "repo:refs_changed", id, body)
	if resp != http.StatusAccepted {
		t.Fatalf("push webhook: %d", resp)
	}
}

func post(t *testing.T, hook string, secret []byte, event, id string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.Header.Set("X-Event-Key", event)
	req.Header.Set("X-Request-Id", id)
	req.Header.Set("X-Hub-Signature", bitbucketserver.Sign(secret, body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
