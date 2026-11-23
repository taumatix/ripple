package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
)

type osvRecord struct {
	ID       string
	Modified time.Time
	JSON     []byte
}

// OSV is a fake of the OSV surfaces ripple reads: the bucket's Go/modified_id.csv
// (newest first, as recorded in docs/vendor/osv/samples), GET /v1/vulns/{id} and
// POST /v1/querybatch. Publish takes literal record JSON, so tests use the payloads OSV
// itself served. The fake querybatch answers by running the records through
// advisory.Vuln's evaluator: what is verified against it is the wire format and the
// request volume, not OSV's own matching, which only the live check can show.
type OSV struct {
	URL string // serves both /osv-vulnerabilities/... and /v1/...

	mu      sync.Mutex
	records []osvRecord
	down    bool
	csvReqs int
	vulnReq int
	batches int
	queries int
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

// BatchRequests counts POST /v1/querybatch requests and the queries in them.
func (o *OSV) BatchRequests() (requests, queries int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.batches, o.queries
}

func (o *OSV) querybatch(w http.ResponseWriter, r *http.Request) {
	o.batches++
	if o.down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		Queries []struct {
			Package struct {
				Name      string `json:"name"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Version string `json:"version"`
		} `json:"queries"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&in); err != nil || len(in.Queries) > 1000 {
		http.Error(w, `{"code":3,"message":"bad request"}`, http.StatusBadRequest)
		return
	}
	type vid struct {
		ID       string `json:"id"`
		Modified string `json:"modified"`
	}
	results := make([]map[string]any, len(in.Queries))
	for i, q := range in.Queries {
		o.queries++
		var ids []vid
		for _, rec := range o.records {
			var v advisory.Vuln
			if json.Unmarshal(rec.JSON, &v) != nil || q.Package.Ecosystem != "Go" || strings.HasPrefix(q.Version, "v") {
				continue // the real API takes versions without "v" for Go
			}
			named := false
			for _, a := range v.Affected {
				named = named || (a.Package.Ecosystem == "Go" && a.Package.Name == q.Package.Name)
			}
			if !named {
				continue
			}
			var hit bool
			if q.Package.Name == "stdlib" || q.Package.Name == "toolchain" {
				hit = v.AssessToolchain("v" + q.Version).Affected
			} else {
				hit = v.Assess(q.Package.Name, "v"+q.Version).Affected
			}
			if hit {
				ids = append(ids, vid{rec.ID, rec.Modified.Format(time.RFC3339)})
			}
		}
		res := map[string]any{}
		if len(ids) > 0 {
			res["vulns"] = ids
		}
		results[i] = res
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

func (o *OSV) serve(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/querybatch" && r.Method == http.MethodPost:
		o.querybatch(w, r)
		return
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
