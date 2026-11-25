package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

var _ app.HeadLister = (*gitlab.Client)(nil)

func headsFixture(t *testing.T, paths ...string) (*testutil.GitLab, func() *gitlab.Client) {
	t.Helper()
	gl := testutil.NewGitLab(t)
	gl.Token("t", "")
	for _, p := range paths {
		gl.AddRepo(p, map[string]string{"README": "x"})
		gl.Member(p)
	}
	return gl, func() *gitlab.Client {
		return &gitlab.Client{BaseURL: gl.APIURL, GitBase: gl.GitBase, Tokens: gitlab.Tokens{Default: "t"}}
	}
}

// The listing without `simple` carries default_branch (api_projects.md) and GET
// /projects/:id/repository/branches/:branch carries commit.id (api_branches.md), so one read per
// project gives its head; ListRepos names the branch it learned.
func TestListRepoHeadsReadsEachProjectsDefaultBranchCommit(t *testing.T) {
	gl, mk := headsFixture(t, "team/a", "team/b")
	c := mk()
	heads, err := c.ListRepoHeads(context.Background())
	if err != nil || len(heads) != 2 || heads["team/a"] != gl.BranchHead("team/a", "main") || heads["team/b"] != gl.BranchHead("team/b", "main") {
		t.Fatalf("heads %v err %v", heads, err)
	}
	repos, _ := c.ListRepos(context.Background())
	if len(repos) != 2 || repos[0].DefaultBranch != "main" {
		t.Fatalf("ListRepos after learning: %+v", repos)
	}
}

// A project with no default branch (empty) is left out, and a 429 on a branch read ends the
// listing with a RateLimitError so the app keeps what it remembered.
func TestListRepoHeadsLeavesOutAnEmptyProjectAndStopsOnARateLimit(t *testing.T) {
	gl, _ := headsFixture(t, "team/a")
	gl.Member("team/empty") // listed with no repository behind it: no default_branch
	c := &gitlab.Client{BaseURL: gl.APIURL, Tokens: gitlab.Tokens{Default: "t"}}
	heads, err := c.ListRepoHeads(context.Background())
	if err != nil || len(heads) != 1 || heads["team/a"] == "" {
		t.Fatalf("%v %v, want only team/a", heads, err)
	}

	target, _ := url.Parse(gl.APIURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/repository/branches/") {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer front.Close()
	limited := &gitlab.Client{BaseURL: front.URL, Tokens: gitlab.Tokens{Default: "t"}}
	var rl *platform.RateLimitError
	if _, err := limited.ListRepoHeads(context.Background()); !errors.As(err, &rl) {
		t.Fatalf("error %v is not a RateLimitError", err)
	}
}

// Driven through App.Discover against the fake GitLab: a project whose default branch moved while
// ripple was down is re-scanned once on the first round of a new process, an unchanged one is not.
func TestDiscoverRescansAGitLabProjectWhoseHeadMovedOnce(t *testing.T) {
	ctx := context.Background()
	gl, mk := headsFixture(t, "team/moves", "team/still")
	st := store.NewMemory()
	newApp := func(c *gitlab.Client) *app.App {
		return &app.App{Hosts: []*platform.Host{{Name: "gl", Platform: c}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
	}
	pushes := func() []model.Event {
		evs, err := st.ListEvents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []model.Event
		for _, e := range evs {
			if e.Kind == "push" {
				out = append(out, e)
			}
		}
		return out
	}
	newApp(mk()).Discover(ctx)
	newApp(mk()).Discover(ctx)
	if got := pushes(); len(got) != 0 {
		t.Fatalf("unchanged heads queued %v", got)
	}
	gl.Commit("team/moves", map[string]string{"README": "y"})
	newApp(mk()).Discover(ctx) // a restart: nothing learned yet
	got := pushes()
	if len(got) != 1 || got[0].Repo != "gl:team/moves" || got[0].Ref != "refs/heads/main" {
		t.Fatalf("a moved head queued %+v, want one push for gl:team/moves", got)
	}
	newApp(mk()).Discover(ctx)
	if got := pushes(); len(got) != 1 {
		t.Fatalf("the same head queued again: %v", got)
	}
}
