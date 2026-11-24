package gotool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolvePicksTheNewestPatchOfTheMinorOnThisPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Join([]string{
			"v0.0.1-go1.25.1.linux-amd64", "v0.0.1-go1.25.7.linux-amd64", "v0.0.1-go1.25.9.darwin-arm64",
			"v0.0.1-go1.26.2.linux-amd64", "v0.0.1-go1.25rc1.linux-amd64",
		}, "\n")))
	}))
	defer srv.Close()
	i := &Installer{Proxy: srv.URL, OS: "linux", Arch: "amd64"}
	got, err := i.resolve(context.Background(), "1.25", "")
	if err != nil || got != "1.25.7" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err = i.resolve(context.Background(), "1.25.8", ""); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("a patch above the newest must be ErrNoRelease, got %q, %v", got, err)
	}
	if got, err = i.resolve(context.Background(), "1.25", "go1.26.2"); err != nil || got != "1.26.2" {
		t.Fatalf("the toolchain line is used as named: %q, %v", got, err)
	}
}
