package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoProxyLookupLatest(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/@latest") {
			gotPath = r.URL.EscapedPath()
		}
		switch r.URL.Path {
		case "/example.com/m/@latest":
			_, _ = w.Write([]byte(`{"Version":"v1.4.0","Time":"2026-01-02T03:04:05Z"}`))
		case "/example.com/gone/@latest":
			http.Error(w, "gone", http.StatusGone)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	l := &GoProxyLookup{BaseURL: srv.URL, User: "u", Token: "t"}
	r, ok, err := l.Latest(context.Background(), "example.com/m")
	if err != nil || !ok || r.Version != "v1.4.0" || r.Package != "example.com/m" || r.Time.IsZero() {
		t.Fatalf("got %+v ok=%v err=%v", r, ok, err)
	}
	if gotAuth == "" {
		t.Error("credential not sent")
	}
	for _, m := range []string{"example.com/gone", "example.com/none"} {
		if _, ok, err := l.Latest(context.Background(), m); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, want not found without error", m, ok, err)
		}
	}
	// Capital letters are escaped as !lower.
	_, _, _ = l.Latest(context.Background(), "example.com/Mod")
	if gotPath != "/example.com/%21mod/@latest" && gotPath != "/example.com/!mod/@latest" {
		t.Errorf("path %q", gotPath)
	}
}

// fakeProxy serves the GOPROXY paths from a map and counts requests.
func fakeProxy(t *testing.T, files map[string]string) (*httptest.Server, *int) {
	t.Helper()
	n := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, n
}

func TestGoProxyLookupPrereleaseOnlyModule(t *testing.T) {
	srv, _ := fakeProxy(t, map[string]string{
		"/example.com/pre/@v/list":               "v0.1.0-alpha.1\nv0.1.0-beta.2\n",
		"/example.com/pre/@v/v0.1.0-beta.2.info": `{"Version":"v0.1.0-beta.2","Time":"2026-02-03T04:05:06Z"}`,
	})
	r, ok, err := (&GoProxyLookup{BaseURL: srv.URL}).Latest(context.Background(), "example.com/pre")
	if err != nil || !ok || r.Version != "v0.1.0-beta.2" || r.Time.IsZero() {
		t.Fatalf("got %+v ok=%v err=%v", r, ok, err)
	}
}

func TestGoProxyLookupSkipsARetractedLatest(t *testing.T) {
	srv, _ := fakeProxy(t, map[string]string{
		"/example.com/ret/@latest":        `{"Version":"v1.3.0","Time":"2026-01-03T00:00:00Z"}`,
		"/example.com/ret/@v/list":        "v1.1.0\nv1.2.0\nv1.3.0\nv1.4.0-rc.1\n",
		"/example.com/ret/@v/v1.3.0.mod":  "module example.com/ret\n\nretract [v1.2.0, v1.3.0]\n",
		"/example.com/ret/@v/v1.1.0.info": `{"Version":"v1.1.0","Time":"2026-01-01T00:00:00Z"}`,
	})
	r, ok, err := (&GoProxyLookup{BaseURL: srv.URL}).Latest(context.Background(), "example.com/ret")
	if err != nil || !ok || r.Version != "v1.1.0" {
		t.Fatalf("want v1.1.0 (stable, not retracted), got %+v ok=%v err=%v", r, ok, err)
	}
}

func TestGoProxyLookupCachesAcrossRepos(t *testing.T) {
	srv, n := fakeProxy(t, map[string]string{
		"/example.com/m/@latest": `{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}`,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := &GoProxyLookup{BaseURL: srv.URL, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		for _, pkg := range []string{"example.com/m", "example.com/unknown"} {
			if _, _, err := l.Latest(context.Background(), pkg); err != nil {
				t.Fatal(err)
			}
		}
	}
	asked := *n
	now = now.Add(lookupTTL + time.Second)
	_, _, _ = l.Latest(context.Background(), "example.com/m")
	if *n <= asked {
		t.Error("an expired entry was not asked again")
	}
	if asked > 6 {
		t.Errorf("six lookups of two modules cost %d requests; the cache did not hold", asked)
	}
}
