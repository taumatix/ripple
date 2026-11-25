package helmrepo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const index = `apiVersion: v1
entries:
  a:
    - version: 1.2.0
      urls: [https://x/a-1.2.0.tgz]
    - version: 1.3.0
      deprecated: true
    - version: 2.0.0-rc.1
    - version: v1.1.0
    - version: "1.1"
    - version: 1.0.0+build.5
    - name: broken
  b: []
generated: 2016-10-06T16:23:20.499029981-06:00
`

func TestIndexReadsAndFilters(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub/index.yaml" || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "ripple-test" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		w.Write([]byte(index))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), UserAgent: "ripple-test"}
	ix, err := c.Index(context.Background(), srv.URL+"/sub")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ix.Releases("a"), " "); got != "1.2.0 v1.1.0" {
		t.Errorf("releases: not deprecated, not a prerelease, build or short version, 'v' kept: %q", got)
	}
	if _, err := c.Index(context.Background(), srv.URL+"/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestIndexRefusesWhatItWillNotRead(t *testing.T) {
	c := &Client{}
	for _, u := range []string{"http://charts.example.com", "https://u:p@charts.example.com", "https://charts.example.com?x=1", "https://charts.example.com/", "file://x", ""} {
		if _, err := c.Index(context.Background(), u); err == nil {
			t.Errorf("%q was read", u)
		}
	}
}

func TestDefaultClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(index)) }))
	defer srv.Close()
	if _, err := (&Client{}).Index(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Errorf("a repository that resolves to loopback must be refused before connecting: %v", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	if NormalizeURL("https://h.example.com/a/") != "https://h.example.com/a" || NormalizeURL("oci://r/x") != "" || NormalizeURL("@stable") != "" {
		t.Error("normalize")
	}
}
