package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Body copied from a real response of https://index.golang.org/index (2026-11-22).
const realLines = `{"Path":"github.com/adhithyan15/coding-adventures/code/packages/go/grammar-tools","Version":"v0.0.0-20261119235051-eae4af9b156a","Timestamp":"2026-11-20T00:00:04.041828Z"}
{"Path":"github.com/StacklokLabs/mkp","Version":"v0.5.1","Timestamp":"2026-11-20T00:00:09.315481Z"}
`

func TestGoIndexParsesSpecFormat(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Write([]byte(realLines))
	}))
	defer srv.Close()
	g := &GoIndex{BaseURL: srv.URL + "/", PageSize: 2}
	rels, more, err := g.Fetch(context.Background(), "2026-11-20T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "limit=2&since=2026-11-20T00%3A00%3A00Z" {
		t.Fatalf("query %q", gotQuery)
	}
	if len(rels) != 2 || !more {
		t.Fatalf("len=%d more=%v", len(rels), more)
	}
	if rels[1].Package != "github.com/StacklokLabs/mkp" || rels[1].Version != "v0.5.1" || rels[1].Datasource != "go" ||
		!rels[1].Time.Equal(time.Date(2026, 11, 20, 0, 0, 9, 315481000, time.UTC)) {
		t.Fatalf("%+v", rels[1])
	}
	g.PageSize = 5
	if _, more, _ := g.Fetch(context.Background(), ""); more {
		t.Fatal("short page must not report more")
	}
}

func TestGoIndexErrors(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status":  func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) },
		"garbage": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not json\n")) },
		"missing": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Timestamp":"2026-11-20T00:00:09Z"}` + "\n"))
		},
	} {
		srv := httptest.NewServer(h)
		_, _, err := (&GoIndex{BaseURL: srv.URL}).Fetch(context.Background(), "")
		srv.Close()
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, _, err := (&GoIndex{BaseURL: "http://127.0.0.1:1"}).Fetch(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "go index") {
		t.Fatalf("%v", err)
	}
}
