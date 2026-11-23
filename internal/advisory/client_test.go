package advisory

import (
	"context"
	"errors"
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
