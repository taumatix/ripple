package gerrit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeGerrit is a fake Gerrit: the slice of the REST API ripple uses, and bare git
// repositories served over smart HTTP at /a/<project> that behave like Gerrit on
// push. A push to refs/for/<branch> becomes a change (or a new patch set of the
// change with the same Change-Id); a push without a Change-Id footer, or straight to
// a branch, is refused by a pre-receive hook, as Gerrit's defaults do. It is written
// from the vendored docs (docs/vendor/gerrit), not recorded from a real server.
type fakeGerrit struct {
	t    *testing.T
	root string
	// URL is the server root: the REST API is URL/a, git is URL/a/<project>.
	URL string
	// User and Token are the only credential the fake accepts, as HTTP Basic on the
	// API and on git (rest-api.md, "Authentication").
	User, Token string

	mu        sync.Mutex
	calls     map[string]int
	projects  map[string]bool
	changes   []*fakeChange
	rateLimit bool
	requests  []fakeReq
	// failAbandon makes the next abandon answer 500; failRead makes any GET whose
	// path contains it answer 500.
	failAbandon bool
	failRead    string
	// pageCap, when set, caps the page size the server returns, as Gerrit's internal limit does.
	pageCap int
}

type fakeChange struct {
	Number      int
	Project     string
	Branch      string
	ChangeID    string
	Topic       string
	WIP         bool
	Status      string
	Revisions   []string // commit shas, patch set 1 first
	Messages    []string
	MessageTags []string
	// MessageAuthors parallels Messages: the account that wrote each, "" for the system.
	MessageAuthors []string
	Votes          []int
	InlineComments []string
	// AbandonMessage is the review message the abandon carried.
	AbandonMessage string
	Hashtags       []string
	WIPPosts       int
	HashtagsPosted int
}

type fakeReq struct{ Method, Path, Query, Body string }

// preReceive is Gerrit's behaviour on push, reduced to the two rules ripple must
// meet: no direct push to a branch, and a Change-Id footer on every proposed commit.
// REMOTE_ADDR is set by the CGI gateway, so the fake's own seeding pushes pass.
const preReceive = `#!/bin/sh
[ -z "$REMOTE_ADDR" ] && exit 0
while read old new ref; do
  case "$ref" in
    refs/for/*)
      git log -1 --format=%B "$new" | grep -Eq '^Change-Id: I[0-9a-f]{40}$' || { echo "missing Change-Id in commit message" >&2; exit 1; }
      ;;
    *) echo "prohibited by Gerrit: not permitted: create/update $ref (push to refs/for/<branch>)" >&2; exit 1 ;;
  esac
done
`

func newFakeGerrit(t *testing.T) *fakeGerrit {
	t.Helper()
	f := &fakeGerrit{t: t, root: t.TempDir(), User: "ripple-bot", Token: "tok-123", calls: map[string]int{}, projects: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

func gitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (f *fakeGerrit) bare(project string) string { return filepath.Join(f.root, project) }

func (f *fakeGerrit) AddRepo(project string, files map[string]string) {
	f.t.Helper()
	if err := os.MkdirAll(f.bare(project), 0o755); err != nil {
		f.t.Fatal(err)
	}
	git(f.t, f.bare(project), "init", "--bare", "-b", "main")
	git(f.t, f.bare(project), "config", "uploadpack.allowFilter", "true")
	hook := filepath.Join(f.bare(project), "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte(preReceive), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	f.projects[project] = true
	f.mu.Unlock()
	f.Commit(project, files)
}

// Commit adds a commit to main directly, as a submit would.
func (f *fakeGerrit) Commit(project string, files map[string]string) {
	f.t.Helper()
	work := f.t.TempDir()
	git(f.t, work, "clone", "-q", f.bare(project), "w")
	w := filepath.Join(work, "w")
	git(f.t, w, "checkout", "-q", "-B", "main")
	for p, c := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	git(f.t, w, "add", "-A")
	git(f.t, w, "commit", "-q", "-m", "test commit")
	git(f.t, w, "push", "-q", "origin", "main")
}

func (f *fakeGerrit) rev(project, ref string) string {
	return strings.TrimSpace(git(f.t, f.bare(project), "rev-parse", ref))
}

// ingest turns pushes to refs/for/* into changes. Gerrit does this inside the push;
// the fake does it lazily, before any API request or inspection sees the state.
func (f *fakeGerrit) ingest() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ingestLocked()
}

func (f *fakeGerrit) ingestLocked() {
	var names []string
	for p := range f.projects {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, project := range names {
		out := git(f.t, f.bare(project), "for-each-ref", "--format=%(refname) %(objectname)", "refs/for/")
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			ref, sha, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			branch, opts, _ := strings.Cut(strings.TrimPrefix(ref, "refs/for/"), "%")
			branch = strings.TrimPrefix(branch, "refs/heads/")
			topic, wip := "", false
			for _, o := range strings.FieldsFunc(opts, func(r rune) bool { return r == '%' || r == ',' }) {
				switch k, v, _ := strings.Cut(o, "="); k {
				case "topic":
					topic = v
				case "wip":
					wip = true
				}
			}
			msg := git(f.t, f.bare(project), "log", "-1", "--format=%B", sha)
			id := ""
			for _, l := range strings.Split(msg, "\n") {
				if v, ok := strings.CutPrefix(l, "Change-Id: "); ok {
					id = strings.TrimSpace(v)
				}
			}
			var ch *fakeChange
			for _, c := range f.changes {
				if c.Project == project && c.Branch == branch && c.ChangeID == id {
					ch = c
				}
			}
			if ch == nil {
				ch = &fakeChange{Number: len(f.changes) + 1, Project: project, Branch: branch, ChangeID: id, Status: "NEW", WIP: wip}
				f.changes = append(f.changes, ch)
			}
			if ch.Status == "NEW" {
				ch.Revisions = append(ch.Revisions, sha)
				if topic != "" {
					ch.Topic = topic
				}
				git(f.t, f.bare(project), "update-ref", fmt.Sprintf("refs/changes/%02d/%d/%d", ch.Number%100, ch.Number, len(ch.Revisions)), sha)
			}
			git(f.t, f.bare(project), "update-ref", "-d", ref)
		}
	}
}

func (f *fakeGerrit) Changes(project string) []fakeChange {
	f.ingest()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeChange
	for _, c := range f.changes {
		if c.Project == project {
			out = append(out, *c)
		}
	}
	return out
}

// FileAt reads file at the current patch set of the change with topic branch, or on
// the branch itself.
func (f *fakeGerrit) FileAt(project, branch, path string) (string, bool) {
	rev := branch
	for _, c := range f.Changes(project) {
		if c.Topic == branch && len(c.Revisions) > 0 {
			rev = c.Revisions[len(c.Revisions)-1]
		}
	}
	cmd := exec.Command("git", "show", rev+":"+path)
	cmd.Dir = f.bare(project)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	return string(out), err == nil
}

func (f *fakeGerrit) Calls(project string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[project]
}

func (f *fakeGerrit) Requests() []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeReq(nil), f.requests...)
}

func (f *fakeGerrit) authOK(r *http.Request) bool {
	u, p, ok := r.BasicAuth()
	return ok && u == f.User && p == f.Token
}

func (f *fakeGerrit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/a/changes/") || r.URL.Path == "/a/changes" || strings.HasPrefix(r.URL.Path, "/a/projects/"):
		f.serveAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/a/"):
		f.serveGit(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGerrit) serveGit(w http.ResponseWriter, r *http.Request) {
	if !f.authOK(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Gerrit Code Review"`)
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return
	}
	f.ingest()
	backend, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h := &cgi.Handler{Path: strings.TrimSpace(string(backend)) + "/git-http-backend",
		Env: []string{"GIT_PROJECT_ROOT=" + f.root, "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"}}
	if r.ContentLength < 0 { // CGI cannot take a chunked body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		r.Body, r.ContentLength, r.TransferEncoding = io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
		r.Header.Del("Transfer-Encoding")
	}
	http.StripPrefix("/a", h).ServeHTTP(w, r)
}

// writeJSON answers the way rest-api.md ("Response Format") says: the magic prefix
// line, then JSON.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(code)
	b, _ := json.Marshal(v)
	_, _ = w.Write(append([]byte(")]}'\n"), b...))
}

func text(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg)
}

func (f *fakeGerrit) serveAPI(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.ingest()
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/a/")
	var parts []string
	for _, p := range strings.Split(rest, "/") {
		u, _ := url.PathUnescape(p)
		parts = append(parts, u)
	}
	// parts[0] is "changes" or "projects"; "changes/" leaves a trailing empty part.
	f.mu.Lock()
	f.requests = append(f.requests, fakeReq{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, string(body)})
	project := f.projectOf(r, parts)
	if project != "" {
		f.calls[project]++
	}
	f.mu.Unlock()
	if !f.authOK(r) {
		text(w, 401, "Unauthorized")
		return
	}
	f.mu.Lock()
	failRead := f.failRead != "" && r.Method == http.MethodGet && strings.Contains(r.URL.Path, f.failRead)
	f.mu.Unlock()
	if failRead {
		text(w, 500, "injected read failure")
		return
	}
	switch {
	case parts[0] == "changes" && len(parts) == 2 && parts[1] == "" && r.Method == http.MethodGet:
		f.queryChanges(w, r)
	case parts[0] == "changes" && len(parts) == 2 && r.Method == http.MethodGet:
		f.getChange(w, r, parts[1])
	case parts[0] == "changes" && len(parts) == 3 && parts[2] == "comments" && r.Method == http.MethodGet:
		f.listComments(w, parts[1])
	case parts[0] == "changes" && r.Method == http.MethodPost && len(parts) >= 3:
		f.postChange(w, parts[1], strings.Join(parts[2:], "/"), body)
	case parts[0] == "projects" && len(parts) == 2 && parts[1] == "" && r.Method == http.MethodGet:
		f.listProjects(w, r)
	case parts[0] == "projects" && len(parts) == 3 && parts[2] == "HEAD" && r.Method == http.MethodGet:
		f.projectHead(w, parts[1])
	case parts[0] == "projects" && len(parts) >= 4 && parts[2] == "commits" && r.Method == http.MethodGet:
		f.commit(w, parts[1], parts[3], len(parts) == 5 && parts[4] == "files" || len(parts) == 6 && parts[4] == "files")
	default:
		text(w, 404, "Not found: "+r.URL.Path)
	}
}

// projectOf is the project a request is about, for Calls. Called with f.mu held.
func (f *fakeGerrit) projectOf(r *http.Request, parts []string) string {
	switch {
	case parts[0] == "projects" && len(parts) > 1:
		return parts[1]
	case parts[0] == "changes" && len(parts) == 2 && parts[1] == "":
		for _, tok := range strings.Fields(r.URL.Query().Get("q")) {
			if p, ok := strings.CutPrefix(tok, "project:"); ok {
				return p
			}
		}
	case parts[0] == "changes" && len(parts) >= 2:
		if ch := f.lookupLocked(parts[1]); ch != nil {
			return ch.Project
		}
	}
	return ""
}

func (f *fakeGerrit) lookupLocked(handle string) *fakeChange {
	if n, err := strconv.Atoi(handle); err == nil {
		for _, c := range f.changes {
			if c.Number == n {
				return c
			}
		}
		return nil
	}
	p := strings.Split(handle, "~")
	if len(p) != 3 {
		return nil
	}
	for _, c := range f.changes {
		if c.Project == p[0] && c.Branch == p[1] && c.ChangeID == p[2] {
			return c
		}
	}
	return nil
}

func (f *fakeGerrit) changeJSON(c *fakeChange) map[string]any {
	m := map[string]any{"id": url.PathEscape(c.Project) + "~" + c.Branch + "~" + c.ChangeID, "project": c.Project, "branch": c.Branch,
		"change_id": c.ChangeID, "status": c.Status, "_number": c.Number, "current_revision_number": len(c.Revisions)}
	if c.Topic != "" {
		m["topic"] = c.Topic
	}
	if c.WIP {
		m["work_in_progress"] = true
	}
	return m
}

// queryChanges is "Query Changes": status: and project: operators, n and S paging,
// _more_changes on the last entry (rest-api-changes.md).
func (f *fakeGerrit) queryChanges(w http.ResponseWriter, r *http.Request) {
	status, project := "", ""
	for _, tok := range strings.Fields(r.URL.Query().Get("q")) {
		switch {
		case strings.HasPrefix(tok, "status:"):
			status = strings.TrimPrefix(tok, "status:")
		case strings.HasPrefix(tok, "project:"):
			project = strings.TrimPrefix(tok, "project:")
		default:
			text(w, 400, "unsupported query operator: "+tok)
			return
		}
	}
	f.mu.Lock()
	var all []map[string]any
	for i := len(f.changes) - 1; i >= 0; i-- { // newest first
		c := f.changes[i]
		if (status == "open" && c.Status != "NEW") || (project != "" && c.Project != project) {
			continue
		}
		m := f.changeJSON(c)
		// o=CURRENT_REVISION adds the current patch set's commit id (rest-api-changes.md);
		// without the option the field is absent, as on the service.
		if slices.Contains(r.URL.Query()["o"], "CURRENT_REVISION") && len(c.Revisions) > 0 {
			m["current_revision"] = c.Revisions[len(c.Revisions)-1]
		}
		all = append(all, m)
	}
	cap := f.pageCap
	f.mu.Unlock()
	f.page(w, r, all, cap, "_more_changes")
}

func (f *fakeGerrit) page(w http.ResponseWriter, r *http.Request, all []map[string]any, capN int, more string) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || (capN > 0 && n > capN) {
		n = max(capN, 500)
		if capN > 0 {
			n = capN
		}
	}
	skip, _ := strconv.Atoi(r.URL.Query().Get("S"))
	skip = min(skip, len(all))
	end := min(skip+n, len(all))
	out := all[skip:end]
	if end < len(all) && len(out) > 0 {
		out[len(out)-1][more] = true
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, 200, out)
}

// getChange is "Get Change" with the o= options ripple asks for: ALL_REVISIONS with
// ALL_COMMITS (revisions keyed by commit, each with its author and committer),
// DETAILED_LABELS (every vote) and MESSAGES (rest-api-changes.md, "Query Changes").
func (f *fakeGerrit) getChange(w http.ResponseWriter, r *http.Request, handle string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.lookupLocked(handle)
	if c == nil {
		text(w, 404, "Not found: "+handle)
		return
	}
	m := f.changeJSON(c)
	opts := map[string]bool{}
	for _, o := range r.URL.Query()["o"] {
		opts[o] = true
	}
	if opts["ALL_REVISIONS"] {
		revs := map[string]any{}
		for i, sha := range c.Revisions {
			ri := map[string]any{"_number": i + 1}
			if opts["ALL_COMMITS"] {
				who := strings.Split(strings.TrimSpace(git(f.t, f.bare(c.Project), "log", "-1", "--format=%ae%n%ce%n%s", sha)), "\n")
				ri["commit"] = map[string]any{"author": map[string]any{"email": who[0]}, "committer": map[string]any{"email": who[1]}, "subject": who[2]}
			}
			revs[sha] = ri
		}
		m["revisions"] = revs
		m["current_revision"] = c.Revisions[len(c.Revisions)-1]
	}
	if opts["DETAILED_LABELS"] {
		var all []map[string]any
		for _, v := range c.Votes {
			all = append(all, map[string]any{"_account_id": 1000096, "value": v})
		}
		m["labels"] = map[string]any{"Code-Review": map[string]any{"all": all}}
	}
	if opts["MESSAGES"] {
		var msgs []map[string]any
		for i, text := range c.Messages {
			mi := map[string]any{"id": fmt.Sprintf("m%d", i), "message": text, "_revision_number": 1}
			if c.MessageTags[i] != "" {
				mi["tag"] = c.MessageTags[i]
			}
			if c.MessageAuthors[i] != "" {
				mi["author"] = map[string]any{"_account_id": 1000097, "username": c.MessageAuthors[i]}
			}
			msgs = append(msgs, mi)
		}
		m["messages"] = msgs
	}
	writeJSON(w, 200, m)
}

// listComments is "List Change Comments": a map of file path to CommentInfo list.
func (f *fakeGerrit) listComments(w http.ResponseWriter, handle string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.lookupLocked(handle)
	if c == nil {
		text(w, 404, "Not found: "+handle)
		return
	}
	out := map[string]any{}
	for i, msg := range c.InlineComments {
		out["go.mod"] = append(asList(out["go.mod"]), map[string]any{"patch_set": 1, "id": fmt.Sprintf("c%d", i), "message": msg})
	}
	writeJSON(w, 200, out)
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func (f *fakeGerrit) postChange(w http.ResponseWriter, handle, action string, body []byte) {
	f.mu.Lock()
	if f.rateLimit {
		f.rateLimit = false
		f.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		text(w, 429, "Too Many Requests")
		return
	}
	defer f.mu.Unlock()
	c := f.lookupLocked(handle)
	if c == nil {
		text(w, 404, "Not found: "+handle)
		return
	}
	switch action {
	case "wip":
		c.WIP = true
		c.WIPPosts++
		w.WriteHeader(200)
	case "revisions/current/review":
		var in struct{ Message, Tag string }
		if err := json.Unmarshal(body, &in); err != nil {
			text(w, 400, "bad ReviewInput")
			return
		}
		c.Messages = append(c.Messages, in.Message)
		c.MessageTags = append(c.MessageTags, in.Tag)
		c.MessageAuthors = append(c.MessageAuthors, f.User)
		writeJSON(w, 200, map[string]any{})
	case "abandon":
		// rest-api-changes.md, "Abandon Change": 409 when the state does not allow it.
		if f.failAbandon {
			f.failAbandon = false
			text(w, 500, "injected abandon failure")
			return
		}
		if c.Status != "NEW" {
			text(w, 409, "change is "+strings.ToLower(c.Status))
			return
		}
		var in struct{ Message string }
		if len(body) > 0 {
			if err := json.Unmarshal(body, &in); err != nil {
				text(w, 400, "bad AbandonInput")
				return
			}
		}
		c.Status = "ABANDONED"
		c.AbandonMessage = in.Message
		if in.Message != "" {
			c.Messages = append(c.Messages, in.Message)
			c.MessageTags = append(c.MessageTags, "")
			c.MessageAuthors = append(c.MessageAuthors, f.User)
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		writeJSON(w, 200, f.changeJSON(c))
	case "hashtags":
		var in struct{ Add, Remove []string }
		if err := json.Unmarshal(body, &in); err != nil {
			text(w, 400, "bad HashtagsInput")
			return
		}
		c.Hashtags = append(c.Hashtags, in.Add...)
		c.HashtagsPosted++
		writeJSON(w, 200, c.Hashtags)
	default:
		text(w, 404, "Not found: "+action)
	}
}

func (f *fakeGerrit) listProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("type") != "CODE" || q.Get("state") != "ACTIVE" {
		text(w, 400, "unexpected project filters")
		return
	}
	f.mu.Lock()
	var names []string
	for n := range f.projects {
		if strings.HasPrefix(n, q.Get("p")) {
			names = append(names, n)
		}
	}
	capN := f.pageCap
	f.mu.Unlock()
	sort.Strings(names)
	n, _ := strconv.Atoi(q.Get("n"))
	if n <= 0 || (capN > 0 && n > capN) {
		n = max(capN, 500)
		if capN > 0 {
			n = capN
		}
	}
	skip, _ := strconv.Atoi(q.Get("S"))
	skip = min(skip, len(names))
	end := min(skip+n, len(names))
	out := map[string]map[string]any{}
	for _, name := range names[skip:end] {
		out[name] = map[string]any{"id": url.PathEscape(name)}
	}
	if end < len(names) {
		out[names[end-1]]["_more_projects"] = true
	}
	writeJSON(w, 200, out)
}

func (f *fakeGerrit) projectHead(w http.ResponseWriter, project string) {
	f.mu.Lock()
	ok := f.projects[project]
	f.mu.Unlock()
	if !ok {
		text(w, 404, "Not found: "+project)
		return
	}
	writeJSON(w, 200, "refs/heads/main")
}

// commit is "Get Commit" (parents) or "List Files" (a map of path to FileInfo with
// the /COMMIT_MSG magic entry first) of rest-api-projects.md.
func (f *fakeGerrit) commit(w http.ResponseWriter, project, sha string, files bool) {
	f.mu.Lock()
	ok := f.projects[project]
	f.mu.Unlock()
	if !ok {
		text(w, 404, "Not found: "+project)
		return
	}
	if !files {
		cmd := exec.Command("git", "rev-list", "--parents", "-n1", sha)
		cmd.Dir = f.bare(project)
		cmd.Env = gitEnv()
		out, err := cmd.Output()
		if err != nil {
			text(w, 404, "Not found: "+sha)
			return
		}
		fields := strings.Fields(string(out))
		var parents []map[string]any
		for _, p := range fields[1:] {
			parents = append(parents, map[string]any{"commit": p, "subject": "s"})
		}
		writeJSON(w, 200, map[string]any{"commit": fields[0], "parents": parents})
		return
	}
	cmd := exec.Command("git", "diff-tree", "-r", "-M", "--root", "--no-commit-id", "--name-status", sha)
	cmd.Dir = f.bare(project)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		text(w, 404, "Not found: "+sha)
		return
	}
	res := map[string]any{"/COMMIT_MSG": map[string]any{"status": "A", "lines_inserted": 1}}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 2 {
			continue
		}
		fi := map[string]any{}
		st := cols[0][:1]
		if st != "M" {
			fi["status"] = st // FileInfo omits the status for a modification
		}
		path := cols[len(cols)-1]
		if st == "R" {
			fi["old_path"] = cols[1]
		}
		res[path] = fi
	}
	writeJSON(w, 200, res)
}

// byTopic is the change of project with topic. Test helpers below act as a person
// would: through the same state the REST reads report.
func (f *fakeGerrit) byTopic(project, topic string) *fakeChange {
	f.ingestLocked()
	for _, c := range f.changes {
		if c.Project == project && c.Topic == topic {
			return c
		}
	}
	f.t.Fatalf("no change with topic %q in %s", topic, project)
	return nil
}

// HumanMessage is a review message a person posted.
func (f *fakeGerrit) HumanMessage(project, topic, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byTopic(project, topic)
	c.Messages = append(c.Messages, msg)
	c.MessageTags = append(c.MessageTags, "")
	c.MessageAuthors = append(c.MessageAuthors, "jane")
}

// SystemMessage is a message Gerrit wrote itself: no author, an autogenerated tag.
func (f *fakeGerrit) SystemMessage(project, topic, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byTopic(project, topic)
	c.Messages = append(c.Messages, msg)
	c.MessageTags = append(c.MessageTags, "autogenerated:gerrit:newPatchSet")
	c.MessageAuthors = append(c.MessageAuthors, "")
}

func (f *fakeGerrit) Vote(project, topic string, v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byTopic(project, topic)
	c.Votes = append(c.Votes, v)
}

func (f *fakeGerrit) InlineComment(project, topic, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byTopic(project, topic)
	c.InlineComments = append(c.InlineComments, msg)
}

// SetTopic renames the topic, as a person using "Set Topic" does.
func (f *fakeGerrit) SetTopic(project, topic, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byTopic(project, topic).Topic = to
}

// PatchSetBy adds a patch set whose commit is the current one's tree, authored and
// committed by email: a person's edit, or (with ripple's own email) a rewrite that
// keeps ripple's identity.
func (f *fakeGerrit) PatchSetBy(project, topic, author, committer string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byTopic(project, topic)
	bare := f.bare(project)
	last := c.Revisions[len(c.Revisions)-1]
	cmd := exec.Command("git", "commit-tree", last+"^{tree}", "-p", last+"^", "-m", "edited\n\nChange-Id: "+c.ChangeID)
	cmd.Dir = bare
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME=x", "GIT_AUTHOR_EMAIL="+author, "GIT_COMMITTER_NAME=x", "GIT_COMMITTER_EMAIL="+committer)
	out, err := cmd.Output()
	if err != nil {
		f.t.Fatalf("commit-tree: %v", err)
	}
	sha := strings.TrimSpace(string(out))
	c.Revisions = append(c.Revisions, sha)
	git(f.t, bare, "update-ref", fmt.Sprintf("refs/changes/%02d/%d/%d", c.Number%100, c.Number, len(c.Revisions)), sha)
}

// Abandon abandons the change out of band, as a person does.
func (f *fakeGerrit) Abandon(project, topic string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byTopic(project, topic).Status = "ABANDONED"
}

func (f *fakeGerrit) FailNextAbandon() { f.mu.Lock(); f.failAbandon = true; f.mu.Unlock() }
func (f *fakeGerrit) FailReads(pathPart string) {
	f.mu.Lock()
	f.failRead = pathPart
	f.mu.Unlock()
}
