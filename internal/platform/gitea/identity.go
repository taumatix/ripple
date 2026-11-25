package gitea

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

var _ platform.RepoIdentifier = (*Client)(nil)

// RepoHasID is GET /repositories/{id} (Gitea's repoGetByID; Forgejo keeps the route):
// whether the repository with that immutable id is currently called repo.FullName. A 404
// (no such id, or not visible to the credential) is "no", not an error. The route and the
// payload's `repository.id` are not in the vendored excerpts: unverified against a real
// instance, like the rest of this host.
func (c *Client) RepoHasID(ctx context.Context, repo model.Repo, id int64) (bool, error) {
	if _, _, err := checkRepo(repo); err != nil {
		return false, err
	}
	var out struct {
		FullName string `json:"full_name"`
	}
	_, err := c.do(ctx, repo, http.MethodGet, c.api("/repositories/"+strconv.FormatInt(id, 10)), nil, &out)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return strings.EqualFold(out.FullName, repo.FullName), nil
}
