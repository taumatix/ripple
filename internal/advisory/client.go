package advisory

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
)

const (
	// DefaultDumpURL is the public bucket the OSV project documents for bulk access.
	DefaultDumpURL = "https://storage.googleapis.com/osv-vulnerabilities"
	// DefaultAPIURL is the OSV API; GET /v1/vulns/{id} and POST /v1/querybatch are used.
	DefaultAPIURL = "https://api.osv.dev"

	// defaultMaxLines bounds one Modified call. The Go file is ~10k lines (~450KB) on
	// 2026-11-23, so this reads all of it on a first poll after a long outage; maxCSVBytes
	// still bounds the read.
	defaultMaxLines = 20000
	maxCSVBytes     = 4 << 20
	maxVulnBytes    = 1 << 20

	// querybatch limits: OSV documents at most 1000 queries per request.
	maxBatchQueries   = 1000
	maxBatchBytes     = 8 << 20
	maxBatchPageRound = 5
	userAgent         = "ripple-osv-poller (+https://github.com/taumatix/ripple)"
)

// ErrNotFound is returned by Vuln for an id OSV does not know (HTTP 404).
var ErrNotFound = errors.New("advisory: not found")

var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Position is a point in OSV's modification order: the bucket lists records by modified
// time, and several records share a timestamp (a GHSA and its GO alias), so the id breaks ties.
type Position struct {
	Time time.Time
	ID   string
}

// String is the form persisted in the cursor store.
func (p Position) String() string {
	if p.Time.IsZero() {
		return ""
	}
	return p.Time.UTC().Format(time.RFC3339Nano) + "," + p.ID
}

// ParsePosition reads what String wrote; "" is the zero Position.
func ParsePosition(s string) (Position, error) {
	if s == "" {
		return Position{}, nil
	}
	ts, id, ok := strings.Cut(s, ",")
	if !ok {
		return Position{}, fmt.Errorf("advisory: bad position %q", s)
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Position{}, fmt.Errorf("advisory: bad position %q: %w", s, err)
	}
	return Position{Time: t, ID: id}, nil
}

// before reports whether a record at (t,id) is later than p in modification order (p comes before it).
func (p Position) before(t time.Time, id string) bool {
	if t.After(p.Time) {
		return true
	}
	return t.Equal(p.Time) && id > p.ID
}

// Entry is one line of modified_id.csv.
type Entry struct {
	Time time.Time
	ID   string
}

// Client reads OSV. Every call is bounded: a timeout, a body cap, and for the CSV a line cap.
type Client struct {
	DumpURL  string       // default DefaultDumpURL
	APIURL   string       // default DefaultAPIURL
	HTTP     *http.Client // default: 20s timeout
	MaxLines int          // CSV lines read per Modified call; default 20000
	// Retry bounds the waiting on a 429 or a 503 with Retry-After; the zero value is
	// three attempts, none of them waited on longer than 30s.
	Retry httpretry.Policy

	// ecosystem overrides Ecosystem in QueryBatch; a test seam so a live test can page through an
	// ecosystem whose packages have more than 1000 records (no Go package does).
	ecosystem string
}

func (c *Client) httpc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	return c.Retry.Do(ctx, c.httpc(), func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		return req, nil
	})
}

// Modified returns the Go-ecosystem records modified after pos, oldest first. The bucket
// file is newest first, so reading stops as soon as it reaches pos: a quiet poll reads a
// few lines, not the whole file. truncated is true when the line or byte cap was hit
// before pos was reached; the entries returned are then the newest ones and older ones
// in between are skipped (the caller decides how loudly to say so).
func (c *Client) Modified(ctx context.Context, pos Position) (entries []Entry, truncated bool, err error) {
	base := c.DumpURL
	if base == "" {
		base = DefaultDumpURL
	}
	resp, err := c.get(ctx, strings.TrimRight(base, "/")+"/"+Ecosystem+"/modified_id.csv")
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("advisory: modified_id.csv: %s", resp.Status)
	}
	maxLines := c.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxCSVBytes))
	sc.Buffer(make([]byte, 0, 4096), 4096)
	lines, reached, capped := 0, false, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if lines >= maxLines {
			capped = true
			break
		}
		lines++
		ts, id, ok := strings.Cut(line, ",")
		t, terr := time.Parse(time.RFC3339Nano, ts)
		if !ok || terr != nil || !idRE.MatchString(id) {
			return nil, false, fmt.Errorf("advisory: unparseable modified_id.csv line %d", lines)
		}
		if t.Before(pos.Time) {
			reached = true
			break
		}
		if pos.before(t, id) {
			entries = append(entries, Entry{Time: t, ID: id})
		} else if t.Equal(pos.Time) && id == pos.ID {
			reached = true
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, fmt.Errorf("advisory: reading modified_id.csv: %w", err)
	}
	truncated = capped && !reached
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].Time.Equal(entries[j].Time) {
			return entries[i].Time.Before(entries[j].Time)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, truncated, nil
}

// Vuln fetches one record with GET /v1/vulns/{id}. An unknown id is ErrNotFound.
func (c *Client) Vuln(ctx context.Context, id string) (*Vuln, error) {
	if !idRE.MatchString(id) {
		return nil, fmt.Errorf("advisory: refusing id %q", id)
	}
	base := c.APIURL
	if base == "" {
		base = DefaultAPIURL
	}
	resp, err := c.get(ctx, strings.TrimRight(base, "/")+"/v1/vulns/"+id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("advisory: GET /v1/vulns/%s: %s", id, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVulnBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxVulnBytes {
		return nil, fmt.Errorf("advisory: record %s exceeds %d bytes", id, maxVulnBytes)
	}
	var v Vuln
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("advisory: record %s: %w", id, err)
	}
	if v.ID != id {
		return nil, fmt.Errorf("advisory: asked for %s, got %q", id, v.ID)
	}
	return &v, nil
}

// Query asks which advisories affect Name at Version. Version may carry a leading "v".
type Query struct {
	Name    string // a module path, "stdlib" or "toolchain"
	Version string
}

type batchQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version   string `json:"version"`
	PageToken string `json:"page_token,omitempty"`
}

type batchResult struct {
	Vulns []struct {
		ID string `json:"id"`
	} `json:"vulns"`
	NextPageToken string `json:"next_page_token"`
}

// QueryBatch asks POST /v1/querybatch which advisories affect each query and returns
// the ids per query, in the order of qs. It sends at most 1000 queries per request and
// follows next_page_token for a bounded number of rounds; a query whose pages outlast
// that is returned with the ids it got. Only ids come back: fetch a record with Vuln.
func (c *Client) QueryBatch(ctx context.Context, qs []Query) ([][]string, error) {
	out := make([][]string, len(qs))
	for lo := 0; lo < len(qs); lo += maxBatchQueries {
		hi := min(lo+maxBatchQueries, len(qs))
		if err := c.queryChunk(ctx, qs[lo:hi], out[lo:hi]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Client) queryChunk(ctx context.Context, qs []Query, out [][]string) error {
	base := c.APIURL
	if base == "" {
		base = DefaultAPIURL
	}
	pending := make([]batchQuery, len(qs))
	index := make([]int, len(qs))
	for i, q := range qs {
		pending[i].Package.Name = q.Name
		pending[i].Package.Ecosystem = cmp.Or(c.ecosystem, Ecosystem)
		pending[i].Version = strings.TrimPrefix(q.Version, "v")
		index[i] = i
	}
	seen := make([]map[string]bool, len(qs))
	for round := 0; len(pending) > 0 && round < maxBatchPageRound; round++ {
		body, err := json.Marshal(map[string]any{"queries": pending})
		if err != nil {
			return err
		}
		resp, err := c.Retry.Do(ctx, c.httpc(), func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/querybatch", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("User-Agent", userAgent)
			req.Header.Set("Content-Type", "application/json")
			return req, nil
		})
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBatchBytes+1))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("advisory: POST /v1/querybatch: %s", resp.Status)
		}
		if len(raw) > maxBatchBytes {
			return fmt.Errorf("advisory: querybatch response exceeds %d bytes", maxBatchBytes)
		}
		var parsed struct {
			Results []batchResult `json:"results"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return fmt.Errorf("advisory: querybatch response: %w", err)
		}
		if len(parsed.Results) != len(pending) {
			return fmt.Errorf("advisory: querybatch answered %d of %d queries", len(parsed.Results), len(pending))
		}
		var next []batchQuery
		var nextIndex []int
		for i, r := range parsed.Results {
			qi := index[i]
			for _, v := range r.Vulns {
				if !idRE.MatchString(v.ID) {
					return fmt.Errorf("advisory: querybatch returned id %q", v.ID)
				}
				if seen[qi] == nil {
					seen[qi] = map[string]bool{}
				}
				if !seen[qi][v.ID] {
					seen[qi][v.ID] = true
					out[qi] = append(out[qi], v.ID)
				}
			}
			if r.NextPageToken != "" {
				q := pending[i]
				q.PageToken = r.NextPageToken
				next = append(next, q)
				nextIndex = append(nextIndex, qi)
			}
		}
		pending, index = next, nextIndex
	}
	return nil
}
