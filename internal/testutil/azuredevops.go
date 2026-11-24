package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// AzurePAT is the personal access token the fake Azure DevOps accepts.
const AzurePAT = "azure-test-pat"

// AzureCreatorID is the identity every pull request is created by, the way the
// real service stamps createdBy with the authenticated user.
const AzureCreatorID = "d6245f20-2af8-44f4-9451-8107cb2767db"

// AzurePR is a pull request the fake Azure DevOps recorded.
type AzurePR struct {
	ID                   int
	Head, Base           string // branch names, without refs/heads/
	Title, Description   string
	IsDraft              bool
	Labels               []string
	AutoCompleteSetBy    string
	MergeStrategy        string
	sourceRef, targetRef string
}

// AzureDevOps is a fake Azure DevOps Services organisation: the REST 7.1 subset
// ripple calls, and the git smart-HTTP endpoint, both on one server the way
// dev.azure.com serves them. Repositories are addressed "org/project/repo"
// and live on disk at <root>/org/project/_git/repo. Written from the vendored
// Microsoft docs in docs/vendor/azuredevops, not recorded from the real service.
type AzureDevOps struct {
	// URL is both the REST base and the git base (http://127.0.0.1:port).
	URL string
	Org string

	t    *testing.T
	root string

	mu    sync.Mutex
	calls map[string]int
	prs   map[string][]*AzurePR
	// RateLimitOnce makes the next pull request POST answer 429 (TF400733) with Retry-After: 1.
	RateLimitOnce bool
	// RetryAfterOnOK makes the next successful GET carry Retry-After: 60 on a 200, which
	// the rate-limit doc says the service does ahead of delaying a caller.
	RetryAfterOnOK bool
	// GitRejected counts git requests refused for missing or wrong credentials.
	GitRejected int
}

func NewAzureDevOps(t *testing.T, org string) *AzureDevOps {
	t.Helper()
	a := &AzureDevOps{Org: org, t: t, root: t.TempDir(), calls: map[string]int{}, prs: map[string][]*AzurePR{}}
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	backend := &cgi.Handler{
		Path: strings.TrimSpace(string(execPath)) + "/git-http-backend",
		Env: []string{"GIT_PROJECT_ROOT=" + a.root, "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			if strings.Contains(r.URL.Path, "/_git/") {
				a.mu.Lock()
				a.GitRejected++
				a.mu.Unlock()
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="Azure DevOps"`)
			http.Error(w, "TF400813: The user is not authorized to access this resource.", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.Path, "/_git/") {
			if r.ContentLength < 0 { // CGI cannot take a chunked body
				body, _ := io.ReadAll(r.Body)
				r.Body, r.ContentLength, r.TransferEncoding = io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
				r.Header.Del("Transfer-Encoding")
			}
			backend.ServeHTTP(w, r)
			return
		}
		a.serveREST(w, r)
	}))
	t.Cleanup(srv.Close)
	a.URL = srv.URL
	return a
}

// authorized is the PAT scheme of use-personal-access-tokens.md: HTTP Basic, the
// user name ignored, the token as the password.
func (a *AzureDevOps) authorized(r *http.Request) bool {
	_, pass, ok := basicAuth(r)
	return ok && pass == AzurePAT
}

func (a *AzureDevOps) bare(path string) string {
	org, rest, _ := strings.Cut(path, "/")
	project, repo, _ := strings.Cut(rest, "/")
	return filepath.Join(a.root, org, project, "_git", repo)
}

// AddRepo creates the repository org/project/repo with files on main.
func (a *AzureDevOps) AddRepo(path string, files map[string]string) {
	a.t.Helper()
	bare := a.bare(path)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		a.t.Fatal(err)
	}
	git(a.t, bare, "init", "--bare", "-b", "main")
	git(a.t, bare, "config", "uploadpack.allowFilter", "true")
	a.Commit(path, files)
}

// Commit adds a commit on main (a human pushing to the default branch) and returns its id.
func (a *AzureDevOps) Commit(path string, files map[string]string) string {
	a.t.Helper()
	work := a.t.TempDir()
	git(a.t, work, "clone", "-q", a.bare(path), "w")
	w := filepath.Join(work, "w")
	git(a.t, w, "checkout", "-q", "-B", "main")
	for p, c := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			a.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			a.t.Fatal(err)
		}
	}
	git(a.t, w, "add", "-A")
	git(a.t, w, "commit", "-q", "-m", "test commit")
	git(a.t, w, "push", "-q", "origin", "main")
	return strings.TrimSpace(git(a.t, w, "rev-parse", "HEAD"))
}

// PushBranch creates branch off main with one commit by the given identity.
func (a *AzureDevOps) PushBranch(path, branch, authorEmail string) {
	a.t.Helper()
	pushBranchCommit(a.t, a.bare(path), branch, authorEmail)
}

// HumanCommitOnBranch pushes one more commit by a person onto branch.
func (a *AzureDevOps) HumanCommitOnBranch(path, branch string) {
	a.t.Helper()
	humanCommit(a.t, a.bare(path), branch)
}

// BranchHead is the commit branch points at in path.
func (a *AzureDevOps) BranchHead(path, branch string) string { return branchTip(a.bare(path), branch) }

// FileAt is the content of file on branch, ok=false when absent.
func (a *AzureDevOps) FileAt(path, branch, file string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+file)
	cmd.Dir = a.bare(path)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	return string(out), err == nil
}

func (a *AzureDevOps) hasRef(path, ref string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = a.bare(path)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// Calls is the number of REST requests addressed to the repository.
func (a *AzureDevOps) Calls(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[path]
}

func (a *AzureDevOps) PRs(path string) []AzurePR {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AzurePR
	for _, p := range a.prs[path] {
		out = append(out, *p)
	}
	return out
}

func (a *AzureDevOps) fail(w http.ResponseWriter, code int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.t.Logf("fake azure devops: %d %s", code, msg)
	http.Error(w, msg, code)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *AzureDevOps) projects() []string {
	ents, _ := os.ReadDir(filepath.Join(a.root, a.Org))
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func (a *AzureDevOps) repos(project string) []string {
	ents, _ := os.ReadDir(filepath.Join(a.root, a.Org, project, "_git"))
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func page(q map[string][]string, n int) (from, to int) {
	skip, top := 0, 100
	if v := q["$skip"]; len(v) > 0 {
		skip, _ = strconv.Atoi(v[0])
	}
	if v := q["$top"]; len(v) > 0 {
		top, _ = strconv.Atoi(v[0])
	}
	from = min(skip, n)
	return from, min(from+top, n)
}

func (a *AzureDevOps) serveREST(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("api-version") != "7.1" {
		a.fail(w, http.StatusBadRequest, "api-version must be 7.1, got %q", q.Get("api-version"))
		a.t.Errorf("request without api-version=7.1: %s %s", r.Method, r.URL)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != a.Org {
		a.fail(w, http.StatusNotFound, "unknown route %s", r.URL.Path)
		return
	}
	if a.takeRetryAfterOnOK() {
		w.Header().Set("Retry-After", "60")
	}
	// /{org}/_apis/projects
	if len(parts) == 3 && parts[1] == "_apis" && parts[2] == "projects" && r.Method == http.MethodGet {
		ps := a.projects()
		from, to := page(q, len(ps))
		var vals []map[string]any
		for _, p := range ps[from:to] {
			vals = append(vals, map[string]any{"id": "proj-" + p, "name": p, "state": "wellFormed"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(vals), "value": vals})
		return
	}
	// /{org}/{project}/_apis/git/repositories[/{repo}/pullrequests[/{id}[/labels]]]
	if len(parts) < 5 || parts[2] != "_apis" || parts[3] != "git" || parts[4] != "repositories" {
		a.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
		return
	}
	project := parts[1]
	if len(parts) == 5 && r.Method == http.MethodGet {
		rs := a.repos(project)
		from, to := page(q, len(rs))
		vals := []map[string]any{}
		for _, n := range rs[from:to] {
			vals = append(vals, map[string]any{"id": "repo-" + n, "name": n, "defaultBranch": "refs/heads/main", "isDisabled": false,
				"project": map[string]any{"name": project}})
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(vals), "value": vals})
		return
	}
	if len(parts) < 6 {
		a.fail(w, http.StatusNotFound, "unknown route")
		return
	}
	a.serveGit(w, r, project, parts[5:])
}

func (a *AzureDevOps) takeRetryAfterOnOK() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.RetryAfterOnOK
	a.RetryAfterOnOK = false
	return v
}

func (a *AzureDevOps) serveGit(w http.ResponseWriter, r *http.Request, project string, rest []string) {
	repo := rest[0]
	path := a.Org + "/" + project + "/" + repo
	if _, err := os.Stat(a.bare(path)); err != nil {
		a.fail(w, http.StatusNotFound, "TF401019: The Git repository with name or identifier %s does not exist", repo)
		return
	}
	a.mu.Lock()
	a.calls[path]++
	limit := a.RateLimitOnce && r.Method == http.MethodPost
	if limit {
		a.RateLimitOnce = false
	}
	a.mu.Unlock()
	if limit {
		w.Header().Set("Retry-After", "1")
		a.fail(w, http.StatusTooManyRequests, "TF400733: The request has been canceled: Request was blocked due to exceeding usage of resource Git in namespace 1.")
		return
	}
	if len(rest) == 4 && rest[1] == "commits" && rest[3] == "changes" && r.Method == http.MethodGet {
		a.commitChanges(w, path, rest[2])
		return
	}
	if len(rest) < 2 || strings.ToLower(rest[1]) != "pullrequests" {
		a.fail(w, http.StatusNotFound, "unknown route %s", r.URL.Path)
		return
	}
	switch {
	case len(rest) == 2 && r.Method == http.MethodGet:
		a.listPRs(w, r, path)
	case len(rest) == 2 && r.Method == http.MethodPost:
		a.createPR(w, r, path, project, repo)
	case len(rest) == 3 && r.Method == http.MethodPatch:
		a.patchPR(w, r, path, project, repo, rest[2])
	case len(rest) == 4 && rest[3] == "labels" && r.Method == http.MethodPost:
		a.labelPR(w, r, path, rest[2])
	default:
		a.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

func (a *AzureDevOps) prJSON(path, project, repo string, p *AzurePR) map[string]any {
	m := map[string]any{
		"repository":    map[string]any{"id": "repo-" + repo, "name": repo, "project": map[string]any{"name": project}},
		"pullRequestId": p.ID, "status": "active", "isDraft": p.IsDraft,
		"createdBy": map[string]any{"id": AzureCreatorID},
		"title":     p.Title, "sourceRefName": p.sourceRef, "targetRefName": p.targetRef,
		"url": fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d", a.URL, a.Org, "repo-"+repo, p.ID),
	}
	if sha := branchTip(a.bare(path), strings.TrimPrefix(p.sourceRef, "refs/heads/")); sha != "" {
		// lastMergeSourceCommit.commitId: the source branch's commit (rest-pull-requests-get-list.md).
		m["lastMergeSourceCommit"] = map[string]any{"commitId": sha}
	}
	if p.AutoCompleteSetBy != "" {
		m["autoCompleteSetBy"] = map[string]any{"id": p.AutoCompleteSetBy}
	}
	return m
}

func (a *AzureDevOps) listPRs(w http.ResponseWriter, r *http.Request, path string) {
	q := r.URL.Query()
	if s := q.Get("searchCriteria.status"); s != "" && s != "active" {
		writeJSON(w, http.StatusOK, map[string]any{"count": 0, "value": []any{}})
		return
	}
	project, repo := splitPath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	all := a.prs[path]
	from, to := page(q, len(all))
	vals := []map[string]any{}
	for _, p := range all[from:to] {
		vals = append(vals, a.prJSON(path, project, repo, p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(vals), "value": vals})
}

func splitPath(path string) (project, repo string) {
	parts := strings.Split(path, "/")
	return parts[1], parts[2]
}

func (a *AzureDevOps) createPR(w http.ResponseWriter, r *http.Request, path, project, repo string) {
	var in struct {
		SourceRefName string `json:"sourceRefName"`
		TargetRefName string `json:"targetRefName"`
		Title         string `json:"title"`
		Description   string `json:"description"`
		IsDraft       bool   `json:"isDraft"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" {
		a.fail(w, http.StatusBadRequest, "bad pull request body")
		return
	}
	for _, ref := range []string{in.SourceRefName, in.TargetRefName} {
		if !strings.HasPrefix(ref, "refs/heads/") || !a.hasRef(path, ref) {
			a.fail(w, http.StatusBadRequest, "TF401398: The pull request cannot be activated because the source or target branch %q does not exist", ref)
			return
		}
	}
	a.mu.Lock()
	p := &AzurePR{ID: len(a.prs[path]) + 1, Head: strings.TrimPrefix(in.SourceRefName, "refs/heads/"), Base: strings.TrimPrefix(in.TargetRefName, "refs/heads/"),
		Title: in.Title, Description: in.Description, IsDraft: in.IsDraft, sourceRef: in.SourceRefName, targetRef: in.TargetRefName}
	a.prs[path] = append(a.prs[path], p)
	resp := a.prJSON(path, project, repo, p)
	a.mu.Unlock()
	writeJSON(w, http.StatusCreated, resp)
}

func (a *AzureDevOps) find(path, id string) *AzurePR {
	n, _ := strconv.Atoi(id)
	for _, p := range a.prs[path] {
		if p.ID == n {
			return p
		}
	}
	return nil
}

func (a *AzureDevOps) patchPR(w http.ResponseWriter, r *http.Request, path, project, repo, id string) {
	var in struct {
		AutoCompleteSetBy *struct{ ID string } `json:"autoCompleteSetBy"`
		CompletionOptions *struct {
			MergeStrategy string `json:"mergeStrategy"`
		} `json:"completionOptions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		a.fail(w, http.StatusBadRequest, "bad body")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.find(path, id)
	if p == nil {
		a.fail(w, http.StatusNotFound, "TF401180: The requested pull request was not found.")
		return
	}
	if in.AutoCompleteSetBy != nil {
		if in.AutoCompleteSetBy.ID != AzureCreatorID {
			a.fail(w, http.StatusBadRequest, "autoCompleteSetBy.id must be the authenticated user")
			return
		}
		p.AutoCompleteSetBy = in.AutoCompleteSetBy.ID
	}
	if in.CompletionOptions != nil {
		p.MergeStrategy = in.CompletionOptions.MergeStrategy
	}
	writeJSON(w, http.StatusOK, a.prJSON(path, project, repo, p))
}

func (a *AzureDevOps) labelPR(w http.ResponseWriter, r *http.Request, path, id string) {
	var in struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		a.fail(w, http.StatusBadRequest, "bad label body")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.find(path, id)
	if p == nil {
		a.fail(w, http.StatusNotFound, "no pull request")
		return
	}
	p.Labels = append(p.Labels, in.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"id": "tag-" + in.Name, "name": in.Name, "active": true})
}

// commitChanges is "Commits - Get Changes" (rest-commits-get-changes.md): the files
// the commit touched, as items with a leading-slash path.
func (a *AzureDevOps) commitChanges(w http.ResponseWriter, path, sha string) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		a.fail(w, http.StatusNotFound, "TF401019: commit %q not found", sha)
		return
	}
	cmd := exec.Command("git", "show", "--no-renames", "--name-status", "--format=", sha)
	cmd.Dir = a.bare(path)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		a.fail(w, http.StatusNotFound, "TF401019: commit %s not found", sha)
		return
	}
	var changes []map[string]any
	counts := map[string]int{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		st, file, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		kind := map[string]string{"A": "add", "M": "edit", "D": "delete"}[st]
		counts[strings.ToUpper(kind[:1])+kind[1:]]++
		changes = append(changes, map[string]any{"changeType": kind, "item": map[string]any{"gitObjectType": "blob", "path": "/" + file}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"changeCounts": counts, "changes": changes})
}
