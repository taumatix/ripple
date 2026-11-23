package advisory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func recorded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "vendor", "osv", "samples", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeOSV serves the two documented surfaces: the bucket's Go/modified_id.csv and GET /v1/vulns/{id}.
func fakeOSV(t *testing.T, csv []byte, vulns map[string][]byte) (*Client, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/osv-vulnerabilities/Go/modified_id.csv", func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(csv)
	})
	mux.HandleFunc("/v1/vulns/", func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		b, ok := vulns[strings.TrimPrefix(r.URL.Path, "/v1/vulns/")]
		if !ok {
			http.Error(w, `{"code":5,"message":"Bug not found."}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{DumpURL: srv.URL + "/osv-vulnerabilities", APIURL: srv.URL}, &n
}

func TestModifiedReturnsNewerEntriesOldestFirst(t *testing.T) {
	c, _ := fakeOSV(t, recorded(t, "Go-modified_id.head.csv"), nil)
	after := Position{Time: time.Date(2026, 11, 21, 18, 10, 52, 122974014, time.UTC), ID: "GO-2026-5813"}
	got, truncated, err := c.Modified(context.Background(), after)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	// Same-timestamp entries are ordered by id; entries at or before the position are not returned.
	want := "GHSA-vgrc-hq28-p3xp GO-2026-5809 GHSA-2wrx-84qj-4pcg GO-2026-6894"
	if strings.Join(ids, " ") != want {
		t.Fatalf("got %v", ids)
	}
}

func TestModifiedStopsReadingAtTheCursor(t *testing.T) {
	var big strings.Builder
	big.WriteString("2026-11-22T16:10:57.01683783Z,GO-2026-0002\n2026-11-22T16:10:56Z,GO-2026-0001\n")
	for i := 0; i < 100000; i++ {
		big.WriteString("2020-01-01T00:00:00Z,GO-2020-0001\n")
	}
	c, _ := fakeOSV(t, []byte(big.String()), nil)
	c.MaxLines = 10
	got, truncated, err := c.Modified(context.Background(), Position{Time: time.Date(2026, 11, 22, 16, 10, 56, 0, time.UTC), ID: "GO-2026-0001"})
	if err != nil || truncated || len(got) != 1 || got[0].ID != "GO-2026-0002" {
		t.Fatalf("got %v truncated=%v err=%v", got, truncated, err)
	}
}

func TestModifiedLineCapReportsTruncation(t *testing.T) {
	c, _ := fakeOSV(t, recorded(t, "Go-modified_id.head.csv"), nil)
	c.MaxLines = 3
	got, truncated, err := c.Modified(context.Background(), Position{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil || !truncated || len(got) != 3 {
		t.Fatalf("got %d truncated=%v err=%v", len(got), truncated, err)
	}
}

func TestModifiedRejectsGarbage(t *testing.T) {
	c, _ := fakeOSV(t, []byte("not a csv line\n"), nil)
	if _, _, err := c.Modified(context.Background(), Position{}); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestModifiedServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 503) }))
	defer srv.Close()
	c := &Client{DumpURL: srv.URL, APIURL: srv.URL}
	if _, _, err := c.Modified(context.Background(), Position{}); err == nil {
		t.Fatal("503 accepted")
	}
	if _, err := c.Vuln(context.Background(), "GO-1"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("503 must be a plain error, got %v", err)
	}
}

func TestVulnFetchAndNotFound(t *testing.T) {
	c, _ := fakeOSV(t, nil, map[string][]byte{"GO-2026-6894": recorded(t, "GO-2026-6894.json")})
	v, err := c.Vuln(context.Background(), "GO-2026-6894")
	if err != nil || v.ID != "GO-2026-6894" || len(v.Modules()) != 1 {
		t.Fatalf("got %+v %v", v, err)
	}
	if _, err := c.Vuln(context.Background(), "GO-0000-0000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// An id from a CSV is data: it must not be able to alter the request path.
	if _, err := c.Vuln(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestPositionRoundTrip(t *testing.T) {
	p := Position{Time: time.Date(2026, 11, 22, 16, 10, 57, 16837830, time.UTC), ID: "GO-2026-6894"}
	got, err := ParsePosition(p.String())
	if err != nil || !got.Time.Equal(p.Time) || got.ID != p.ID {
		t.Fatalf("got %+v %v", got, err)
	}
	if z, err := ParsePosition(""); err != nil || !z.Time.IsZero() {
		t.Fatalf("empty: %+v %v", z, err)
	}
	if _, err := ParsePosition("junk"); err == nil {
		t.Fatal("junk accepted")
	}
}

// batchServer answers POST /v1/querybatch like the documented API: results in query order,
// ids only, and a next_page_token when a query has more than pageSize ids.
func batchServer(t *testing.T, answers map[string][]string, pageSize int) (*Client, *[]map[string]any) {
	t.Helper()
	var reqs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/querybatch" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Queries []struct {
				Package   struct{ Name, Ecosystem string }
				Version   string
				PageToken string `json:"page_token"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if len(in.Queries) > 1000 {
			http.Error(w, "too many", 400)
			return
		}
		type res struct {
			Vulns []map[string]string `json:"vulns,omitempty"`
			Next  string              `json:"next_page_token,omitempty"`
		}
		var results []res
		for _, q := range in.Queries {
			reqs = append(reqs, map[string]any{"name": q.Package.Name, "eco": q.Package.Ecosystem, "version": q.Version, "token": q.PageToken})
			ids := answers[q.Package.Name+"@"+q.Version]
			start := 0
			if q.PageToken != "" {
				fmt.Sscanf(q.PageToken, "%d", &start)
			}
			end := min(start+pageSize, len(ids))
			var r res
			for _, id := range ids[start:end] {
				r.Vulns = append(r.Vulns, map[string]string{"id": id, "modified": "2026-11-01T00:00:00Z"})
			}
			if end < len(ids) {
				r.Next = fmt.Sprint(end)
			}
			results = append(results, r)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	t.Cleanup(srv.Close)
	return &Client{APIURL: srv.URL}, &reqs
}

func TestQueryBatchKeepsOrderStripsVAndFollowsPages(t *testing.T) {
	c, reqs := batchServer(t, map[string][]string{
		"example.com/a@1.0.0": {"GO-1", "GO-2", "GO-3"},
		"stdlib@1.21.0":       {"GO-9"},
	}, 2)
	got, err := c.QueryBatch(context.Background(), []Query{
		{"example.com/a", "v1.0.0"}, {"example.com/clean", "v2.0.0"}, {"stdlib", "v1.21.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"GO-1", "GO-2", "GO-3"}, nil, {"GO-9"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, r := range *reqs {
		if r["eco"] != "Go" || strings.HasPrefix(r["version"].(string), "v") {
			t.Fatalf("bad request %v", r)
		}
	}
}

func TestQueryBatchChunksAtOneThousand(t *testing.T) {
	c, reqs := batchServer(t, nil, 10)
	qs := make([]Query, 2500)
	for i := range qs {
		qs[i] = Query{fmt.Sprintf("example.com/m%d", i), "v1.0.0"}
	}
	got, err := c.QueryBatch(context.Background(), qs)
	if err != nil || len(got) != 2500 || len(*reqs) != 2500 {
		t.Fatalf("err=%v got=%d reqs=%d", err, len(got), len(*reqs))
	}
}

func TestQueryBatchErrors(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status":  func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 503) },
		"short":   func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"results":[]}`)) },
		"garbage": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>`)) },
		"bad id": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"../x"}]}]}`))
		},
		"too big": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, maxBatchBytes+10)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			c := &Client{APIURL: srv.URL}
			if _, err := c.QueryBatch(context.Background(), []Query{{"example.com/a", "v1.0.0"}}); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// The default line cap covers the whole Go list (about 10k lines on 2026-11-23), so a
// poll after a long outage is not truncated.
func TestModifiedDefaultCapCoversTheWholeGoList(t *testing.T) {
	var b strings.Builder
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 12000; i > 0; i-- {
		fmt.Fprintf(&b, "%s,GO-2026-%05d\n", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i)
	}
	c, _ := fakeOSV(t, []byte(b.String()), nil)
	got, truncated, err := c.Modified(context.Background(), Position{Time: base})
	if err != nil || truncated || len(got) != 12000 {
		t.Fatalf("err=%v truncated=%v entries=%d", err, truncated, len(got))
	}
}
