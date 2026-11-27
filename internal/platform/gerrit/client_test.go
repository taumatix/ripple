package gerrit_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

func newClient(f *fakeGerrit) *gerrit.Client {
	return &gerrit.Client{BaseURL: f.URL, WorkDir: f.t.TempDir(),
		Credentials: platform.StaticCredential{Username: f.User, Secret: f.Token}}
}

var ctx = context.Background()

// user-changeid.md: "I" followed by the 40 hex digits of a SHA-1.
func TestChangeIDIsStablePerBranchAndWellFormed(t *testing.T) {
	re := regexp.MustCompile(`^I[0-9a-f]{40}$`)
	a, b := gerrit.ChangeID("ripple/a"), gerrit.ChangeID("ripple/b")
	if !re.MatchString(a) || !re.MatchString(b) {
		t.Fatalf("malformed Change-Ids %q %q", a, b)
	}
	if a == b || a != gerrit.ChangeID("ripple/a") {
		t.Errorf("Change-Id must differ per branch and be stable: %q %q", a, b)
	}
}

// checkoutAndPush clones project, writes a file and proposes it as branch.
func checkoutAndPush(t *testing.T, c *gerrit.Client, project, branch, file, content string) (bool, error) {
	t.Helper()
	ws, err := c.Checkout(ctx, model.Repo{FullName: project, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteFile(file, []byte(content)); err != nil {
		t.Fatal(err)
	}
	return ws.CommitAndPush(ctx, branch, "chore(deps): bump example.com/lib")
}

// ROADMAP 291: Gerrit's refusal names the permission but not the setting that fixes it; the error says both.
func TestForgeAuthorRefusalNamesTheSettingThatFixesIt(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	f.RequireEmail("acme/app", "registered@example.com")
	c := newClient(f)
	_, err := checkoutAndPush(t, c, "acme/app", "ripple/lib-v1", "go.mod", "module x\n\nrequire a v1\n")
	if err == nil {
		t.Fatal("push accepted")
	}
	for _, want := range []string{"forge author", "ripple@users.noreply.invalid", "RIPPLE_GERRIT_AUTHOR_EMAIL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	c.AuthorEmail = "registered@example.com"
	if ok, err := checkoutAndPush(t, c, "acme/app", "ripple/lib-v1", "go.mod", "module x\n\nrequire a v1\n"); err != nil || !ok {
		t.Fatalf("a registered email must be accepted: %v %v", ok, err)
	}
}

// user-upload.md ("Topic"): HEAD:refs/for/<branch>%topic=<topic>; user-changeid.md:
// the Change-Id footer; a second push with the same Change-Id is a new patch set.
func TestProposalPushesToRefsForWithTopicAndChangeID(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	c := newClient(f)
	if ok, err := checkoutAndPush(t, c, "acme/app", "ripple/lib-v1", "go.mod", "module x\n\nrequire a v1\n"); err != nil || !ok {
		t.Fatalf("push: %v %v", ok, err)
	}
	chs := f.Changes("acme/app")
	if len(chs) != 1 {
		t.Fatalf("changes: %+v", chs)
	}
	ch := chs[0]
	if ch.Topic != "ripple/lib-v1" || ch.Branch != "main" || ch.ChangeID != gerrit.ChangeID("ripple/lib-v1") || len(ch.Revisions) != 1 {
		t.Errorf("change %+v", ch)
	}
	msg := strings.TrimSpace(git(t, f.bare("acme/app"), "log", "-1", "--format=%B", ch.Revisions[0]))
	if want := "chore(deps): bump example.com/lib\n\nChange-Id: " + gerrit.ChangeID("ripple/lib-v1"); msg != want {
		t.Errorf("commit message %q, want %q", msg, want)
	}
	if out := git(t, f.bare("acme/app"), "branch", "--list"); strings.TrimSpace(out) != "* main" && strings.TrimSpace(out) != "main" {
		t.Errorf("a branch was created on the server: %q", out)
	}
	// The same branch again, with different content: a new patch set, not a second change.
	if ok, err := checkoutAndPush(t, c, "acme/app", "ripple/lib-v1", "go.mod", "module x\n\nrequire a v2\n"); err != nil || !ok {
		t.Fatalf("second push: %v %v", ok, err)
	}
	chs = f.Changes("acme/app")
	if len(chs) != 1 || len(chs[0].Revisions) != 2 {
		t.Fatalf("after the second push: %+v", chs)
	}
	if got, _ := f.FileAt("acme/app", "ripple/lib-v1", "go.mod"); !strings.Contains(got, "a v2") {
		t.Errorf("current patch set go.mod: %q", got)
	}
}

func TestProposalRefusesBranchesThatCarryPushOptions(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	for _, b := range []string{"ripple/x%wip", "ripple/x,y", "ripple/x=1", "ripple/ x"} {
		if _, err := checkoutAndPush(t, newClient(f), "acme/app", b, "a.txt", "a\n"); err == nil {
			t.Errorf("branch %q was pushed", b)
		}
	}
	if n := len(f.Changes("acme/app")); n != 0 {
		t.Errorf("%d changes created", n)
	}
}

// The fake refuses a direct push to a branch the way Gerrit does by default, which is
// what makes the proposal shape necessary; this proves the default shape is unchanged
// for every other host by running it against the same server.
func TestDefaultShapeIsRefusedByGerritSoProposalIsNeeded(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	ws, err := gitws.Checkout(ctx, gitws.Options{GitBase: f.URL, WorkDir: t.TempDir()}, f.URL+"/a/acme/app", "main",
		func(context.Context) (platform.Credential, error) {
			return platform.Credential{Username: f.User, Secret: f.Token}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.WriteFile("a.txt", []byte("a\n"))
	if _, err := ws.CommitAndPush(ctx, "ripple/direct", "msg"); err == nil || !strings.Contains(err.Error(), "prohibited by Gerrit") {
		t.Errorf("direct push: %v, want a refusal from the server", err)
	}
}

func seed(f *fakeGerrit, project string, n int, topic func(i int) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 1; i <= n; i++ {
		f.changes = append(f.changes, &fakeChange{Number: len(f.changes) + 1, Project: project, Branch: "main", Status: "NEW",
			ChangeID: fmt.Sprintf("I%040d", len(f.changes)+1), Topic: topic(i)})
	}
}

// rest-api-changes.md ("Query Changes"): n limits, S skips, and the last entry of a
// truncated page carries _more_changes.
func TestListOpenPRsPagesAndFiltersByTopicPrefix(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"a": "a"})
	f.pageCap = 2 // the server's internal limit is below the 100 asked for
	seed(f, "acme/app", 5, func(i int) string {
		if i%2 == 0 {
			return fmt.Sprintf("human-topic-%d", i)
		}
		return fmt.Sprintf("ripple/lib-%d", i)
	})
	seed(f, "acme/app", 1, func(int) string { return "" }) // no topic at all
	f.mu.Lock()
	f.changes[0].Status = "MERGED"
	f.mu.Unlock()
	prs, err := newClient(f).ListOpenPRs(ctx, model.Repo{FullName: "acme/app"}, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	var heads []string
	for _, p := range prs {
		heads = append(heads, p.Head)
	}
	sort.Strings(heads)
	if want := []string{"ripple/lib-3", "ripple/lib-5"}; !reflect.DeepEqual(heads, want) {
		t.Errorf("heads %v, want %v", heads, want)
	}
	var queries []string
	for _, r := range f.Requests() {
		queries = append(queries, r.Query)
	}
	if len(queries) < 3 || !strings.Contains(queries[0], "q=status%3Aopen+project%3Aacme%2Fapp") || !strings.Contains(queries[0], "n=100") ||
		!strings.Contains(queries[0], "S=0") || !strings.Contains(queries[1], "S=2") {
		t.Errorf("queries %q", queries)
	}
	if prs[0].URL == "" || prs[0].Number == 0 || !strings.HasSuffix(prs[0].URL, fmt.Sprintf("/c/acme/app/+/%d", prs[0].Number)) {
		t.Errorf("pr %+v", prs[0])
	}
}

func TestCreatePRSetsWIPMessageAndHashtags(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	c := newClient(f)
	if _, err := checkoutAndPush(t, c, "acme/app", "ripple/lib-v1", "a.txt", "a\n"); err != nil {
		t.Fatal(err)
	}
	pr, err := c.CreatePR(ctx, model.Repo{FullName: "acme/app"}, platform.PRSpec{Head: "ripple/lib-v1", Base: "main", Title: "t", Body: "why this bump", Draft: true, Labels: []string{"dependencies", "go"}})
	if err != nil {
		t.Fatal(err)
	}
	ch := f.Changes("acme/app")[0]
	if pr.Number != ch.Number || pr.Head != "ripple/lib-v1" || pr.ID == "" {
		t.Errorf("pr %+v, change %+v", pr, ch)
	}
	if !ch.WIP || ch.WIPPosts != 1 {
		t.Errorf("draft change is not work in progress: %+v", ch)
	}
	if !reflect.DeepEqual(ch.Messages, []string{"why this bump"}) || ch.MessageTags[0] != "autogenerated:ripple" {
		t.Errorf("messages %q tags %q", ch.Messages, ch.MessageTags)
	}
	if !reflect.DeepEqual(ch.Hashtags, []string{"dependencies", "go"}) {
		t.Errorf("hashtags %v", ch.Hashtags)
	}
}

func TestCreatePRNotDraftIsNotWIPAndUnlabelledSkipsHashtags(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	c := newClient(f)
	if _, err := checkoutAndPush(t, c, "acme/app", "ripple/b", "a.txt", "a\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreatePR(ctx, model.Repo{FullName: "acme/app"}, platform.PRSpec{Head: "ripple/b", Base: "main", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if ch := f.Changes("acme/app")[0]; ch.WIP || ch.WIPPosts != 0 || ch.HashtagsPosted != 0 {
		t.Errorf("change %+v", ch)
	}
}

func TestCreatePRWithoutAPushedChangeFails(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	if _, err := newClient(f).CreatePR(ctx, model.Repo{FullName: "acme/app"}, platform.PRSpec{Head: "ripple/none", Base: "main"}); err == nil {
		t.Fatal("no error for a branch that was never pushed")
	}
}

// rest-api.md ("429 Too Many Requests"): no Retry-After is documented, so a minute is
// assumed; one that is sent is honoured.
func TestRateLimitAnswersUseRetryAfterOrAMinute(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for header, want := range map[string]time.Duration{"7": 7 * time.Second, "": time.Minute, "soon": time.Minute} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				w.Header().Set("Retry-After", header)
			}
			http.Error(w, "Too Many Requests", 429)
		}))
		c := &gerrit.Client{BaseURL: srv.URL, Now: func() time.Time { return now }, Credentials: platform.StaticCredential{Username: "u", Secret: "p"}}
		_, err := c.ListOpenPRs(ctx, model.Repo{FullName: "acme/app"}, "")
		srv.Close()
		var rl *platform.RateLimitError
		if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(want)) {
			t.Errorf("Retry-After %q: err %v, want until +%v", header, err, want)
		}
		if got := c.RateLimitedUntil(model.Repo{}); !got.Equal(now.Add(want)) {
			t.Errorf("RateLimitedUntil %v", got)
		}
	}
}

// rest-api-projects.md ("List Projects"): the literal example answer, with the magic
// prefix line, a map of project name to ProjectInfo.
func TestListReposParsesTheDocumentedAnswer(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		u, p, _ := r.BasicAuth()
		if u != "bot" || p != "pw" {
			http.Error(w, "no", 401)
			return
		}
		_, _ = w.Write([]byte(")]}'\n" + `{
  "external/bison": {"id": "external%2Fbison", "description": "GNU parser generator"},
  "external/gcc": {"id": "external%2Fgcc"},
  "external/openssl": {"id": "external%2Fopenssl", "description": "encryption\ncrypto routines"},
  "test": {"id": "test", "description": "<html> is escaped"},
  "weird name": {"id": "weird%20name"},
  "../up": {"id": "..%2Fup"}
}`))
	}))
	defer srv.Close()
	c := &gerrit.Client{BaseURL: srv.URL, ProjectPrefix: "ext", Credentials: platform.StaticCredential{Username: "bot", Secret: "pw"}}
	repos, err := c.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
	}
	if want := []string{"external/bison", "external/gcc", "external/openssl", "test"}; !reflect.DeepEqual(names, want) {
		t.Errorf("projects %v, want %v (unsafe names are skipped)", names, want)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "/a/projects/?") || !strings.Contains(seen[0], "type=CODE") || !strings.Contains(seen[0], "state=ACTIVE") || !strings.Contains(seen[0], "p=ext") {
		t.Errorf("requests %q", seen)
	}
}

func TestListReposPagesWithMoreProjects(t *testing.T) {
	f := newFakeGerrit(t)
	f.pageCap = 2
	for _, p := range []string{"a/one", "a/two", "a/three", "a/four", "a/five"} {
		f.AddRepo(p, map[string]string{"x": "x"})
	}
	repos, err := newClient(f).ListRepos(ctx)
	if err != nil || len(repos) != 5 {
		t.Fatalf("%v %v", repos, err)
	}
}

// A project whose files the fake reads from real commits: PushFiles walks first
// parents, merges the file lists, skips the /COMMIT_MSG magic entry (rest-api-changes.md,
// "List Files") and reports a rename's old path.
func TestPushFilesWalksFirstParents(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n", "old.txt": "o\n"})
	base := f.rev("acme/app", "main")
	f.Commit("acme/app", map[string]string{"a/b.go": "package a\n"})
	f.Commit("acme/app", map[string]string{"go.mod": "module x\n// changed\n"})
	head := f.rev("acme/app", "main")
	c := newClient(f)
	files, err := c.PushFiles(ctx, "acme/app", base, head)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if want := []string{"a/b.go", "go.mod"}; !reflect.DeepEqual(files, want) {
		t.Errorf("files %v, want %v", files, want)
	}
	// A new branch (zero oldRev) and an unreachable oldRev are re-scans.
	for _, old := range []string{strings.Repeat("0", 40), strings.Repeat("a", 40)} {
		if files, err := c.PushFiles(ctx, "acme/app", old, head); err != nil && !strings.Contains(err.Error(), "status 404") || files != nil {
			t.Errorf("oldRev %s: %v %v, want a re-scan", old[:4], files, err)
		}
	}
}

func TestPushFilesReportsRenameSourceAndSkipsMerges(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"old.txt": "some content that is long enough to be a rename\n"})
	base := f.rev("acme/app", "main")
	work := t.TempDir()
	git(t, work, "clone", "-q", f.bare("acme/app"), "w")
	w := filepath.Join(work, "w")
	git(t, w, "mv", "old.txt", "new.txt")
	git(t, w, "commit", "-q", "-am", "rename")
	git(t, w, "push", "-q", "origin", "HEAD:main")
	renamed := f.rev("acme/app", "main")
	c := newClient(f)
	files, err := c.PushFiles(ctx, "acme/app", base, renamed)
	sort.Strings(files)
	if err != nil || !reflect.DeepEqual(files, []string{"new.txt", "old.txt"}) {
		t.Errorf("files %v, err %v", files, err)
	}
	// A merge commit has no single parent to diff against: re-scan.
	git(t, w, "checkout", "-q", "-b", "side", base)
	if err := os.WriteFile(filepath.Join(w, "side.txt"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w, "add", "-A")
	git(t, w, "commit", "-q", "-m", "side")
	git(t, w, "checkout", "-q", "main")
	git(t, w, "merge", "-q", "--no-ff", "-m", "merge", "side")
	git(t, w, "push", "-q", "origin", "HEAD:main")
	if files, err := c.PushFiles(ctx, "acme/app", renamed, f.rev("acme/app", "main")); err != nil || files != nil {
		t.Errorf("merge: %v %v, want a re-scan", files, err)
	}
}

func TestEveryRequestCarriesBasicCredentialsAndNoneLeaksIntoURLs(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	c := newClient(f)
	if _, err := c.ListOpenPRs(ctx, model.Repo{FullName: "acme/app"}, "ripple/"); err != nil {
		t.Fatal(err)
	}
	c.Credentials = platform.StaticCredential{Username: f.User, Secret: "wrong"}
	if _, err := c.ListOpenPRs(ctx, model.Repo{FullName: "acme/app"}, "ripple/"); err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Errorf("wrong password: %v", err)
	}
	for _, r := range f.Requests() {
		if strings.Contains(r.Path+r.Query, f.Token) {
			t.Errorf("credential in a URL: %s?%s", r.Path, r.Query)
		}
		if !strings.HasPrefix(r.Path, "/a/") {
			t.Errorf("request outside the authenticated /a/ prefix: %s", r.Path)
		}
	}
}

func TestUnsafeProjectNamesAreRefusedBeforeAnyRequest(t *testing.T) {
	f := newFakeGerrit(t)
	c := newClient(f)
	for _, name := range []string{"", "../x", "a/../b", "a b", "a?b", "/a", "a//b", "a%2fb", "-a"} {
		r := model.Repo{FullName: name}
		if _, err := c.ListOpenPRs(ctx, r, ""); err == nil {
			t.Errorf("%q: ListOpenPRs accepted", name)
		}
		if _, err := c.CreatePR(ctx, r, platform.PRSpec{}); err == nil {
			t.Errorf("%q: CreatePR accepted", name)
		}
		if _, err := c.Checkout(ctx, r); err == nil {
			t.Errorf("%q: Checkout accepted", name)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestGerritHasNoAutoMerge(t *testing.T) {
	var p platform.Platform = &gerrit.Client{}
	if err := platform.RequestAutoMerge(ctx, p, model.Repo{}, platform.PR{}); !errors.Is(err, platform.ErrNoAutoMerge) {
		t.Errorf("RequestAutoMerge: %v, want ErrNoAutoMerge", err)
	}
}
