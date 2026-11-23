package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

var _ platform.PRCloser = (*Client)(nil)

// CloseUntouchedPR declines pr and comments on it unless a person touched it.
// Touched means: the source branch is not in this repository; the pull request does
// not have exactly one commit whose author is ripple's identity; it has any
// comment or task; its activity log holds anything but "update" entries (a comment,
// an approval, a request for changes); or an update records a title, description or
// source commit other than the current one (a rename, an edited description, a
// rewritten or extended branch). Every read must succeed and nothing is declined
// on a failed one. Not detected: a reviewer added, a force push that the activity
// log does not record, and the committer of the commit (the API reports only the
// author). The decline comes first and the comment is best effort, so a failed
// decline leaves nothing to repeat on the retry. The branch is not deleted.
func (c *Client) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr platform.PR, comment string) (bool, error) {
	if err := checkRepo(repo); err != nil {
		return false, err
	}
	if pr.Number <= 0 {
		return false, fmt.Errorf("pull request has no number")
	}
	id := strconv.Itoa(pr.Number)
	// GET .../pullrequests/{pull_request_id}: state, title, description, source,
	// comment_count, task_count.
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-get
	var cur struct {
		State       string `json:"state"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Source      struct {
			Branch     struct{ Name string } `json:"branch"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"source"`
		CommentCount int `json:"comment_count"`
		TaskCount    int `json:"task_count"`
	}
	if _, err := c.do(ctx, http.MethodGet, c.apiURL(repo.FullName, "pullrequests", id), nil, &cur); err != nil {
		return false, err
	}
	if cur.State != "OPEN" || cur.Source.Branch.Name != pr.Head {
		return false, nil
	}
	if !strings.EqualFold(cur.Source.Repository.FullName, repo.FullName) || cur.CommentCount > 0 || cur.TaskCount > 0 {
		return false, nil
	}
	// GET .../pullrequests/{id}/commits: chronological. author.raw as "Name <email>"
	// is assumed (the vendored page does not show the commit object); anything else
	// reads as someone else's commit.
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-commits-get
	type commit struct {
		Hash   string `json:"hash"`
		Author struct {
			Raw string `json:"raw"`
		} `json:"author"`
	}
	var commits page[commit]
	if _, err := c.do(ctx, http.MethodGet, c.apiURL(repo.FullName, "pullrequests", id, "commits")+"?pagelen=2", nil, &commits); err != nil {
		return false, err
	}
	ours := gitws.AuthorEmail(gitws.Options{AuthorEmail: c.AuthorEmail})
	if len(commits.Values) != 1 || commits.Next != "" || !strings.HasSuffix(commits.Values[0].Author.Raw, "<"+ours+">") {
		return false, nil
	}
	// GET .../pullrequests/{id}/comments: any comment, including a deleted one.
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-comments-get
	var comments page[json.RawMessage]
	if _, err := c.do(ctx, http.MethodGet, c.apiURL(repo.FullName, "pullrequests", id, "comments")+"?pagelen=1", nil, &comments); err != nil {
		return false, err
	}
	if len(comments.Values) > 0 {
		return false, nil
	}
	// GET .../pullrequests/{id}/activity: comments, updates, approvals and requests
	// for changes. An update carries the title, description and source commit of the
	// moment. Pages are bounded: a busier pull request counts as touched.
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-activity-get
	entries, err := list[map[string]json.RawMessage](ctx, c, c.apiURL(repo.FullName, "pullrequests", id, "activity"), url.Values{"pagelen": {"50"}}, 5)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		for key, raw := range e {
			switch key {
			case "pull_request":
			case "update":
				var u struct {
					Title       string `json:"title"`
					Description string `json:"description"`
					Source      struct {
						Commit struct{ Hash string } `json:"commit"`
					} `json:"source"`
				}
				if err := json.Unmarshal(raw, &u); err != nil {
					return false, fmt.Errorf("decode activity update: %w", err)
				}
				if u.Title != cur.Title || u.Description != cur.Description || u.Source.Commit.Hash != commits.Values[0].Hash {
					return false, nil
				}
			default:
				return false, nil
			}
		}
	}
	// POST .../pullrequests/{id}/decline, then POST .../comments {"content":{"raw":...}}.
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-decline-post
	// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-comments-post
	if _, err := c.do(ctx, http.MethodPost, c.apiURL(repo.FullName, "pullrequests", id, "decline"), nil, nil); err != nil {
		return false, err
	}
	body := map[string]any{"content": map[string]string{"raw": comment}}
	if _, err := c.do(ctx, http.MethodPost, c.apiURL(repo.FullName, "pullrequests", id, "comments"), body, nil); err != nil && c.Log != nil {
		c.Log.Warn("commenting on the declined superseded pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
	}
	return true, nil
}
