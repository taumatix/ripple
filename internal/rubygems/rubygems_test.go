package rubygems

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
		if r.URL.Path != "/api/v1/versions/rack.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[{"number":"3.0.0.rc1","platform":"ruby","prerelease":true},
		{"number":"2.2.8","platform":"ruby","prerelease":false},
		{"number":"2.2.8","platform":"java","prerelease":false},
		{"number":"2.2.7.1","platform":"ruby","prerelease":false},
		{"number":"2.2.9","platform":"x86_64-linux","prerelease":false},
		{"number":"2.1.0","prerelease":false}]`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	vs, err := c.Versions(context.Background(), "rack")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Releases(vs), []string{"2.2.8", "2.1.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("releases %v, want %v", got, want)
	}
	if _, err := c.Versions(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing gem: %v", err)
	}
	for _, bad := range []string{"", "../x", "a/b", "-x", "a b"} {
		if _, err := c.Versions(context.Background(), bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q must be refused before any request, got %v", bad, err)
		}
	}
}

func TestDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	if _, err := (&Client{BaseURL: srv.URL}).Versions(context.Background(), "x"); !errors.Is(err, ErrDenied) {
		t.Errorf("got %v", err)
	}
}
