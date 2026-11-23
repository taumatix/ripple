package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

var _ platform.TagSource = (*Client)(nil)

// maxTagPages bounds ListTags: 10 pages of 100 tags.
const maxTagPages = 10

func (c *Client) repoURL(target, suffix string) string {
	return fmt.Sprintf("%s/repos/%s%s", strings.TrimRight(c.APIBase, "/"), target, suffix)
}

// ListTags pages through GET /repos/{target}/tags, which is not ordered.
func (c *Client) ListTags(ctx context.Context, via model.Repo, target string) ([]string, error) {
	if err := checkRepo(via); err != nil {
		return nil, err
	}
	var names []string
	for page := 1; page <= maxTagPages; page++ {
		var items []struct {
			Name string `json:"name"`
		}
		if _, err := c.do(ctx, via, http.MethodGet, c.repoURL(target, fmt.Sprintf("/tags?per_page=100&page=%d", page)), nil, &items); err != nil {
			return nil, err
		}
		for _, it := range items {
			names = append(names, it.Name)
		}
		if len(items) < 100 {
			break
		}
	}
	return names, nil
}

type refObject struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

// TagCommit resolves refs/tags/{tag} and, when it names an annotated tag object,
// follows it (git/tags/{sha}) to the commit it tags, which is what `uses:` pins.
func (c *Client) TagCommit(ctx context.Context, via model.Repo, target, tag string) (string, error) {
	if err := checkRepo(via); err != nil {
		return "", err
	}
	var ref refObject
	if _, err := c.do(ctx, via, http.MethodGet, c.repoURL(target, "/git/ref/tags/"+url.PathEscape(tag)), nil, &ref); err != nil {
		return "", err
	}
	// An annotated tag may point at another tag: bounded, so a cycle cannot loop.
	for i := 0; ref.Object.Type == "tag" && i < 5; i++ {
		var next refObject
		if _, err := c.do(ctx, via, http.MethodGet, c.repoURL(target, "/git/tags/"+ref.Object.SHA), nil, &next); err != nil {
			return "", err
		}
		ref = next
	}
	if ref.Object.Type != "commit" || ref.Object.SHA == "" {
		return "", fmt.Errorf("tag %s of %s does not point at a commit (%s)", tag, target, ref.Object.Type)
	}
	return ref.Object.SHA, nil
}

// ReleaseOf reads GET /repos/{target}/releases/tags/{tag}: 404 means the tag has no
// release, and a draft or prerelease is reported as none, since neither is stable.
func (c *Client) ReleaseOf(ctx context.Context, via model.Repo, target, tag string) (platform.TagRelease, bool, error) {
	if err := checkRepo(via); err != nil {
		return platform.TagRelease{}, false, err
	}
	var rel struct {
		HTMLURL    string    `json:"html_url"`
		Published  time.Time `json:"published_at"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
	}
	status, err := c.do(ctx, via, http.MethodGet, c.repoURL(target, "/releases/tags/"+url.PathEscape(tag)), nil, &rel)
	if status == http.StatusNotFound {
		return platform.TagRelease{}, false, nil
	}
	if err != nil {
		return platform.TagRelease{}, false, err
	}
	if rel.Draft || rel.Prerelease {
		return platform.TagRelease{}, false, nil
	}
	return platform.TagRelease{URL: rel.HTMLURL, Published: rel.Published}, true, nil
}
