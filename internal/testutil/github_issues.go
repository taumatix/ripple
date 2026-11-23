package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Issue is an issue the fake recorded.
type Issue struct {
	Repo, Title, Body string
	Number            int
	Labels            []string
	Closed            bool
	// PullRequest marks an item that is a pull request: GitHub lists those among issues.
	PullRequest bool
}

type issueState struct {
	issues   map[string][]*Issue
	disabled map[string]bool
	forbid   map[string]bool
	writes   map[string]int
	labels   map[string]bool
}

func (g *GitHub) is() *issueState {
	if g.issueState == nil {
		g.issueState = &issueState{issues: map[string][]*Issue{}, disabled: map[string]bool{}, forbid: map[string]bool{}, writes: map[string]int{}}
	}
	return g.issueState
}

// DisableIssues makes every issues endpoint of repo answer 410, as GitHub does when
// the repository has issues turned off.
func (g *GitHub) DisableIssues(repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.is().disabled[repo] = true
}

// ForbidIssues makes every issues endpoint of repo answer 403 "Resource not
// accessible by integration", as GitHub does for an App without the Issues permission.
func (g *GitHub) ForbidIssues(repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.is().forbid[repo] = true
}

// Issues lists the issues recorded for repo, in number order.
func (g *GitHub) Issues(repo string) []Issue {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Issue
	for _, i := range g.is().issues[repo] {
		out = append(out, *i)
	}
	return out
}

// IssueWrites counts the issue creations and updates addressed to repo.
func (g *GitHub) IssueWrites(repo string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.is().writes[repo]
}

// HumanIssue adds an issue a person opened, with labels.
func (g *GitHub) HumanIssue(repo, title, body string, labels ...string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.is()
	i := &Issue{Repo: repo, Title: title, Body: body, Labels: labels, Number: len(st.issues[repo]) + 1}
	st.issues[repo] = append(st.issues[repo], i)
	return i.Number
}

// HumanCloseIssue closes an issue as a person would.
func (g *GitHub) HumanCloseIssue(repo string, number int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, i := range g.is().issues[repo] {
		if i.Number == number {
			i.Closed = true
		}
	}
}

// HumanDeleteIssue removes an issue; the fake then answers 410 for it, as GitHub does.
func (g *GitHub) HumanDeleteIssue(repo string, number int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.is()
	for k, i := range st.issues[repo] {
		if i.Number == number {
			st.issues[repo][k] = &Issue{Repo: repo, Number: number, Title: "\x00deleted"}
		}
	}
}

func issueJSON(i *Issue) map[string]any {
	labels := []map[string]any{}
	for _, l := range i.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	m := map[string]any{"number": i.Number, "title": i.Title, "body": i.Body, "labels": labels,
		"state":    map[bool]string{false: "open", true: "closed"}[i.Closed],
		"html_url": fmt.Sprintf("https://github.example/%s/issues/%d", i.Repo, i.Number)}
	if i.PullRequest {
		m["pull_request"] = map[string]any{}
	}
	return m
}

// handleIssues serves GET and POST /repos/o/r/issues and PATCH /repos/o/r/issues/{n};
// g.mu is not held. Listing is paged the way GitHub pages it (per_page, page) and
// filters on labels and state.
func (g *GitHub) handleIssues(w http.ResponseWriter, r *http.Request, repo string, parts []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.is()
	if st.disabled[repo] {
		g.fail(w, http.StatusGone, "Issues are disabled for this repo")
		return
	}
	if st.forbid[repo] {
		g.fail(w, http.StatusForbidden, "Resource not accessible by integration")
		return
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodGet:
		q := r.URL.Query()
		per, _ := strconv.Atoi(q.Get("per_page"))
		pg, _ := strconv.Atoi(q.Get("page"))
		if per < 1 {
			per = 30
		}
		if pg < 1 {
			pg = 1
		}
		state := q.Get("state")
		if state == "" {
			state = "open"
		}
		var want []string
		if l := q.Get("labels"); l != "" {
			want = strings.Split(l, ",")
		}
		var items []any
		for _, i := range st.issues[repo] {
			if i.Title == "\x00deleted" || (state == "open" && i.Closed) || (state == "closed" && !i.Closed) {
				continue
			}
			ok := true
			for _, l := range want {
				has := false
				for _, il := range i.Labels {
					has = has || il == l
				}
				ok = ok && has
			}
			if ok {
				items = append(items, issueJSON(i))
			}
		}
		lo, hi := min((pg-1)*per, len(items)), min(pg*per, len(items))
		_ = json.NewEncoder(w).Encode(append([]any{}, items[lo:hi]...))
	case len(parts) == 3 && r.Method == http.MethodPost:
		var in struct {
			Title, Body string
			Labels      []string
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Title == "" {
			g.fail(w, http.StatusUnprocessableEntity, "bad issue")
			return
		}
		st.writes[repo]++
		i := &Issue{Repo: repo, Title: in.Title, Body: in.Body, Labels: in.Labels, Number: len(st.issues[repo]) + 1}
		st.issues[repo] = append(st.issues[repo], i)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(issueJSON(i))
	case len(parts) == 4 && r.Method == http.MethodPatch:
		n, _ := strconv.Atoi(parts[3])
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			g.fail(w, http.StatusUnprocessableEntity, "bad issue update")
			return
		}
		for _, i := range st.issues[repo] {
			if i.Number != n {
				continue
			}
			if i.Title == "\x00deleted" {
				g.fail(w, http.StatusGone, "This issue was deleted")
				return
			}
			st.writes[repo]++
			if b, ok := in["body"].(string); ok {
				i.Body = b
			}
			_ = json.NewEncoder(w).Encode(issueJSON(i))
			return
		}
		g.fail(w, http.StatusNotFound, "no issue %d", n)
	default:
		g.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

// handleLabelCreate serves POST /repos/o/r/labels: 201, or 422 when the label exists.
// The fake keeps issue labels as given on create, unlike GitHub, which drops labels
// that do not exist; the client therefore creates labels first, and this records them.
func (g *GitHub) handleLabelCreate(w http.ResponseWriter, r *http.Request, repo string) {
	var in struct{ Name string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.is()
	if st.disabled[repo] {
		g.fail(w, http.StatusGone, "Issues are disabled for this repo")
		return
	}
	if st.forbid[repo] {
		g.fail(w, http.StatusForbidden, "Resource not accessible by integration")
		return
	}
	if st.labels == nil {
		st.labels = map[string]bool{}
	}
	if st.labels[repo+"\x00"+in.Name] {
		g.fail(w, http.StatusUnprocessableEntity, "already_exists")
		return
	}
	st.labels[repo+"\x00"+in.Name] = true
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{}`))
}
