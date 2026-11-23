package testutil

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorkflowDenyMessage is what the fake's pre-receive hook prints when a push
// touches .github/workflows. It follows the wording GitHub uses for an App token
// without the workflows permission; ripple matches it loosely, since GitHub's docs
// do not state it.
const WorkflowDenyMessage = "refusing to allow a GitHub App to create or update workflow `%s` without `workflows` permission"

type refState struct {
	releases map[string]map[string]time.Time // repo -> tag -> published_at
}

func (g *GitHub) rs() *refState {
	if g.refState == nil {
		g.refState = &refState{releases: map[string]map[string]time.Time{}}
	}
	return g.refState
}

// Tag tags the head of main with a lightweight tag and returns the commit.
func (g *GitHub) Tag(repo, name string) string {
	g.t.Helper()
	git(g.t, g.bare(repo), "tag", "-f", name, "main")
	return strings.TrimSpace(git(g.t, g.bare(repo), "rev-parse", name+"^{commit}"))
}

// AnnotatedTag tags the head of main with an annotated tag object and returns the
// commit (the tag object's own id is what the refs API reports first).
func (g *GitHub) AnnotatedTag(repo, name string) string {
	g.t.Helper()
	git(g.t, g.bare(repo), "tag", "-f", "-a", "-m", name, name, "main")
	return strings.TrimSpace(git(g.t, g.bare(repo), "rev-parse", name+"^{commit}"))
}

// Release publishes a GitHub release for tag at the given time.
func (g *GitHub) Release(repo, tag string, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rs().releases[repo] == nil {
		g.rs().releases[repo] = map[string]time.Time{}
	}
	g.rs().releases[repo][tag] = at
}

// DenyWorkflowPush makes repo refuse any push that changes a file under
// .github/workflows, as GitHub does for an App token without the workflows permission.
func (g *GitHub) DenyWorkflowPush(repo string) {
	g.t.Helper()
	hooks := filepath.Join(g.bare(repo), "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		g.t.Fatal(err)
	}
	script := `#!/bin/sh
zero=0000000000000000000000000000000000000000
while read old new ref; do
  [ "$new" = "$zero" ] && continue
  base=$old
  [ "$old" = "$zero" ] && base=$(git rev-parse --verify -q refs/heads/main)
  for f in $(git diff --name-only "$base" "$new" 2>/dev/null | grep '^\.github/workflows/'); do
    printf 'refusing to allow a GitHub App to create or update workflow ` + "`%s`" + ` without ` + "`workflows`" + ` permission\n' "$f" >&2
    exit 1
  done
done
`
	if err := os.WriteFile(filepath.Join(hooks, "pre-receive"), []byte(script), 0o755); err != nil {
		g.t.Fatal(err)
	}
}

// AllowWorkflowPush removes what DenyWorkflowPush installed.
func (g *GitHub) AllowWorkflowPush(repo string) {
	_ = os.Remove(filepath.Join(g.bare(repo), "hooks", "pre-receive"))
}

// handleRefs serves /tags, /git/ref/tags/{tag}, /git/tags/{sha} and
// /releases/tags/{tag}; parts is the path after /repos/ split on "/".
func (g *GitHub) handleRefs(w http.ResponseWriter, r *http.Request, repo string, parts []string) {
	if r.Method != http.MethodGet {
		g.fail(w, http.StatusMethodNotAllowed, "method")
		return
	}
	bare := g.bare(repo)
	out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	run := func(args ...string) (string, bool) {
		o, err := gitOut(bare, args...)
		return strings.TrimSpace(o), err == nil
	}
	rest := strings.Join(parts[2:], "/")
	switch {
	case rest == "tags":
		names, _ := run("tag", "--list")
		list := []map[string]any{}
		for _, n := range strings.Fields(names) {
			sha, _ := run("rev-parse", n+"^{commit}")
			list = append(list, map[string]any{"name": n, "commit": map[string]any{"sha": sha}})
		}
		out(list)
	case strings.HasPrefix(rest, "git/ref/tags/"):
		tag := strings.TrimPrefix(rest, "git/ref/tags/")
		sha, ok := run("rev-parse", "--verify", "-q", "refs/tags/"+tag)
		if !ok {
			g.fail(w, http.StatusNotFound, "Not Found")
			return
		}
		typ, _ := run("cat-file", "-t", sha)
		out(map[string]any{"ref": "refs/tags/" + tag, "object": map[string]any{"type": typ, "sha": sha}})
	case strings.HasPrefix(rest, "git/tags/"):
		sha := strings.TrimPrefix(rest, "git/tags/")
		if typ, _ := run("cat-file", "-t", sha); typ != "tag" {
			g.fail(w, http.StatusNotFound, "Not Found")
			return
		}
		target, _ := run("rev-parse", sha+"^{}")
		typ, _ := run("cat-file", "-t", target)
		out(map[string]any{"sha": sha, "object": map[string]any{"type": typ, "sha": target}})
	case strings.HasPrefix(rest, "releases/tags/"):
		tag := strings.TrimPrefix(rest, "releases/tags/")
		g.mu.Lock()
		at, ok := g.rs().releases[repo][tag]
		g.mu.Unlock()
		if !ok {
			g.fail(w, http.StatusNotFound, "Not Found")
			return
		}
		out(map[string]any{"html_url": "https://github.com/" + repo + "/releases/tag/" + tag, "tag_name": tag, "published_at": at.UTC().Format(time.RFC3339), "draft": false, "prerelease": false})
	default:
		g.fail(w, http.StatusNotFound, "unknown route %s", r.URL.Path)
	}
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	o, err := cmd.Output()
	return string(o), err
}
