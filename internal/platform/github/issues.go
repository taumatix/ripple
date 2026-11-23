package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

var _ platform.IssueTracker = (*Client)(nil)

// maxIssuePages bounds the scan for the dashboard issue: 10 pages of 100 labelled issues.
const maxIssuePages = 10

type issueJSON struct {
	Number      int            `json:"number"`
	Body        string         `json:"body"`
	State       string         `json:"state"`
	PullRequest map[string]any `json:"pull_request"`
}

func (i issueJSON) issue() platform.Issue {
	return platform.Issue{Number: i.Number, Body: i.Body, Closed: i.State == "closed"}
}

// issuesUnavailable maps the statuses that mean "this repo's issues cannot be used by
// this credential": 410 (issues disabled), 403 (App lacks the Issues permission) and
// 404 (repo or issues hidden from the credential).
func issuesUnavailable(status int, err error) error {
	switch status {
	case http.StatusGone, http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("%w: %v", platform.ErrIssuesUnavailable, err)
	}
	return err
}

func (c *Client) issuesURL(repo model.Repo, suffix string) string {
	return fmt.Sprintf("%s/repos/%s/issues%s", strings.TrimRight(c.APIBase, "/"), repo.FullName, suffix)
}

// FindIssue scans the issues labelled label, open and closed, for one whose body
// starts with marker. Pull requests, which the issues endpoint also lists, are skipped.
func (c *Client) FindIssue(ctx context.Context, repo model.Repo, label, marker string) (platform.Issue, bool, error) {
	if err := checkRepo(repo); err != nil {
		return platform.Issue{}, false, err
	}
	for page := 1; page <= maxIssuePages; page++ {
		var items []issueJSON
		u := c.issuesURL(repo, fmt.Sprintf("?state=all&labels=%s&per_page=100&page=%d", url.QueryEscape(label), page))
		status, err := c.do(ctx, repo, http.MethodGet, u, nil, &items)
		if err != nil {
			return platform.Issue{}, false, issuesUnavailable(status, err)
		}
		for _, it := range items {
			if it.PullRequest == nil && strings.HasPrefix(it.Body, marker) {
				return it.issue(), true, nil
			}
		}
		if len(items) < 100 {
			break
		}
	}
	return platform.Issue{}, false, nil
}

// CreateIssue opens an issue with the given labels. GitHub's docs say labels that do
// not exist are silently dropped on create, and FindIssue looks the issue up by
// label, so each label is created first (best effort: 422 means it exists already).
func (c *Client) CreateIssue(ctx context.Context, repo model.Repo, spec platform.IssueSpec) (platform.Issue, error) {
	if err := checkRepo(repo); err != nil {
		return platform.Issue{}, err
	}
	for _, l := range spec.Labels {
		u := fmt.Sprintf("%s/repos/%s/labels", strings.TrimRight(c.APIBase, "/"), repo.FullName)
		if status, err := c.do(ctx, repo, http.MethodPost, u, map[string]any{"name": l, "color": "ededed", "description": "Managed by ripple"}, nil); err != nil && status != http.StatusUnprocessableEntity {
			return platform.Issue{}, issuesUnavailable(status, err)
		}
	}
	var out issueJSON
	payload := map[string]any{"title": spec.Title, "body": spec.Body}
	if len(spec.Labels) > 0 {
		payload["labels"] = spec.Labels
	}
	status, err := c.do(ctx, repo, http.MethodPost, c.issuesURL(repo, ""), payload, &out)
	if err != nil {
		return platform.Issue{}, issuesUnavailable(status, err)
	}
	return out.issue(), nil
}

// UpdateIssueBody patches only the body. A 404 or 410 on the issue itself means it
// was deleted (or issues were disabled since); the caller re-finds to tell which.
func (c *Client) UpdateIssueBody(ctx context.Context, repo model.Repo, number int, body string) error {
	if err := checkRepo(repo); err != nil {
		return err
	}
	status, err := c.do(ctx, repo, http.MethodPatch, c.issuesURL(repo, fmt.Sprintf("/%d", number)), map[string]any{"body": body}, nil)
	if err == nil {
		return nil
	}
	var rl *platform.RateLimitError
	if errors.As(err, &rl) {
		return err
	}
	switch status {
	case http.StatusNotFound, http.StatusGone:
		return fmt.Errorf("%w: %v", platform.ErrIssueGone, err)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %v", platform.ErrIssuesUnavailable, err)
	}
	return err
}
