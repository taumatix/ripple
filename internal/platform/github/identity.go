package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

var _ platform.RepoIdentifier = (*Client)(nil)

// RepoHasID is GET /repositories/{id}: whether the repository with that immutable id is
// currently called repo.FullName. A 404 (no such id, or not visible to the credential)
// is "no", not an error.
func (c *Client) RepoHasID(ctx context.Context, repo model.Repo, id int64) (bool, error) {
	if err := checkRepo(repo); err != nil {
		return false, err
	}
	var out struct {
		FullName string `json:"full_name"`
	}
	u := fmt.Sprintf("%s/repositories/%d", strings.TrimRight(c.APIBase, "/"), id)
	status, err := c.do(ctx, repo, http.MethodGet, u, nil, &out)
	if status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(out.FullName, repo.FullName), nil
}
