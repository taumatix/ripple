package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoProxyLookupLatest(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.EscapedPath()
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
