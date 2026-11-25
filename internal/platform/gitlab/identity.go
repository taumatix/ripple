package gitlab

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

var _ platform.RepoIdentifier = (*Client)(nil)

// RepoHasID is GET /projects/{id} (api_projects.md "Get a single project": the id is the
// numeric one): whether the project with that immutable id is currently at repo's path.
// A 404 (no such id, or not visible to the credential) is "no", not an error.
func (c *Client) RepoHasID(ctx context.Context, repo model.Repo, id int64) (bool, error) {
	var out struct {
		Path string `json:"path_with_namespace"`
	}
	status, err := c.doRepo(ctx, repo, http.MethodGet, c.api()+"/projects/"+strconv.FormatInt(id, 10), nil, &out)
	if status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(out.Path, repo.FullName), nil
}
