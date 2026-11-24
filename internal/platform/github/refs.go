package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// readRepo is GET /repos/{target}{suffix} on the host, or on UpstreamAPIBase when the host
// does not have the repository and one is configured. The upstream is asked for nothing else.
func (c *Client) readRepo(ctx context.Context, via model.Repo, target, suffix string, out any) (int, error) {
	status, err := c.doWith(ctx, via, http.MethodGet, c.repoURL(target, suffix), nil, out, true)
	if status != http.StatusNotFound || c.UpstreamAPIBase == "" {
		return status, err
	}
	// A 404 may also be a tag or release the host's own copy lacks: only a repository the
	// host does not have is read from upstream, never a different one's tag or release.
	if suffix != "" {
		if pstatus, perr := c.doWith(ctx, via, http.MethodGet, c.repoURL(target, "/tags?per_page=1"), nil, &[]struct{}{}, true); perr == nil {
			return status, err
		} else if pstatus != http.StatusNotFound {
			return pstatus, perr
		}
	}
	return c.upstreamGet(ctx, fmt.Sprintf("%s/repos/%s%s", strings.TrimRight(c.UpstreamAPIBase, "/"), target, suffix), out)
}

// upstreamGet reads public data from UpstreamAPIBase. It never sends an installation
// token: the host's credentials stay with the host.
func (c *Client) upstreamGet(ctx context.Context, rawURL string, out any) (int, error) {
	var (
		resp *http.Response
		req  *http.Request
		lw   limitWaiter
	)
	for {
		var err error
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return 0, err
		}
		setHeaders(req, c.Versions.Current())
		if c.UpstreamToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.UpstreamToken)
		}
		resp, err = c.client().Do(req)
		if err != nil {
			return 0, fmt.Errorf("github upstream GET %s: %w", req.URL.Path, err)
		}
		if !isRateLimited(resp) {
			break
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if err := c.wait(ctx, &lw, req, resp); err != nil {
			return resp.StatusCode, err
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("github upstream GET %s: status %d: %s", req.URL.Path, resp.StatusCode, snippet(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("github upstream GET %s: decode: %w", req.URL.Path, err)
	}
	return resp.StatusCode, nil
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
		if _, err := c.readRepo(ctx, via, target, fmt.Sprintf("/tags?per_page=100&page=%d", page), &items); err != nil {
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
	if _, err := c.readRepo(ctx, via, target, "/git/ref/tags/"+url.PathEscape(tag), &ref); err != nil {
		return "", err
	}
	// An annotated tag may point at another tag: bounded, so a cycle cannot loop.
	for i := 0; ref.Object.Type == "tag" && i < 5; i++ {
		var next refObject
		if _, err := c.readRepo(ctx, via, target, "/git/tags/"+ref.Object.SHA, &next); err != nil {
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
	status, err := c.readRepo(ctx, via, target, "/releases/tags/"+url.PathEscape(tag), &rel)
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
