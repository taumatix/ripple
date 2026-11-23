package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type osvRecord struct {
	ID       string
	Modified time.Time
	JSON     []byte
}

// OSV is a fake of the two OSV surfaces ripple reads: the bucket's Go/modified_id.csv
// (newest first, as recorded in docs/vendor/osv/samples) and GET /v1/vulns/{id}.
// Publish takes literal record JSON, so tests use the payloads OSV itself served.
type OSV struct {
	URL string // serves both /osv-vulnerabilities/... and /v1/...

	mu      sync.Mutex
	records []osvRecord
	down    bool
	csvReqs int
	vulnReq int
}

func NewOSV(t *testing.T) *OSV {
	o := &OSV{}
	srv := httptest.NewServer(http.HandlerFunc(o.serve))
	t.Cleanup(srv.Close)
	o.URL = srv.URL
	return o
}

// DumpURL and APIURL are the values for advisory.Client.
func (o *OSV) DumpURL() string { return o.URL + "/osv-vulnerabilities" }
func (o *OSV) APIURL() string  { return o.URL }

// Publish adds (or replaces) a record modified at the given time.
func (o *OSV) Publish(id string, modified time.Time, record []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.records {
		if o.records[i].ID == id {
			o.records = append(o.records[:i], o.records[i+1:]...)
			break
		}
	}
	o.records = append(o.records, osvRecord{id, modified.UTC(), record})
}

// SetDown makes every request answer 503 while true.
func (o *OSV) SetDown(down bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.down = down
}

// Requests counts CSV and record requests served (including those answered 503).
func (o *OSV) Requests() (csv, vulns int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.csvReqs, o.vulnReq
}

func (o *OSV) serve(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch {
	case r.URL.Path == "/osv-vulnerabilities/Go/modified_id.csv":
		o.csvReqs++
	case strings.HasPrefix(r.URL.Path, "/v1/vulns/"):
		o.vulnReq++
	default:
		http.NotFound(w, r)
		return
	}
	if o.down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/vulns/") {
		id := strings.TrimPrefix(r.URL.Path, "/v1/vulns/")
		for _, rec := range o.records {
			if rec.ID == id {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(rec.JSON)
				return
			}
		}
		http.Error(w, `{"code":5,"message":"Bug not found."}`, http.StatusNotFound)
		return
	}
	recs := append([]osvRecord(nil), o.records...)
	sort.Slice(recs, func(i, j int) bool {
		if !recs[i].Modified.Equal(recs[j].Modified) {
			return recs[i].Modified.After(recs[j].Modified)
		}
		return recs[i].ID > recs[j].ID
	})
	for _, rec := range recs {
		fmt.Fprintf(w, "%s,%s\n", rec.Modified.Format(time.RFC3339Nano), rec.ID)
	}
}
