package bitbucketserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

var _ platform.PRCloser = (*Client)(nil)

// CloseUntouchedPR declines pr and comments on it unless a person touched it.
// Touched means: the source branch is not in this repository; the pull request does
// not have exactly one commit whose author and committer are both ripple's
// identity; or its activity log holds anything but the OPENED entry (a comment, an
// approval, a review, an edit, a rescope, a reviewer change). The schema does not
// mark system activity, so every other action counts as a person's. Every read must
// succeed and nothing is declined on a failed one. Not detected: a change the
// activity log does not record. The decline carries the version read first, so a
// concurrent edit makes it answer 409, which is returned as an error and leaves
// the pull request open. The decline comes first and the comment is best effort.
// The branch is not deleted.
func (c *Client) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr platform.PR, comment string) (bool, error) {
	key, slug, err := split(repo)
	if err != nil {
		return false, err
	}
	if pr.Number <= 0 {
		return false, fmt.Errorf("pull request has no number")
	}
	base := repoPath(key, slug) + "/pull-requests/" + strconv.Itoa(pr.Number)
	// GET .../pull-requests/{pullRequestId}: state, version, fromRef.
	// https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/#api-api-latest-projects-projectkey-repos-repositoryslug-pull-requests-pullrequestid-get
	var cur struct {
		prJSON
		State string `json:"state"`
	}
	if err := c.do(ctx, repo, http.MethodGet, c.api(base), nil, &cur); err != nil {
		return false, err
	}
	if cur.State != "OPEN" || strings.TrimPrefix(cur.FromRef.ID, branches) != pr.Head {
		return false, nil
	}
	if !strings.EqualFold(cur.FromRef.Repository.Slug, slug) || !strings.EqualFold(cur.FromRef.Repository.Project.Key, key) {
		return false, nil
	}
	// GET .../pull-requests/{id}/commits: RestCommit with author and committer.
	// https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/#api-api-latest-projects-projectkey-repos-repositoryslug-pull-requests-pullrequestid-commits-get
	var commits struct {
		Values []struct {
			Author    struct{ EmailAddress string } `json:"author"`
			Committer struct{ EmailAddress string } `json:"committer"`
		} `json:"values"`
		IsLastPage bool `json:"isLastPage"`
	}
	if err := c.do(ctx, repo, http.MethodGet, c.api(base+"/commits?limit=2"), nil, &commits); err != nil {
		return false, err
	}
	ours := gitws.AuthorEmail(gitws.Options{AuthorEmail: c.AuthorEmail})
	if len(commits.Values) != 1 || !commits.IsLastPage ||
		commits.Values[0].Author.EmailAddress != ours || commits.Values[0].Committer.EmailAddress != ours {
		return false, nil
	}
	// GET .../pull-requests/{id}/activities: RestPullRequestActivity.action
	// (OPENED, COMMENTED, APPROVED, UPDATED, RESCOPED, ...).
	// https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/#api-api-latest-projects-projectkey-repos-repositoryslug-pull-requests-pullrequestid-activities-get
	touched := false
	err = c.paged(ctx, repo, base+"/activities", func(raw json.RawMessage) error {
		var acts []struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(raw, &acts); err != nil {
			return fmt.Errorf("decode activities: %w", err)
		}
		for _, a := range acts {
			if a.Action != "OPENED" {
				touched = true
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if touched {
		return false, nil
	}
	// POST .../pull-requests/{id}/decline?version=N, then POST .../comments {"text":...}.
	// https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/#api-api-latest-projects-projectkey-repos-repositoryslug-pull-requests-pullrequestid-decline-post
	// https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/#api-api-latest-projects-projectkey-repos-repositoryslug-pull-requests-pullrequestid-comments-post
	if err := c.do(ctx, repo, http.MethodPost, c.api(base+"/decline?version="+strconv.Itoa(cur.Version)), nil, nil); err != nil {
		return false, err
	}
	if err := c.do(ctx, repo, http.MethodPost, c.api(base+"/comments"), map[string]string{"text": comment}, nil); err != nil && c.Log != nil {
		c.Log.Warn("commenting on the declined superseded pull request failed", "repo", repo.FullName, "pr", pr.Number, "err", err)
	}
	return true, nil
}
