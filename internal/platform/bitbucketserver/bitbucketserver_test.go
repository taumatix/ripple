package bitbucketserver_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucketserver"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/testutil"
)

// docs/vendor/bitbucket-server/webhooks.md, "Webhook secrets": the header is
// "sha256=" and the HMAC-SHA256 hex of the body. The literal was computed with
// `printf '{"a":1}' | openssl dgst -sha256 -hmac 'It Is a Secret'`.
func TestSignLiteral(t *testing.T) {
	got := bitbucketserver.Sign([]byte("It Is a Secret"), []byte(`{"a":1}`))
	if want := "sha256=1ec505c13b687cb065132d3af873308853269bde90965c84bb6d22b08af090f5"; got != want {
		t.Errorf("Sign = %s, want %s", got, want)
	}
}

type fakeLookup struct {
	mu       sync.Mutex
	def      string
	defErr   error
	files    []string
	filesErr error
	defCalls int
	fcCalls  int
	from, to string
}

func (l *fakeLookup) DefaultBranch(context.Context, model.Repo) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.defCalls++
	return l.def, l.defErr
}
func (l *fakeLookup) ChangedFiles(_ context.Context, _ model.Repo, from, to string) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fcCalls++
	l.from, l.to = from, to
	return l.files, l.filesErr
}

type sink struct {
	mu  sync.Mutex
	evs []platform.Event
	err error
}

func (s *sink) HandleEvent(_ context.Context, e platform.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.evs = append(s.evs, e)
	return nil
}

var (
	from = "197a3e0d2f9a2b3ed1c4fe5923d5dd701bee9fdd"
	to   = "a00945762949b7b787ecabc388c0e20b1b85f0b4"
)

func newReceiver(l *fakeLookup, s *sink) *httptest.Server {
	r := &bitbucketserver.Receiver{Secret: testSecret, Sink: s, Lookup: l, Log: slog.New(slog.DiscardHandler)}
	srv := httptest.NewServer(r)
	return srv
}

func TestReceiverPush(t *testing.T) {
	l := &fakeLookup{def: "main", files: []string{"go.mod", "x/y.go"}}
	s := &sink{}
	srv := newReceiver(l, s)
	defer srv.Close()

	// The payload's field names are those of event-payload.md "Push".
	if c := post(t, srv.URL, testSecret, "repo:refs_changed", "r1", refsChanged("ACME/p", "refs/heads/main", from, to, "UPDATE")); c != http.StatusAccepted {
		t.Fatalf("status %d", c)
	}
	if len(s.evs) != 1 {
		t.Fatalf("events: %+v", s.evs)
	}
	ev := s.evs[0]
	if ev.Kind != platform.Push || ev.Ref != "refs/heads/main" || ev.DefaultBranch != "main" || ev.Delivery != "r1" ||
		!reflect.DeepEqual(ev.Repos, []string{"ACME/p"}) || !reflect.DeepEqual(ev.Files, []string{"go.mod", "x/y.go"}) {
		t.Errorf("event %+v", ev)
	}
	if l.from != from || l.to != to {
		t.Errorf("ChangedFiles(%s, %s)", l.from, l.to)
	}

	// A branch other than the default: no event, no file lookup; the default branch is cached.
	post(t, srv.URL, testSecret, "repo:refs_changed", "r2", refsChanged("ACME/p", "refs/heads/feature", from, to, "UPDATE"))
	if len(s.evs) != 1 || l.fcCalls != 1 || l.defCalls != 1 {
		t.Errorf("feature push: events %d, file lookups %d, default lookups %d", len(s.evs), l.fcCalls, l.defCalls)
	}

	// Deleting the default branch is not a push to scan.
	post(t, srv.URL, testSecret, "repo:refs_changed", "r3", refsChanged("ACME/p", "refs/heads/main", to, "0000000000000000000000000000000000000000", "DELETE"))
	if len(s.evs) != 1 {
		t.Errorf("a delete produced an event")
	}

	// Creating the default branch has no prior commit to compare with: empty Files forces a scan.
	post(t, srv.URL, testSecret, "repo:refs_changed", "r4", refsChanged("ACME/p", "refs/heads/main", "0000000000000000000000000000000000000000", to, "ADD"))
	if len(s.evs) != 2 || len(s.evs[1].Files) != 0 || l.fcCalls != 1 {
		t.Errorf("ADD: %+v (file lookups %d)", s.evs, l.fcCalls)
	}
}

func TestReceiverRejectsAndIgnores(t *testing.T) {
	l := &fakeLookup{def: "main"}
	s := &sink{}
	srv := newReceiver(l, s)
	defer srv.Close()
	body := refsChanged("ACME/p", "refs/heads/main", from, to, "UPDATE")

	if c := post(t, srv.URL, []byte("wrong"), "repo:refs_changed", "a", body); c != http.StatusUnauthorized {
		t.Errorf("bad signature: %d", c)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	req.Header.Set("X-Event-Key", "repo:refs_changed")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	req.Header.Set("X-Event-Key", "repo:refs_changed")
	req.Header.Set("X-Hub-Signature", "sha256=zz")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("malformed signature: %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", resp.StatusCode)
	}
	// Events ripple does not act on are acknowledged, and cost nothing.
	if c := post(t, srv.URL, testSecret, "pr:opened", "b", []byte(`{}`)); c != http.StatusAccepted {
		t.Errorf("pr:opened: %d", c)
	}
	if c := post(t, srv.URL, testSecret, "diagnostics:ping", "c", []byte(`{"test":true}`)); c != http.StatusOK {
		t.Errorf("ping: %d", c)
	}
	if len(s.evs) != 0 || l.defCalls != 0 {
		t.Errorf("rejected deliveries had effects: %+v, %d lookups", s.evs, l.defCalls)
	}
}

func TestReceiverDedupeAndFailures(t *testing.T) {
	l := &fakeLookup{def: "main", files: []string{"go.mod"}}
	s := &sink{}
	srv := newReceiver(l, s)
	defer srv.Close()
	body := refsChanged("ACME/p", "refs/heads/main", from, to, "UPDATE")

	post(t, srv.URL, testSecret, "repo:refs_changed", "same", body)
	post(t, srv.URL, testSecret, "repo:refs_changed", "same", body)
	if len(s.evs) != 1 {
		t.Errorf("repeated X-Request-Id delivered %d events", len(s.evs))
	}

	// A sink that refuses makes the delivery retryable: 503, and the id is released.
	s.err = errors.New("full")
	if c := post(t, srv.URL, testSecret, "repo:refs_changed", "busy", body); c != http.StatusServiceUnavailable {
		t.Errorf("sink error: %d", c)
	}
	s.err = nil
	if c := post(t, srv.URL, testSecret, "repo:refs_changed", "busy", body); c != http.StatusAccepted || len(s.evs) != 2 {
		t.Errorf("retry: %d, %d events", c, len(s.evs))
	}

	// Changed files that cannot be fetched force a scan rather than dropping the push.
	l.filesErr = errors.New("boom")
	post(t, srv.URL, testSecret, "repo:refs_changed", "nofiles", body)
	if len(s.evs) != 3 || len(s.evs[2].Files) != 0 {
		t.Errorf("files lookup failure: %+v", s.evs)
	}

	// A default branch that cannot be fetched is retryable: nothing is known about the push.
	srv2 := newReceiver(&fakeLookup{defErr: errors.New("down")}, s)
	defer srv2.Close()
	if c := post(t, srv2.URL, testSecret, "repo:refs_changed", "x", body); c != http.StatusServiceUnavailable || len(s.evs) != 3 {
		t.Errorf("default lookup failure: %d", c)
	}
}

// The client against the strict fake: the wire formats the fake enforces are the
// vendored OpenAPI's (create body, merge query and body, paging).
func newClient2(t *testing.T) (*bitbucketserver.Client, *testutil.BitbucketServer) {
	b := testutil.NewBitbucketServer(t)
	return newClient(b, gitws.Options{}, nil), b
}

func TestListReposPagesAndFilters(t *testing.T) {
	c, b := newClient2(t)
	b.PageMax = 2
	var want []string
	for i := 0; i < 5; i++ {
		n := fmt.Sprintf("ACME/r%d", i)
		b.AddRepo(n, map[string]string{"README.md": "x"})
		want = append(want, n)
	}
	b.Show(want...)
	got, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range got {
		names = append(names, r.FullName)
		if r.DefaultBranch != "" {
			t.Errorf("listing invented a default branch: %+v", r)
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) {
		t.Errorf("repos %v, want %v", names, want)
	}
}

func TestCreateListAndAutoMerge(t *testing.T) {
	c, b := newClient2(t)
	b.PageMax = 1
	b.AddRepo("ACME/p", map[string]string{"README.md": "x"})
	repo := model.Repo{FullName: "ACME/p", DefaultBranch: "main"}
	ctx := context.Background()
	for _, br := range []string{"ripple/go/a", "ripple/go/b", "other"} {
		b.Branch("ACME/p", br)
	}
	var made []platform.PR
	for _, br := range []string{"ripple/go/a", "ripple/go/b", "other"} {
		pr, err := c.CreatePR(ctx, repo, platform.PRSpec{Head: br, Base: "main", Title: "t " + br, Body: "b", Draft: br == "ripple/go/b", Labels: []string{"x"}})
		if err != nil {
			t.Fatal(err)
		}
		if pr.Head != br || pr.Number == 0 || pr.URL == "" {
			t.Errorf("pr %+v", pr)
		}
		made = append(made, pr)
	}
	prs, err := c.ListOpenPRs(ctx, repo, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].Head != "ripple/go/a" || prs[1].Head != "ripple/go/b" {
		t.Errorf("listed %+v", prs)
	}
	fp := b.PRs("ACME/p")
	if fp[0].Draft || !fp[1].Draft {
		t.Errorf("draft flags %+v", fp)
	}
	if err := c.RequestAutoMerge(ctx, repo, made[0]); err != nil {
		t.Fatal(err)
	}
	if !b.PRs("ACME/p")[0].AutoMerge || len(b.Merges) != 1 || b.Merges[0] != `{"autoMerge":true}` {
		t.Errorf("merges %v", b.Merges)
	}
	// A repository whose auto-merge setting is off answers 403: surfaced, not hidden.
	b.AutoMergeOff = true
	if err := c.RequestAutoMerge(ctx, repo, made[2]); err == nil {
		t.Error("403 swallowed")
	}
	// A second PR for the same branches is a conflict.
	if _, err := c.CreatePR(ctx, repo, platform.PRSpec{Head: "other", Base: "main", Title: "dup"}); err == nil {
		t.Error("duplicate PR accepted")
	}
}

func TestRateLimit(t *testing.T) {
	b := testutil.NewBitbucketServer(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	c := newClient(b, gitws.Options{}, func() time.Time { return now })
	b.AddRepo("ACME/p", map[string]string{"README.md": "x"})
	b.Branch("ACME/p", "ripple/x")
	repo := model.Repo{FullName: "ACME/p"}
	b.RateLimitOnce = true
	_, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"})
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(time.Second)) {
		t.Fatalf("err %v", err)
	}
	if !c.RateLimitedUntil(repo).Equal(now.Add(time.Second)) {
		t.Errorf("RateLimitedUntil %v", c.RateLimitedUntil(repo))
	}
}

func TestUnsafeRepoNames(t *testing.T) {
	c, _ := newClient2(t)
	for _, n := range []string{"", "ACME", "../x/y", "ACME/..", "ACME/a/b", "ACME/a b", "AC/ME/x"} {
		if _, err := c.ListOpenPRs(context.Background(), model.Repo{FullName: n}, ""); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestChangedFilesAndDefaultBranch(t *testing.T) {
	c, b := newClient2(t)
	b.PageMax = 1
	b.AddRepo("ACME/p", map[string]string{"README.md": "x"})
	f, to := b.Commit("ACME/p", map[string]string{"go.mod": "m", "a/b.go": "p"})
	repo := model.Repo{FullName: "ACME/p"}
	files, err := c.ChangedFiles(context.Background(), repo, f, to)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if !reflect.DeepEqual(files, []string{"a/b.go", "go.mod"}) {
		t.Errorf("files %v", files)
	}
	d, err := c.DefaultBranch(context.Background(), repo)
	if err != nil || d != "main" {
		t.Errorf("default %q, %v", d, err)
	}
}

// The conformance fixture leaves the receiver's two lookups out of Calls. This pins
// what they cost: the default branch once per repository (cached), the changed files
// once per push to it, nothing for another branch, and never a scan or a clone.
func TestPushLookupsAreBounded(t *testing.T) {
	c, b := newClient2(t)
	b.AddRepo("ACME/p", map[string]string{"README.md": "x"})
	s := &sink{}
	srv := httptest.NewServer(bitbucketserver.NewHost(c, testSecret, slog.New(slog.DiscardHandler)).Receiver(s))
	defer srv.Close()

	for i := 0; i < 3; i++ {
		f, to := b.Commit("ACME/p", map[string]string{"README.md": fmt.Sprint(i)})
		post(t, srv.URL, testSecret, "repo:refs_changed", fmt.Sprint("feat", i), refsChanged("ACME/p", "refs/heads/feat", f, to, "UPDATE"))
	}
	if n := b.Calls("ACME/p"); n != 1 || b.Lookups("ACME/p") != 1 || len(s.evs) != 0 {
		t.Errorf("3 pushes to another branch: %d calls, %d lookups, %d events", n, b.Lookups("ACME/p"), len(s.evs))
	}
	f, to := b.Commit("ACME/p", map[string]string{"go.mod": "m"})
	post(t, srv.URL, testSecret, "repo:refs_changed", "main1", refsChanged("ACME/p", "refs/heads/main", f, to, "UPDATE"))
	if n := b.Calls("ACME/p"); n != 2 || b.Calls("ACME/p") != b.Lookups("ACME/p") || len(s.evs) != 1 || !reflect.DeepEqual(s.evs[0].Files, []string{"go.mod"}) {
		t.Errorf("push to main: %d calls (%d lookups), events %+v", n, b.Lookups("ACME/p"), s.evs)
	}
}
