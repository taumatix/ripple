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

	"github.com/taumatix/ripple/internal/model"
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
	// These tests call the API directly, as no ripple would: a pull request cannot be created
	// while the instance still calls the repository empty (404). The conformance suite does not wait.
	f.settled(t, f.Path("facts"))
	return f
}

// TestRealRequestAutoMerge drives ripple's own RequestAutoMerge right after the pull request is
// created, as the engine does, with no wait for the instance's mergeability check: it must end
// with the auto-merge scheduled, and a repeat must end in the instance's 409, not in a 405.
func TestRealRequestAutoMerge(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	c := &gitea.Client{APIBase: in.url, GitBase: in.url, Flavour: in.flavour,
		Credentials: platform.StaticCredential{Username: "ripple", Secret: f.tok}}
	repo := model.Repo{FullName: path, DefaultBranch: "main"}
	n := f.branchPR(t, "facts", "ripple/go/auto")
	start := time.Now()
	if err := c.RequestAutoMerge(context.Background(), repo, platform.PR{Number: n}); err != nil {
		t.Fatalf("first RequestAutoMerge: %v", err)
	}
	t.Logf("FACT RequestAutoMerge right after creating the pull request succeeded in %v", time.Since(start))
	err := c.RequestAutoMerge(context.Background(), repo, platform.PR{Number: n})
	t.Logf("FACT second RequestAutoMerge: %v", err)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("second RequestAutoMerge: %v, want the instance's 409", err)
	}
}

// mergeRequest sends one merge request and, while the instance answers 405
// {"message":"Please try again later"}, sends it again. Gitea 1.27.3 answers that while the
// pull request's mergeability check (an asynchronous queue job) is pending, and nothing the API
// shows says so beforehand: CI run 46019416863 read `mergeable: true` on GET pulls/{n} after
// 4 reads and still got the 405 on the next merge request, which was then accepted (201). The
// 405 is the only signal, so it is the one waited for; every other answer is returned as is.
// The retries are logged so a run shows how often it happened.
func (f *realFixture) mergeRequest(t *testing.T, path string, n int, body map[string]any, when string) (int, []byte) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for refused := 0; ; refused++ {
		st, _, resp := f.in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/merge", path, n), body, nil)
		if st != http.StatusMethodNotAllowed {
			if refused > 0 {
				t.Logf("FACT %s#%d %s: %d x 405 %q before %d", path, n, when, refused, strings.TrimSpace(string(resp)), st)
			}
			return st, resp
		}
		if !strings.Contains(string(resp), "Please try again later") || time.Now().After(deadline) {
			t.Fatalf("%s#%d %s: 405 %q after %d refusals", path, n, when, resp, refused+1)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestRealFirstDiscoveryOfAFreshRepository is ROADMAP 280 on the real instance: a repository is
// listed straight after its first push, while the instance may still call it empty, and must be
// in that listing (nothing lists it again until the next discovery).
func TestRealFirstDiscoveryOfAFreshRepository(t *testing.T) {
	in := realInstance(t)
	f := &realFixture{in: in, dirs: map[string]string{}, hooked: map[string]bool{}, cur: t}
	f.user, f.tok = in.newUser(t)
	f.AddRepo(t, f.Path("fresh"), map[string]string{"README.md": "fresh\n"}) // no settle
	c := &gitea.Client{APIBase: in.url, GitBase: in.url, Credentials: platform.StaticCredential{Username: "ripple", Secret: f.tok}}
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if strings.EqualFold(r.FullName, f.Path("fresh")) {
			return
		}
	}
	t.Fatalf("listing straight after the first push lacks %s: %v", f.Path("fresh"), repos)
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
	// Gitea 1.27.3, Forgejo 17.0.2 and 7.0.16 never throttle: no 429 and no rate-limit header, which
	// is why the conformance fixture injects its own 429 (docs/platform-gitea.md).
	if statuses[200] != 600 || len(rl) != 0 {
		t.Errorf("the instance now rate-limits (statuses %v, headers %v); the fixture's injected 429 is no longer the only source", statuses, rl)
	}
}

// TestRealScheduledMerge: the status of "merge when checks succeed", the body key case on each
// flavour, and what the pull request looks like afterwards (ROADMAP 12).
func TestRealScheduledMerge(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	for i, key := range []string{"do", "Do"} {
		n := f.branchPR(t, "facts", fmt.Sprintf("ripple/go/m%d", i))
		st, body := f.mergeRequest(t, path, n, map[string]any{key: "squash", "merge_when_checks_succeed": true}, "first request")
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
		st2, body2 := f.mergeRequest(t, path, n, map[string]any{key: "squash", "merge_when_checks_succeed": true}, "second request")
		t.Logf("FACT merge key=%q: status %d body %q; pr state=%s merged=%v; timeline %v; second request %d %q",
			key, st, strings.TrimSpace(string(body)), pr.State, pr.Merged, types, st2, strings.TrimSpace(string(body2)))
		// Literal answers of Gitea 1.27.3, Forgejo 17.0.2 and 7.0.16, either key case.
		if st != http.StatusCreated || pr.State != "open" || pr.Merged || strings.Join(types, ",") != "pull_push,pull_scheduled_merge" ||
			st2 != http.StatusConflict || !strings.Contains(string(body2), "already scheduled to auto merge") {
			t.Errorf("scheduled merge with key %q: first %d, state %s merged %v, timeline %v, second %d %q", key, st, pr.State, pr.Merged, types, st2, body2)
		}
	}
}

// TestRealLabelByName: whether a label that does not exist yet can be put on a pull request by
// name, and by name once it exists (ROADMAP 12, "labels by name on create").
func TestRealLabelByName(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	n := f.branchPR(t, "facts", "ripple/go/label")
	var v struct {
		Version string `json:"version"`
	}
	in.must(t, nil, http.MethodGet, "/version", nil, &v)
	// Forgejo 7.0.16 sits on Gitea 1.21, whose IssueLabelsOption.Labels is []int64: names are a 422.
	idsOnly := strings.HasPrefix(v.Version, "7.")
	st, _, body := in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", path, n), map[string]any{"labels": []string{"dependencies"}}, nil)
	t.Logf("FACT label by name, label absent: %d %q (version %s)", st, strings.TrimSpace(string(body)), v.Version)
	if idsOnly {
		if st != http.StatusUnprocessableEntity || !strings.Contains(string(body), "int64") {
			t.Errorf("a label by name on %s: %d %q, want 422 naming int64", v.Version, st, body)
		}
	} else if st != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("an absent label by name: %d %q, want 200 []", st, body)
	}
	var made labelJSON
	in.must(t, token(f.tok), http.MethodPost, "/repos/"+path+"/labels", map[string]any{"name": "dependencies", "color": "#0075ca"}, &made)
	var labels any = []string{"dependencies"}
	if idsOnly {
		labels = []int64{made.ID}
	}
	st, _, body = in.call(t, token(f.tok), http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", path, n), map[string]any{"labels": labels}, nil)
	t.Logf("FACT label (by id on 7.x), label exists: %d %q", st, strings.TrimSpace(string(body)))
	if st != http.StatusOK || !strings.Contains(string(body), `"name":"dependencies"`) {
		t.Errorf("an existing label: %d %q, want 200 listing it", st, body)
	}
}

type labelJSON struct {
	ID int64 `json:"id"`
}

// TestRealCreatePRLabels drives Client.CreatePR with one label the repository has and one it lacks,
// whichever way the instance takes labels (names, or ids on Forgejo 7), and reads the pull request back.
func TestRealCreatePRLabels(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	dir := f.dirs[path]
	f.git(t, dir, "checkout", "-q", "-b", "ripple/go/lbl")
	writeFiles(t, dir, map[string]string{"lbl.txt": "x\n"})
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "lbl")
	f.git(t, dir, "push", "-q", in.url+"/"+path+".git", "ripple/go/lbl")
	in.must(t, token(f.tok), http.MethodPost, "/repos/"+path+"/labels", map[string]any{"name": "dependencies", "color": "#0075ca"}, nil)
	c := &gitea.Client{APIBase: in.url, GitBase: in.url, Credentials: platform.StaticCredential{Username: "ripple", Secret: f.tok}}
	pr, err := c.CreatePR(context.Background(), model.Repo{FullName: path, DefaultBranch: "main"}, platform.PRSpec{
		Head: "ripple/go/lbl", Base: "main", Title: "t", Labels: []string{"dependencies", "nosuchlabel"}})
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Name string `json:"name"`
	}
	in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/labels", path, pr.Number), nil, &got)
	t.Logf("FACT labels on the pull request after CreatePR: %v", got)
	if len(got) != 1 || got[0].Name != "dependencies" {
		t.Errorf("labels on the pull request = %v, want only dependencies", got)
	}
}

// TestRealGitBasicAuthUser: which basic-auth user names git accepts with the token as the
// password (ROADMAP 12, "the git basic-auth user").
func TestRealGitBasicAuthUser(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	// The repository is private, so "accepted" proves the token authenticated. Any user name works
	// (Gitea 1.27.3, Forgejo 17.0.2 and 7.0.16), which is what Client's "ripple" relies on.
	for _, u := range []string{"ripple", "oauth2", "x-access-token", "", f.user} {
		got := lsRemote(in, f, url.UserPassword(u, f.tok))
		t.Logf("FACT git ls-remote as user %q with the token as password: %s", u, got)
		if !strings.HasPrefix(got, "accepted") {
			t.Errorf("user %q with the token: %s", u, got)
		}
	}
	t.Logf("FACT git ls-remote with the token as the user and no password: %s", lsRemote(in, f, url.UserPassword(f.tok, "")))
	wrong := lsRemote(in, f, url.UserPassword("ripple", "nottoken"))
	t.Logf("FACT git ls-remote with a wrong token: %s", wrong)
	if !strings.HasPrefix(wrong, "refused") {
		t.Errorf("a wrong token was accepted: %s", wrong)
	}
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
	// Gitea 1.27.3, Forgejo 17.0.2 and 7.0.16 announce a created repository and never a deleted one,
	// so Discover (not the webhook) is what notices a deletion.
	if action != "created" && found {
		t.Errorf("a delete is now announced (action %q): the Receiver's repos_removed path can be proven for real", action)
	}
	for _, e := range sink.events() {
		t.Logf("FACT receiver event kind=%v repos=%v", e.Kind, e.Repos)
	}
}
