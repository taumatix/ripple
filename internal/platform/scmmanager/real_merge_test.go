//go:build scmreal

package scmmanager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

// newRealRepo creates an initialised repository on the real instance and clones it into a
// temporary directory, returning the client under test, the full name, its default branch and the
// clone.
func newRealRepo(t *testing.T, s *scm, prefix string) (*scmmanager.Client, string, string, string) {
	t.Helper()
	key := s.apiKey(t)
	code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/repositories?initialize=true", "", mtRepo,
		map[string]any{"namespace": "acme", "name": prefix + "-" + randHex(3), "type": "git", "contextEntries": map[string]any{}})
	if code != http.StatusCreated {
		t.Fatalf("creating the repository: %d %s", code, clip(body))
	}
	full := strings.TrimPrefix(h.Get("Location"), s.base+"/api/v2/repositories/")
	c := &scmmanager.Client{BaseURL: s.base, Credentials: platform.StaticCredential{Secret: key}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	def, err := c.DefaultBranch(ctx, full)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s.gitReal(t, dir, "clone", "-q", s.base+"/repo/"+full, ".")
	return c, full, def, dir
}

func commitFile(t *testing.T, s *scm, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s.gitReal(t, dir, "add", "-A")
	s.gitReal(t, dir, "commit", "-q", "-m", "add "+name)
	return strings.TrimSpace(s.gitReal(t, dir, "rev-parse", "HEAD"))
}

// TestRealMergeCommitModifications pushes a true merge commit to the default branch and records what
// the modifications resource says about it (ROADMAP 284). The assertion is the one the engine
// relies on: when every pushed changeset id is given, go.mod (touched on the merged branch) is
// reported. What the merge commit's own modifications hold is logged as a FACT.
func TestRealMergeCommitModifications(t *testing.T) {
	s := realSCM(t)
	c, full, def, dir := newRealRepo(t, s, "merge")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	s.gitReal(t, dir, "checkout", "-q", "-b", "feature")
	featureID := commitFile(t, s, dir, "go.mod", "module example.invalid/m\n\ngo 1.22\n")
	s.gitReal(t, dir, "checkout", "-q", def)
	mainID := commitFile(t, s, dir, "other.txt", "other\n")
	s.gitReal(t, dir, "merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	mergeID := strings.TrimSpace(s.gitReal(t, dir, "rev-parse", "HEAD"))
	s.gitReal(t, dir, "push", "-q", "origin", def)

	alone, err := c.PushFiles(ctx, full, def, []string{mergeID})
	if err != nil {
		t.Fatalf("PushFiles with the merge commit alone: %v", err)
	}
	t.Logf("FACT modifications of the merge commit %s alone: %v (nil means the client chose to re-scan)", mergeID, alone)

	all, err := c.PushFiles(ctx, full, def, []string{mainID, featureID, mergeID})
	if err != nil {
		t.Fatalf("PushFiles with every pushed id: %v", err)
	}
	t.Logf("FACT modifications of main, feature and merge together: %v", all)
	if !contains(all, "go.mod") {
		t.Errorf("PushFiles over every pushed changeset = %v, want go.mod among them", all)
	}
	// The resource itself, trimmed, so the parent question has an answer on record.
	code, _, body := s.call(t, http.MethodGet, s.base+"/api/v2/repositories/"+full+"/modifications/"+mergeID, mtModifs, "", nil)
	t.Logf("FACT modifications resource of the merge commit: %d %s", code, clip(body))
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

const mtModifs = "application/vnd.scmm-modifications+json;v=2"

// TestRealMergeDeletesSourceBranch merges a pull request created by the client (which sets
// shouldDeleteSourceBranch) through the review plugin's merge link and reads the branch list
// afterwards (ROADMAP 285). The merge link and the body it takes are discovered, not assumed:
// each attempt is logged.
func TestRealMergeDeletesSourceBranch(t *testing.T) {
	s := realSCM(t)
	c, full, def, dir := newRealRepo(t, s, "mergedel")
	ns, name, _ := strings.Cut(full, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	s.gitReal(t, dir, "checkout", "-q", "-b", "ripple/del")
	commitFile(t, s, dir, "a.txt", "a\n")
	s.gitReal(t, dir, "push", "-q", "origin", "ripple/del")
	if _, err := c.CreatePR(ctx, model.Repo{FullName: full, DefaultBranch: def}, platform.PRSpec{Head: "ripple/del", Base: def, Title: "del", Body: "b"}); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	_, _, body := s.call(t, http.MethodGet, s.base+"/api/v2/pull-requests/"+ns+"/"+name, mtPRList, "", nil)
	var list struct {
		Embedded struct {
			PRs []struct {
				ID                       string          `json:"id"`
				ShouldDeleteSourceBranch bool            `json:"shouldDeleteSourceBranch"`
				Links                    links           `json:"_links"`
				Raw                      json.RawMessage `json:"-"`
			} `json:"pullRequests"`
		} `json:"_embedded"`
	}
	_ = json.Unmarshal(body, &list)
	if len(list.Embedded.PRs) != 1 {
		t.Fatalf("pull request list: %s", clip(body))
	}
	pr := list.Embedded.PRs[0]
	t.Logf("FACT shouldDeleteSourceBranch on the created pull request: %v; link names: %v", pr.ShouldDeleteSourceBranch, pr.Links.names())
	merge := pr.Links.href("merge")
	if merge == "" {
		t.Fatalf("the pull request has no merge link (names %v); record which link merges and rerun", pr.Links.names())
	}
	t.Logf("FACT merge link: %s", merge)
	base := strings.SplitN(merge, "{", 2)[0]
	var merged bool
	// The link carries its own ?strategy=. The merge media type is not in the vendored docs, so each
	// candidate is tried and its answer logged; the accepted one is the fact.
	body0 := map[string]any{"commitMessage": "merge", "overrideMessage": false}
	for _, ct := range []string{
		"application/vnd.scmm-mergeCommand+json;v=2",
		"application/vnd.scmm-mergeCommit+json;v=2",
		"application/vnd.scmm-mergeCommitDto+json;v=2",
		"application/vnd.scmm-merge+json;v=2",
		"application/json",
	} {
		code, _, b := s.call(t, http.MethodPost, base, "", ct, body0)
		t.Logf("FACT merge attempt %s content-type %s: %d %s", base, ct, code, clip(b))
		if code/100 == 2 {
			t.Logf("FACT merge accepted with content-type %s", ct)
			merged = true
			break
		}
	}
	if !merged {
		t.Fatal("no merge attempt was accepted; see the FACT lines for the answers and adjust the body")
	}
	var exists bool
	for _, wait := range []time.Duration{0, 2 * time.Second, 5 * time.Second} {
		time.Sleep(wait)
		code, _, b := s.call(t, http.MethodGet, s.base+"/api/v2/repositories/"+full+"/branches/ripple%2Fdel", mtBranchV2, "", nil)
		exists = code == http.StatusOK
		t.Logf("FACT after the merge (waited %s) the source branch resource answers %d %s", wait, code, clip(b))
		if !exists {
			break
		}
	}
	t.Logf("FACT source branch still present after a merge with shouldDeleteSourceBranch=%v: %v", pr.ShouldDeleteSourceBranch, exists)
	if pr.ShouldDeleteSourceBranch && exists {
		t.Errorf("shouldDeleteSourceBranch was set on the pull request but the source branch survived the merge")
	}
}
