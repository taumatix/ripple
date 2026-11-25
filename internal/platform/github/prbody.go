package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

var _ platform.PRBodyEditor = (*Client)(nil)

// EditPRBody is PATCH /pulls/{n} with the new body, after reading the pull request:
// it must still be open on the branch given and its description must have no edit
// by anyone but this token's identity (the GraphQL edit history names each editor).
func (c *Client) EditPRBody(ctx context.Context, repo model.Repo, pr platform.PR, edit func(string) (string, bool)) (bool, error) {
	if err := checkRepo(repo); err != nil {
		return false, err
	}
	base := fmt.Sprintf("%s/repos/%s/pulls/%d", strings.TrimRight(c.APIBase, "/"), repo.FullName, pr.Number)
	var cur struct {
		State  string `json:"state"`
		Body   string `json:"body"`
		NodeID string `json:"node_id"`
		Head   struct {
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}
	if _, err := c.do(ctx, repo, http.MethodGet, base, nil, &cur); err != nil {
		return false, err
	}
	if cur.State != "open" || cur.Head.Ref != pr.Head || !strings.EqualFold(cur.Head.Repo.FullName, repo.FullName) {
		return false, nil
	}
	body, changed := edit(cur.Body)
	if !changed {
		return false, nil
	}
	if cur.NodeID == "" {
		return false, fmt.Errorf("pull request has no node id to read its description edits")
	}
	if person, err := c.editedByPerson(ctx, repo, cur.NodeID); err != nil || person {
		return false, err
	}
	if _, err := c.do(ctx, repo, http.MethodPatch, base, map[string]string{"body": body}, nil); err != nil {
		return false, err
	}
	return true, nil
}
