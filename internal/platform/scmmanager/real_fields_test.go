//go:build scmreal

package scmmanager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

const mtBranchV2 = "application/vnd.scmm-branch+json;v=2"

// keysOf is the sorted keys of a JSON object, for a FACT line that names a resource's fields.
func keysOf(raw []byte) []string {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	var k []string
	for name := range m {
		k = append(k, name)
	}
	sort.Strings(k)
	return k
}

// TestRealPullRequestAndBranchFields records the fields of the open pull request, in the list and
// on its own resource, and of the branch resource, so that a head commit on the pull request
// (ROADMAP 187) and the branch revision (ROADMAP 243) rest on an answer and not on memory.
func TestRealPullRequestAndBranchFields(t *testing.T) {
	s := realSCM(t)
	key := s.apiKey(t)
	suffix := randHex(3)
	code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/repositories?initialize=true", "", mtRepo,
		map[string]any{"namespace": "acme", "name": "fields-" + suffix, "type": "git", "contextEntries": map[string]any{}})
	if code != http.StatusCreated {
		t.Fatalf("creating the repository: %d %s", code, clip(body))
	}
	full := strings.TrimPrefix(h.Get("Location"), s.base+"/api/v2/repositories/")
	ns, name, _ := strings.Cut(full, "/")
	c := &scmmanager.Client{BaseURL: s.base, Credentials: platform.StaticCredential{Secret: key}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	def, err := c.DefaultBranch(ctx, full)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	s.gitReal(t, dir, "clone", "-q", s.base+"/repo/"+full, ".")
	s.gitReal(t, dir, "checkout", "-q", "-b", "ripple/fields")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.gitReal(t, dir, "add", "-A")
	s.gitReal(t, dir, "commit", "-q", "-m", "fields")
	s.gitReal(t, dir, "push", "-q", "origin", "ripple/fields")
	sourceHead := strings.TrimSpace(s.gitReal(t, dir, "rev-parse", "HEAD"))
	repo := model.Repo{FullName: full, DefaultBranch: def}
	if _, err := c.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/fields", Base: def, Title: "fields", Body: "b"}); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/pull-requests/"+ns+"/"+name, mtPRList, "", nil)
	var list struct {
		Embedded struct {
			PRs []json.RawMessage `json:"pullRequests"`
		} `json:"_embedded"`
	}
	_ = json.Unmarshal(body, &list)
	if code != http.StatusOK || len(list.Embedded.PRs) != 1 {
		t.Fatalf("pull request list: %d %s", code, clip(body))
	}
	item := list.Embedded.PRs[0]
	t.Logf("FACT pull request list item fields: %v", keysOf(item))
	t.Logf("FACT pull request list item (trimmed): %s", clip(item))
	t.Logf("FACT source branch head pushed: %s", sourceHead)
	t.Logf("FACT the list item contains the source head: %v", strings.Contains(string(item), sourceHead))
	var one struct {
		Links links `json:"_links"`
	}
	_ = json.Unmarshal(item, &one)
	if self := one.Links.href("self"); self != "" {
		code, _, body = s.call(t, http.MethodGet, self, "", "", nil)
		t.Logf("FACT pull request resource: %d fields %v contains the source head: %v", code, keysOf(body), strings.Contains(string(body), sourceHead))
		t.Logf("FACT pull request resource (trimmed): %s", clip(body))
	}

	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/repositories/"+full+"/branches/"+"ripple%2Ffields", mtBranchV2, "", nil)
	t.Logf("FACT branch resource ripple/fields: %d %s", code, clip(body))
	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/repositories/"+full+"/branches/"+def, mtBranchV2, "", nil)
	t.Logf("FACT branch resource %s: %d %s", def, code, clip(body))
	var br struct {
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(body, &br)
	if code != http.StatusOK || len(br.Revision) != 40 {
		t.Errorf("branch resource of the default branch: %d revision %q, want 200 and 40 hex", code, br.Revision)
	}
}

// TestRealArchivedListing records how an archived repository appears in the repository listing,
// with and without showArchived, and what the client reports (ROADMAP 213).
func TestRealArchivedListing(t *testing.T) {
	s := realSCM(t)
	key := s.apiKey(t)
	suffix := randHex(3)
	code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/repositories?initialize=true", "", mtRepo,
		map[string]any{"namespace": "acme", "name": "arch-" + suffix, "type": "git", "contextEntries": map[string]any{}})
	if code != http.StatusCreated {
		t.Fatalf("creating the repository: %d %s", code, clip(body))
	}
	full := strings.TrimPrefix(h.Get("Location"), s.base+"/api/v2/repositories/")
	c := &scmmanager.Client{BaseURL: s.base, Credentials: platform.StaticCredential{Secret: key}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/repositories/"+full, mtRepo, "", nil)
	var repo struct {
		Links links `json:"_links"`
	}
	_ = json.Unmarshal(body, &repo)
	code, _, body = s.call(t, http.MethodPost, repo.Links.href("archive"), "", "", nil)
	if code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("archive: %d %s", code, clip(body))
	}

	listed := func(query string) (bool, bool) {
		code, _, body := s.call(t, http.MethodGet, s.base+"/api/v2/repositories?pageSize=1000"+query, mtRepos, "", nil)
		var p struct {
			Embedded struct {
				Repos []struct {
					Namespace string `json:"namespace"`
					Name      string `json:"name"`
					Archived  bool   `json:"archived"`
				} `json:"repositories"`
			} `json:"_embedded"`
		}
		_ = json.Unmarshal(body, &p)
		if code != http.StatusOK {
			t.Fatalf("listing %q: %d %s", query, code, clip(body))
		}
		for _, r := range p.Embedded.Repos {
			if r.Namespace+"/"+r.Name == full {
				return true, r.Archived
			}
		}
		return false, false
	}
	for _, q := range []string{"", "&showArchived=true", "&showArchived=false"} {
		in, arch := listed(q)
		t.Logf("FACT archived repository in GET repositories%s: listed=%v archived=%v", q, in, arch)
	}
	in, arch := listed("")
	if !in || !arch {
		t.Errorf("the archived repository in the default listing: listed=%v archived=%v, want both true", in, arch)
	}
	archived, err := c.ListArchivedRepos(ctx)
	found := false
	for _, r := range archived {
		found = found || r.FullName == full
	}
	t.Logf("FACT ListArchivedRepos contains %s: %v err=%v", full, found, err)
	if !found {
		t.Errorf("ListArchivedRepos does not report %s", full)
	}
	active, err := c.ListRepos(ctx)
	for _, r := range active {
		if r.FullName == full {
			t.Errorf("ListRepos still lists the archived %s", full)
		}
	}
	t.Logf("FACT ListRepos omits the archived repository: err=%v", err)
}
