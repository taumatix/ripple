//go:build giteareal

package gitea_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/platform/platformtest"
)

// TestRealConformance is the whole conformance suite against the real instance: ripple's receiver,
// queue, engine and git, with the instance's own pull requests, files and webhooks.
func TestRealConformance(t *testing.T) {
	platformtest.Run(t, &realFixture{in: realInstance(t)})
}

// scene is a user with a token and one repository, "facts", holding main.
func scene(t *testing.T, in *instance) *realFixture {
	t.Helper()
	f := &realFixture{in: in, dirs: map[string]string{}, hooked: map[string]bool{}, cur: t}
	f.user, f.tok = in.newUser(t)
	f.AddRepo(t, f.Path("facts"), map[string]string{"README.md": "facts\n"})
	return f
}

// branchPR pushes a branch with one new file and opens a pull request for it, as ripple does.
func (f *realFixture) branchPR(t *testing.T, short, branch string) int {
	t.Helper()
	path := f.Path(short)
	dir := f.dirs[path]
	f.git(t, dir, "checkout", "-q", "-b", branch)
	writeFiles(t, dir, map[string]string{strings.ReplaceAll(branch, "/", "-") + ".txt": branch + "\n"})
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", branch)
	f.git(t, dir, "push", "-q", f.in.url+"/"+path+".git", branch)
	f.git(t, dir, "checkout", "-q", "main")
	var pr struct {
		Number int `json:"number"`
	}
	f.in.must(t, token(f.tok), http.MethodPost, "/repos/"+path+"/pulls", map[string]any{"title": branch, "head": branch, "base": "main"}, &pr)
	return pr.Number
}

// TestRealVersionAndFlavour: GET /version and whether DetectFlavour reads the flavour the job
// started from it (ROADMAP 12, "Forgejo before 7 cannot be detected").
func TestRealVersionAndFlavour(t *testing.T) {
	in := realInstance(t)
	var v struct {
		Version string `json:"version"`
	}
	in.must(t, nil, http.MethodGet, "/version", nil, &v)
	in.record("version", v)
	t.Logf("FACT version=%q detected=%q job=%q", v.Version, gitea.DetectFlavour(v.Version), in.flavour)
}

// recordingSink collects the events a Receiver hands over.
type recordingSink struct {
	mu sync.Mutex
	ev []platform.Event
}

func (s *recordingSink) HandleEvent(_ context.Context, e platform.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ev = append(s.ev, e)
	return nil
}

func (s *recordingSink) events() []platform.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]platform.Event(nil), s.ev...)
}

func headerNames(h http.Header) []string {
	var names []string
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-") {
			names = append(names, k)
		}
	}
	return names
}

// receiverFor serves ripple's Receiver and points the forwarder at it.
func receiverFor(t *testing.T, f *realFixture, secret string) *recordingSink {
	sink := &recordingSink{}
	rcv := httptest.NewServer(&gitea.Receiver{Secret: []byte(secret), Sink: sink, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(rcv.Close)
	f.fwd = newForwarder(t, f.in)
	f.fwd.mu.Lock()
	f.fwd.next = rcv.URL
	f.fwd.mu.Unlock()
	return sink
}

// TestRealWebhookHeaders: a push webhook of each hook type the instance offers, which signature
// headers arrive, and that ripple's own Receiver accepts the delivery (ROADMAP 12).
func TestRealWebhookHeaders(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	secret := "header secret"
	sink := receiverFor(t, f, secret)
	types := []string{"gitea"}
	if in.flavour == gitea.Forgejo {
		types = append(types, "forgejo")
	}
	for _, typ := range types {
		st, _, body := in.call(t, token(f.tok), http.MethodPost, "/repos/"+f.Path("facts")+"/hooks", map[string]any{
			"type": typ, "active": true, "events": []string{"push"},
			"config": map[string]string{"url": f.fwd.url(in), "content_type": "json", "secret": secret}}, nil)
		t.Logf("FACT create hook type=%s status=%d %s", typ, st, strings.TrimSpace(string(body[:min(len(body), 200)])))
		if st >= 300 {
			continue
		}
		before := len(f.fwd.deliveries())
		dir := f.dirs[f.Path("facts")]
		writeFiles(t, dir, map[string]string{"h-" + typ + ".txt": typ + "\n"})
		f.git(t, dir, "add", "-A")
		f.git(t, dir, "commit", "-q", "-m", typ)
		f.git(t, dir, "push", "-q", in.url+"/"+f.Path("facts")+".git", "main")
		d := f.fwd.await(t, before, "push "+typ, func(d delivery) bool { return d.event() == "push" })
		t.Logf("FACT hook type=%s headers=%v receiver_status=%d", typ, headerNames(d.Headers), d.Status)
	}
	for _, e := range sink.events() {
		t.Logf("FACT event %+v", e)
	}
}

// TestRealRateLimit: does the instance ever answer 429, and with which headers, under a burst
// of authenticated reads (ROADMAP 12, "the real rate-limit answer").
func TestRealRateLimit(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	statuses := map[int]int{}
	var last http.Header
	for i := 0; i < 600; i++ {
		st, h, _ := in.call(t, token(f.tok), http.MethodGet, "/repos/"+f.Path("facts"), nil, nil)
		statuses[st]++
		last = h
	}
	var rl []string
	for k, v := range last {
		if strings.Contains(strings.ToLower(k), "ratelimit") || strings.EqualFold(k, "Retry-After") {
			rl = append(rl, k+"="+strings.Join(v, ","))
		}
	}
	t.Logf("FACT burst of 600 authenticated reads: statuses %v, rate-limit headers on the last answer %v", statuses, rl)
}

// TestRealScheduledMerge: the status of "merge when checks succeed", the body key case on each
// flavour, and what the pull request looks like afterwards (ROADMAP 12).
func TestRealScheduledMerge(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	for i, key := range []string{"do", "Do"} {
		n := f.branchPR(t, "facts", fmt.Sprintf("ripple/go/m%d", i))
		st, _, body := in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/merge", path, n),
			map[string]any{key: "squash", "merge_when_checks_succeed": true}, nil)
		var pr struct {
			State  string `json:"state"`
			Merged bool   `json:"merged"`
		}
		in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", path, n), nil, &pr)
		var tl []struct {
			Type string `json:"type"`
		}
		in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/timeline", path, n), nil, &tl)
		var types []string
		for _, e := range tl {
			types = append(types, e.Type)
		}
		st2, _, body2 := in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/merge", path, n),
			map[string]any{key: "squash", "merge_when_checks_succeed": true}, nil)
		t.Logf("FACT merge key=%q: status %d body %q; pr state=%s merged=%v; timeline %v; second request %d %q",
			key, st, strings.TrimSpace(string(body)), pr.State, pr.Merged, types, st2, strings.TrimSpace(string(body2)))
	}
}

// TestRealLabelByName: whether a label that does not exist yet can be put on a pull request by
// name, and by name once it exists (ROADMAP 12, "labels by name on create").
func TestRealLabelByName(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	n := f.branchPR(t, "facts", "ripple/go/label")
	st, _, body := in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", path, n), map[string]any{"labels": []string{"dependencies"}}, nil)
	t.Logf("FACT label by name, label absent: %d %q", st, strings.TrimSpace(string(body)))
	in.must(t, token(f.tok), http.MethodPost, "/repos/"+path+"/labels", map[string]any{"name": "dependencies", "color": "#0075ca"}, nil)
	st, _, body = in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", path, n), map[string]any{"labels": []string{"dependencies"}}, nil)
	t.Logf("FACT label by name, label exists: %d %q", st, strings.TrimSpace(string(body)))
}

// TestRealGitBasicAuthUser: which basic-auth user names git accepts with the token as the
// password (ROADMAP 12, "the git basic-auth user").
func TestRealGitBasicAuthUser(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	for _, u := range []string{"ripple", "oauth2", "x-access-token", "", f.user} {
		t.Logf("FACT git ls-remote as user %q with the token as password: %s", u, lsRemote(in, f, url.UserPassword(u, f.tok)))
	}
	t.Logf("FACT git ls-remote with the token as the user and no password: %s", lsRemote(in, f, url.UserPassword(f.tok, "")))
	t.Logf("FACT git ls-remote with a wrong token: %s", lsRemote(in, f, url.UserPassword("ripple", "nottoken")))
}

func lsRemote(in *instance, f *realFixture, u *url.Userinfo) string {
	base, _ := url.Parse(in.url)
	base.User = u
	base.Path = "/" + f.Path("facts") + ".git"
	cmd := exec.Command("git", "ls-remote", base.String())
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "refused: " + redact(strings.TrimSpace(string(out)), f.tok)
	}
	return "accepted: " + strings.Fields(string(out))[1]
}

// TestRealRepositoryWebhook: the `repository` event a system webhook delivers when a repository
// is created and deleted, and what ripple's Receiver makes of it (ROADMAP 12, "repository
// created/deleted webhooks are parsed but unproven").
func TestRealRepositoryWebhook(t *testing.T) {
	in := realInstance(t)
	f := &realFixture{in: in, dirs: map[string]string{}, hooked: map[string]bool{}, cur: t}
	f.user, f.tok = in.newUser(t)
	secret := "repo secret"
	sink := receiverFor(t, f, secret)
	typ := "gitea"
	if in.flavour == gitea.Forgejo {
		typ = "forgejo"
	}
	var hook struct {
		ID int `json:"id"`
	}
	admin := basic(in.adminUser, in.adminPass)
	st, _, body := in.call(t, admin, http.MethodPost, "/admin/hooks", map[string]any{
		"type": typ, "active": true, "events": []string{"repository"},
		"config": map[string]string{"url": f.fwd.url(in), "content_type": "json", "secret": secret}}, &hook)
	t.Logf("FACT create system hook: %d %s", st, strings.TrimSpace(string(body[:min(len(body), 200)])))
	if st >= 300 {
		t.Fatalf("a system webhook could not be created")
	}
	t.Cleanup(func() { in.call(t, admin, http.MethodDelete, fmt.Sprintf("/admin/hooks/%d", hook.ID), nil, nil) })
	before := len(f.fwd.deliveries())
	f.AddRepo(t, f.Path("watched"), map[string]string{"README.md": "x\n"})
	d := f.fwd.await(t, before, "repository created", func(d delivery) bool { return d.event() == "repository" })
	_, repo, action := d.payload()
	t.Logf("FACT repository webhook on create: action=%q repo=%q headers=%v receiver_status=%d", action, repo, headerNames(d.Headers), d.Status)
	before = len(f.fwd.deliveries())
	in.must(t, token(f.tok), http.MethodDelete, "/repos/"+f.Path("watched"), nil, nil)
	// Soft wait: whether a delete is announced at all is the question.
	deadline := time.Now().Add(15 * time.Second)
	found := false
	for !found && time.Now().Before(deadline) {
		for _, d := range f.fwd.deliveries()[before:] {
			if d.event() == "repository" {
				_, repo, action = d.payload()
				t.Logf("FACT repository webhook on delete: action=%q repo=%q receiver_status=%d", action, repo, d.Status)
				found = true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !found {
		var seen []string
		for _, d := range f.fwd.deliveries()[before:] {
			seen = append(seen, d.event())
		}
		t.Logf("FACT no repository webhook within 15s of deleting the repository; deliveries: %v", seen)
	}
	for _, e := range sink.events() {
		t.Logf("FACT receiver event kind=%v repos=%v", e.Kind, e.Repos)
	}
}
