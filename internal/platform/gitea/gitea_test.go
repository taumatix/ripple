package gitea_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/testutil"
)

// Literal wire values below are cited to docs/vendor/<flavour>/ sections.

func TestDetectFlavour(t *testing.T) {
	for v, want := range map[string]gitea.Flavour{
		// docs/vendor/README.md "live version responses"; forgejo/numbering-scheme.md
		testutil.GiteaVersion:   gitea.Gitea,
		testutil.ForgejoVersion: gitea.Forgejo,
		"1.22.0":                gitea.Gitea,
		"7.0.0+gitea-1.22.0":    gitea.Forgejo,
	} {
		if got := gitea.DetectFlavour(v); got != want {
			t.Errorf("%q: %s, want %s", v, got, want)
		}
	}
}

func TestParseFlavour(t *testing.T) {
	for in, want := range map[string]gitea.Flavour{"": gitea.Auto, "auto": gitea.Auto, "Gitea": gitea.Gitea, " forgejo ": gitea.Forgejo} {
		if got, err := gitea.ParseFlavour(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := gitea.ParseFlavour("gogs"); err == nil {
		t.Error("gogs accepted")
	}
}

func client(g *testutil.Gitea) *gitea.Client {
	return &gitea.Client{APIBase: g.APIURL, GitBase: g.GitBase, Log: slog.New(slog.DiscardHandler),
		Credentials: platform.StaticCredential{Username: "ripple", Secret: testutil.GiteaToken}}
}

// End to end over HTTP for both flavours: version detection, then the merge body
// carries the flavour's key (swagger MergePullRequestForm: `do` on Gitea, `Do` on
// Forgejo) with merge_when_checks_succeed; the fake rejects the wrong case.
func TestAutoMergeBodyPerFlavour(t *testing.T) {
	for _, forgejo := range []bool{false, true} {
		g := testutil.NewGitea(t, forgejo)
		g.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
		c := client(g)
		repo := model.Repo{FullName: "acme/app", DefaultBranch: "main"}
		ctx := context.Background()
		pr, err := c.CreatePR(ctx, repo, platform.PRSpec{Title: "t", Head: "main", Base: "main", Draft: true})
		if err != nil {
			t.Fatalf("forgejo=%v create: %v", forgejo, err)
		}
		if err := c.RequestAutoMerge(ctx, repo, pr); err != nil {
			t.Fatalf("forgejo=%v merge: %v", forgejo, err)
		}
		if err := c.RequestAutoMerge(ctx, repo, pr); err != nil {
			t.Fatal(err)
		}
		if g.VersionCalls != 1 {
			t.Errorf("version asked %d times, want 1", g.VersionCalls)
		}
		key := `"do"`
		if forgejo {
			key = `"Do"`
		}
		if len(g.Merges) != 2 || !strings.Contains(g.Merges[0], key) || !strings.Contains(g.Merges[0], `"merge_when_checks_succeed":true`) {
			t.Errorf("forgejo=%v merge bodies %v", forgejo, g.Merges)
		}
		if got := g.PRs("acme/app"); len(got) != 1 || !got[0].Draft || !got[0].AutoMerge {
			t.Errorf("forgejo=%v PRs %+v", forgejo, got)
		}
	}
}

func TestExplicitFlavourSkipsDetection(t *testing.T) {
	g := testutil.NewGitea(t, true)
	c := client(g)
	c.Flavour = gitea.Forgejo
	if f, err := c.DetectedFlavour(context.Background()); err != nil || f != gitea.Forgejo || g.VersionCalls != 0 {
		t.Errorf("%v %v calls=%d", f, err, g.VersionCalls)
	}
}

func TestListReposPagesPastTheLimit(t *testing.T) {
	g := testutil.NewGitea(t, false)
	g.PageMax = 2
	var names []string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		g.AddRepo("acme/"+n, map[string]string{"f": "1"})
		names = append(names, "acme/"+n)
	}
	g.Show(names...)
	repos, err := client(g).ListRepos(context.Background())
	if err != nil || len(repos) != 5 {
		t.Fatalf("%d repos, %v", len(repos), err)
	}
}

func TestRepoNameRefusedBeforeAnyRequest(t *testing.T) {
	g := testutil.NewGitea(t, false)
	_, err := client(g).ListOpenPRs(context.Background(), model.Repo{FullName: "acme/../x"}, "")
	if err == nil {
		t.Fatal("unsafe name accepted")
	}
}

func TestRateLimitHeaders(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for name, hdr := range map[string]http.Header{
		"retry-after": {"Retry-After": {"30"}},
		// Codeberg's proxy: IETF RateLimit header (docs/vendor/README.md, assumed).
		"ietf":    {"RateLimit": {`"default";r=0;t=45`}},
		"default": {},
	} {
		want := map[string]time.Duration{"retry-after": 30 * time.Second, "ietf": 45 * time.Second, "default": time.Minute}[name]
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range hdr {
				w.Header()[k] = v
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		c := &gitea.Client{APIBase: srv.URL, Now: func() time.Time { return t0 },
			Credentials: platform.StaticCredential{Secret: "x"}}
		_, err := c.ListOpenPRs(context.Background(), model.Repo{FullName: "a/b"}, "")
		srv.Close()
		if _, ok := err.(*platform.RateLimitError); !ok {
			t.Fatalf("%s: %v", name, err)
		}
		if got := c.RateLimitedUntil(model.Repo{}); !got.Equal(t0.Add(want)) {
			t.Errorf("%s: until %v", name, got)
		}
	}
}

type sink struct{ evs []platform.Event }

func (s *sink) HandleEvent(_ context.Context, e platform.Event) error {
	s.evs = append(s.evs, e)
	return nil
}

func deliver(h http.Handler, hdr map[string]string, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	io.Copy(io.Discard, w.Body)
	return w.Code
}

const pushBody = `{"ref":"refs/heads/main","total_commits":1,"repository":{"full_name":"acme/app","default_branch":"main"},"commits":[{"added":["a.go"],"modified":["go.mod"],"removed":[]}]}`

// Header names: gitea/webhooks.md "Delivery headers" (X-Gitea-Signature, hex HMAC-SHA256,
// no prefix; X-Hub-Signature-256 as sha256=<digest>); forgejo/webhooks.md "Event
// information" and its PHP sample (X-Forgejo-Signature).
func TestReceiverAuthenticatesEachFlavoursHeaders(t *testing.T) {
	secret := []byte("s3cret")
	sig := gitea.Sign(secret, []byte(pushBody))
	cases := map[string]map[string]string{
		"gitea":   {"X-Gitea-Event": "push", "X-Gitea-Signature": sig},
		"forgejo": {"X-Forgejo-Event": "push", "X-Forgejo-Signature": sig},
		"hub256":  {"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sig},
	}
	for name, hdr := range cases {
		s := &sink{}
		h := &gitea.Receiver{Secret: secret, Sink: s, Log: slog.New(slog.DiscardHandler)}
		if code := deliver(h, hdr, pushBody); code != http.StatusAccepted {
			t.Fatalf("%s: %d", name, code)
		}
		if len(s.evs) != 1 || s.evs[0].Kind != platform.Push || s.evs[0].Repos[0] != "acme/app" || len(s.evs[0].Files) != 2 {
			t.Errorf("%s: %+v", name, s.evs)
		}
	}
	for name, hdr := range map[string]map[string]string{
		"unsigned":  {"X-Gitea-Event": "push"},
		"wrong":     {"X-Gitea-Event": "push", "X-Gitea-Signature": gitea.Sign([]byte("other"), []byte(pushBody))},
		"empty-sig": {"X-Gitea-Event": "push", "X-Gitea-Signature": ""},
	} {
		s := &sink{}
		h := &gitea.Receiver{Secret: secret, Sink: s, Log: slog.New(slog.DiscardHandler)}
		if code := deliver(h, hdr, pushBody); code != http.StatusUnauthorized || len(s.evs) != 0 {
			t.Errorf("%s: %d %d", name, code, len(s.evs))
		}
	}
	// No secret configured: nothing authenticates.
	h := &gitea.Receiver{Sink: &sink{}, Log: slog.New(slog.DiscardHandler)}
	if code := deliver(h, cases["gitea"], pushBody); code != http.StatusUnauthorized {
		t.Errorf("no secret: %d", code)
	}
}

func TestReceiverTruncatedPushForcesRescan(t *testing.T) {
	secret := []byte("k")
	body := strings.Replace(pushBody, `"total_commits":1`, `"total_commits":40`, 1)
	s := &sink{}
	h := &gitea.Receiver{Secret: secret, Sink: s, Log: slog.New(slog.DiscardHandler)}
	hdr := map[string]string{"X-Gitea-Event": "push", "X-Gitea-Signature": gitea.Sign(secret, []byte(body))}
	if code := deliver(h, hdr, body); code != http.StatusAccepted || len(s.evs) != 1 || len(s.evs[0].Files) != 0 {
		t.Errorf("%d %+v", code, s.evs)
	}
}

func TestReceiverDedupesAndIgnores(t *testing.T) {
	secret := []byte("k")
	s := &sink{}
	h := &gitea.Receiver{Secret: secret, Sink: s, Log: slog.New(slog.DiscardHandler)}
	hdr := map[string]string{"X-Gitea-Event": "push", "X-Gitea-Delivery": "d1", "X-Gitea-Signature": gitea.Sign(secret, []byte(pushBody))}
	deliver(h, hdr, pushBody)
	deliver(h, hdr, pushBody)
	if len(s.evs) != 1 {
		t.Errorf("redelivery handled %d times", len(s.evs))
	}
	other := `{"action":"opened"}`
	hdr = map[string]string{"X-Gitea-Event": "issues", "X-Gitea-Signature": gitea.Sign(secret, []byte(other))}
	if code := deliver(h, hdr, other); code != http.StatusAccepted || len(s.evs) != 1 {
		t.Errorf("issues: %d", code)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}
