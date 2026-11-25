package terraform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestVersions(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "" {
			t.Error("a credential was sent")
		}
		switch r.URL.Path {
		case "/v1/modules/acme/vpc/aws/versions":
			w.Write([]byte(`{"modules":[{"source":"acme/vpc/aws","versions":[{"version":"1.0.0"},{"version":"1.1.0-rc1"},{"version":"2.0.0"}]}]}`))
		case "/v1/providers/hashicorp/aws/versions":
			w.Write([]byte(`{"id":"hashicorp/aws","versions":[{"version":"4.0.0","protocols":["5.0"]},{"version":"5.1.0"}]}`))
		case "/v1/providers/acme/private/versions":
			http.Error(w, "no", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	ctx := context.Background()
	vs, err := c.Versions(ctx, "modules/acme/vpc/aws")
	if err != nil || !reflect.DeepEqual(vs, []string{"1.0.0", "1.1.0-rc1", "2.0.0"}) {
		t.Fatalf("module: %v %v", vs, err)
	}
	if got := Releases(vs); !reflect.DeepEqual(got, []string{"1.0.0", "2.0.0"}) {
		t.Errorf("releases %v", got)
	}
	if vs, err = c.Versions(ctx, "providers/hashicorp/aws"); err != nil || !reflect.DeepEqual(vs, []string{"4.0.0", "5.1.0"}) {
		t.Fatalf("provider: %v %v", vs, err)
	}
	if _, err = c.Versions(ctx, "providers/acme/private"); !errors.Is(err, ErrDenied) {
		t.Errorf("denied: %v", err)
	}
	if _, err = c.Versions(ctx, "providers/acme/none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
	for _, bad := range []string{"modules/a/b", "providers/a/b/c", "providers/../x", "modules/a/b/c/d", "modules/host.io/a/b", "x/a/b", "providers/a b/c", "providers//"} {
		if _, err := c.Versions(ctx, bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	for _, s := range []string{"git::https://x/y.git", "./local", "app.terraform.io/o/n/p", "a/b"} {
		if _, ok := ModulePackage(s); ok {
			t.Errorf("module source %q is not a plain registry address", s)
		}
	}
}
