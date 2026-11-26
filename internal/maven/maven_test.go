package maven

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const metadata = `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>org.example</groupId>
  <artifactId>lib</artifactId>
  <versioning>
    <latest>2.0.0</latest>
    <release>2.0.0</release>
    <versions>
      <version>1.0.0</version>
      <version>1.2</version>
      <version>1.2.3</version>
      <version>1.2.3-SNAPSHOT</version>
      <version>2.0.0</version>
    </versions>
    <lastUpdated>20240101000000</lastUpdated>
  </versioning>
</metadata>`

func TestVersions(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/org/example/lib/maven-metadata.xml":
			w.Write([]byte(metadata))
		case "/org/example/private/maven-metadata.xml":
			http.Error(w, "no", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	vs, err := c.Versions(context.Background(), "org.example:lib")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/org/example/lib/maven-metadata.xml" || auth != "" {
		t.Errorf("path %q auth %q", path, auth)
	}
	if want := []string{"1.0.0", "1.2", "1.2.3", "1.2.3-SNAPSHOT", "2.0.0"}; !reflect.DeepEqual(vs, want) {
		t.Errorf("got %v", vs)
	}
	if got, want := Releases(vs), []string{"1.0.0", "1.2", "1.2.3", "2.0.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("releases %v", got)
	}
	if _, err := c.Versions(context.Background(), "org.example:private"); !errors.Is(err, ErrDenied) {
		t.Errorf("denied: %v", err)
	}
	if _, err := c.Versions(context.Background(), "org.example:none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
	for _, bad := range []string{"nocolon", "a/b:c", "a:../c", "a..b:c", ":x", "a:"} {
		if _, err := c.Versions(context.Background(), bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
