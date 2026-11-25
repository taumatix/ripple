package nuget

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestVersionsAndReleases(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/index.json":
			w.Write([]byte(`{"version":"3.0.0","resources":[{"@id":"` + srv.URL + `/search","@type":"SearchQueryService"},{"@id":"` + srv.URL + `/flat/","@type":"PackageBaseAddress/3.0.0"}]}`))
		case "/flat/newtonsoft.json/index.json":
			w.Write([]byte(`{"versions":["9.0.1","12.0.3","13.0.1","13.0.2-beta1","13.0.3.1","14.0.0-rc.1","13.0.1"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	vs, err := c.Versions(context.Background(), "Newtonsoft.Json") // looked up lowercased
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Releases(vs), []string{"9.0.1", "12.0.3", "13.0.1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("releases %v, want %v", got, want)
	}
	if _, err := c.Versions(context.Background(), "Nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing package: %v", err)
	}
	for _, bad := range []string{"", "../x", "a/b", "-x", "a b"} {
		if _, err := c.Versions(context.Background(), bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q must be refused before any request, got %v", bad, err)
		}
	}
}

func TestDenied(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/index.json" {
			w.Write([]byte(`{"resources":[{"@id":"` + srv.URL + `/flat/","@type":"PackageBaseAddress/3.0.0"}]}`))
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()
	if _, err := (&Client{BaseURL: srv.URL}).Versions(context.Background(), "x"); !errors.Is(err, ErrDenied) {
		t.Errorf("got %v", err)
	}
}

func TestNoPackageBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"resources":[]}`)) }))
	defer srv.Close()
	if _, err := (&Client{BaseURL: srv.URL}).Versions(context.Background(), "x"); err == nil {
		t.Error("an index without PackageBaseAddress must fail")
	}
}
