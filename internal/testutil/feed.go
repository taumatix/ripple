package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

type indexEntry struct {
	Path, Version string
	At            time.Time
}

// Index is a fake index.golang.org: GET /index?since=<RFC3339>&limit=<n> answers
// new-line delimited JSON, oldest first, entries with Timestamp >= since.
type Index struct {
	URL string

	mu       sync.Mutex
	entries  []indexEntry
	requests int
}

func NewIndex(t *testing.T) *Index {
	x := &Index{}
	srv := httptest.NewServer(http.HandlerFunc(x.serve))
	t.Cleanup(srv.Close)
	x.URL = srv.URL
	return x
}

func (x *Index) Publish(path, version string, at time.Time) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.entries = append(x.entries, indexEntry{path, version, at.UTC()})
}

// Requests is the number of /index calls served.
func (x *Index) Requests() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.requests
}

func (x *Index) serve(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.requests++
	if r.URL.Path != "/index" {
		http.NotFound(w, r)
		return
	}
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		var err error
		if since, err = time.Parse(time.RFC3339Nano, s); err != nil {
			http.Error(w, "bad since", http.StatusBadRequest)
			return
		}
	}
	limit := 2000
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l < limit {
		limit = l
	}
	var out []indexEntry
	for _, e := range x.entries {
		if !e.At.Before(since) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	for _, e := range out {
		// The documented line format: {"Path":..,"Version":..,"Timestamp":..}
		fmt.Fprintf(w, "{\"Path\":%q,\"Version\":%q,\"Timestamp\":%q}\n", e.Path, e.Version, e.At.Format("2006-01-02T15:04:05.999999999Z"))
	}
}
