package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// Head polling reads every installed repository's default-branch head so the app can re-scan a
// repo whose push webhook never arrived (app.InstalledHeadLister). The repository set comes
// from the App's installations (GET /app/installations, then GET /installation/repositories per
// installation); the heads come from GraphQL, headBatch repositories per request. Sources and
// limits: docs/vendor/github/graphql-repository-heads.md.
//
// headBatch is 100: GraphQL connections are limited to 1-100 items per request and a query's
// cost is its connection requests divided by 100, so an aliased read of 100 repositories has no
// connection and costs the minimum (1 point, assumed), far below the 500,000 node limit and the
// 5,000 points per hour per installation. A bigger batch would save requests nobody is short
// of while moving closer to the 10 second request timeout (502/504).
const headBatch = 100

// headReserve is the rateLimit.remaining below which a round stops reading more batches and
// waits for the window to reset, so polling never spends the points the scans need.
const headReserve = 100

// defaultHeadEvery is HeadEvery's zero value: the app discovers about every minute, and
// rereading every installation, repository page and head that often would spend more of the
// REST allowance (5,000 requests per hour per installation) than a missed push is worth.
const defaultHeadEvery = 10 * time.Minute

// maxRepoPages bounds the repositories read per installation (100 per page).
const maxRepoPages = 1000

type installationLister interface {
	Installations(context.Context) ([]Installation, error)
}

// ListInstalledRepoHeads is the default-branch head of every active repository of every
// unsuspended installation (app.InstalledHeadLister). The answer is remembered for HeadEvery
// and returned as it was until then, so a discovery in between costs no request. A repository
// with no commits, one the read could not resolve, or an archived or disabled one is reported
// with no head or left out. A rate-limited answer (429, a secondary limit, an exhausted GraphQL
// budget) ends the round with *platform.RateLimitError and nothing is remembered, so the next
// discovery reads again; any other failure does the same, because a partial listing would make
// the app forget the heads of the repositories missing from it. A token source that cannot list
// installations (a personal access token) has nothing to poll.
func (c *Client) ListInstalledRepoHeads(ctx context.Context) ([]platform.RepoHead, error) {
	il, ok := c.Tokens.(installationLister)
	if !ok {
		return nil, nil
	}
	c.headMu.Lock()
	defer c.headMu.Unlock()
	every := c.HeadEvery
	if every <= 0 {
		every = defaultHeadEvery
	}
	if c.headSnap != nil && c.now().Sub(c.headAt) < every {
		return append([]platform.RepoHead(nil), c.headSnap...), nil
	}
	insts, err := il.Installations(ctx)
	if err != nil {
		return nil, err
	}
	out := []platform.RepoHead{}
	for _, in := range insts {
		if in.Suspended {
			continue
		}
		hs, err := c.installationHeads(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, hs...)
	}
	c.headSnap, c.headAt = out, c.now()
	return append([]platform.RepoHead(nil), out...), nil
}

// held is the rate-limit error for an installation still held back by an earlier answer.
func (c *Client) held(inst int64) error {
	if until := c.RateLimitedUntil(model.Repo{InstallationID: inst}); until.After(c.now()) {
		return &platform.RateLimitError{Until: until}
	}
	return nil
}

func (c *Client) installationHeads(ctx context.Context, inst int64) ([]platform.RepoHead, error) {
	if err := c.held(inst); err != nil {
		return nil, err
	}
	at := model.Repo{InstallationID: inst}
	type listed struct{ name, def string }
	var repos []listed
	for page := 1; page <= maxRepoPages; page++ {
		var resp struct {
			Repositories []struct {
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
				Archived      bool   `json:"archived"`
				Disabled      bool   `json:"disabled"`
			} `json:"repositories"`
		}
		u := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", strings.TrimRight(c.APIBase, "/"), page)
		if _, err := c.doWith(ctx, at, http.MethodGet, u, nil, &resp, false); err != nil {
			return nil, err
		}
		for _, r := range resp.Repositories {
			if !r.Archived && !r.Disabled && repoName.MatchString(r.FullName) {
				repos = append(repos, listed{r.FullName, r.DefaultBranch})
			}
		}
		if len(resp.Repositories) < 100 {
			break
		}
	}
	var out []platform.RepoHead
	for from := 0; from < len(repos); from += headBatch {
		batch := repos[from:min(from+headBatch, len(repos))]
		names := make([]string, len(batch))
		for i, r := range batch {
			names[i] = r.name
		}
		heads, remaining, resetAt, err := c.readHeads(ctx, at, names)
		if err != nil {
			return nil, err
		}
		for i, r := range batch {
			h := heads[i]
			if h.branch == "" {
				h.branch = r.def
			}
			out = append(out, platform.RepoHead{FullName: r.name, InstallationID: inst, DefaultBranch: h.branch, Head: h.oid})
		}
		if remaining >= 0 && remaining < headReserve && from+headBatch < len(repos) {
			c.noteLimit(inst, resetAt)
			return nil, &platform.RateLimitError{Until: resetAt}
		}
	}
	return out, nil
}

type headRead struct{ branch, oid string }

// readHeads reads the default branch name and head of names in one GraphQL request: an aliased
// repository(owner:, name:) per name, with the names passed as variables so they are never
// spliced into the query text. It returns the rateLimit.remaining and resetAt of the answer
// (-1 when it carried none).
func (c *Client) readHeads(ctx context.Context, at model.Repo, names []string) ([]headRead, int, time.Time, error) {
	var q strings.Builder
	vars := make(map[string]string, 2*len(names))
	var decl, body strings.Builder
	for i, n := range names {
		owner, name, _ := strings.Cut(n, "/")
		is := strconv.Itoa(i)
		vars["o"+is], vars["n"+is] = owner, name
		if i > 0 {
			decl.WriteString(",")
		}
		decl.WriteString("$o" + is + ":String!,$n" + is + ":String!")
		body.WriteString("r" + is + ":repository(owner:$o" + is + ",name:$n" + is + "){defaultBranchRef{name target{oid}}}")
	}
	q.WriteString("query(" + decl.String() + "){" + body.String() + "rateLimit{cost remaining resetAt}}")
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	_, err := c.doWith(ctx, at, http.MethodPost, c.graphQLURL(), map[string]any{"query": q.String(), "variables": vars}, &resp, false)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	for _, e := range resp.Errors {
		// The primary limit answers 200 with an error (assumed type RATE_LIMITED, unverified).
		if e.Type == "RATE_LIMITED" {
			until := c.now().Add(time.Minute)
			c.noteLimit(at.InstallationID, until)
			return nil, 0, time.Time{}, &platform.RateLimitError{Until: until}
		}
	}
	if resp.Data == nil {
		msg := "no data"
		if len(resp.Errors) > 0 {
			msg = resp.Errors[0].Message
		}
		return nil, 0, time.Time{}, fmt.Errorf("github graphql heads: %s", msg)
	}
	remaining, resetAt := -1, time.Time{}
	if raw, ok := resp.Data["rateLimit"]; ok {
		var rl struct {
			Remaining int       `json:"remaining"`
			ResetAt   time.Time `json:"resetAt"`
		}
		if json.Unmarshal(raw, &rl) == nil {
			remaining, resetAt = rl.Remaining, rl.ResetAt
		}
	}
	out := make([]headRead, len(names))
	for i := range names {
		raw, ok := resp.Data["r"+strconv.Itoa(i)]
		if !ok || string(raw) == "null" {
			continue // unreadable this round: reported with no head, so the app keeps what it had
		}
		var r struct {
			Ref *struct {
				Name   string `json:"name"`
				Target *struct {
					Oid string `json:"oid"`
				} `json:"target"`
			} `json:"defaultBranchRef"`
		}
		if json.Unmarshal(raw, &r) != nil || r.Ref == nil {
			continue // an empty repository has no default branch ref (assumed, unverified)
		}
		out[i].branch = r.Ref.Name
		if r.Ref.Target != nil {
			out[i].oid = r.Ref.Target.Oid
		}
	}
	return out, remaining, resetAt, nil
}
