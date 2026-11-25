package packagist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestVersionsAndReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p2/monolog/monolog.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"minified":"composer/2.0","packages":{"monolog/monolog":[
		{"name":"monolog/monolog","version":"3.0.0-RC1"},{"version":"v2.9.1"},{"version":"2.9.0"},
		{"version":"1.2.3.4"},{"version":"dev-main"},{"version":"2.9.1"},{"version":"2.8"}]}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	vs, err := c.Versions(context.Background(), "monolog/monolog")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Releases(vs), []string{"2.9.1", "2.9.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("releases %v, want %v", got, want)
	}
	if _, err := c.Versions(context.Background(), "no/pe"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing package: %v", err)
	}
	for _, bad := range []string{"", "php", "ext-json", "../x/y", "a/b/c", "Vendor/Pkg", "a/b c"} {
		if _, err := c.Versions(context.Background(), bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q must be refused before any request, got %v", bad, err)
		}
	}
}

func TestDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	if _, err := (&Client{BaseURL: srv.URL}).Versions(context.Background(), "a/b"); !errors.Is(err, ErrDenied) {
		t.Errorf("got %v", err)
	}
}
