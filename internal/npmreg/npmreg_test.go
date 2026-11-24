package npmreg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string, seen *http.Request) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, UserAgent: "test"}
}

func TestPackumentParsesAbbreviatedMetadata(t *testing.T) {
	var req http.Request
	c := serve(t, 200, `{"name":"@s/x","dist-tags":{"latest":"1.2.0","next":"2.0.0-rc.1"},
	  "versions":{"1.0.0":{"deprecated":"use 1.1"},"1.1.0":{"deprecated":""},"1.2.0":{},"2.0.0-rc.1":{}}}`, &req)
	pv, err := c.Packument(context.Background(), "@s/x")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.EscapedPath() != "/@s%2Fx" {
		t.Errorf("path %q", req.URL.EscapedPath())
	}
	if !strings.HasPrefix(req.Header.Get("Accept"), "application/vnd.npm.install-v1+json") || req.Header.Get("Authorization") != "" {
		t.Errorf("headers %v", req.Header)
	}
	if pv.Latest != "1.2.0" || len(pv.Versions) != 4 {
		t.Fatalf("%+v", pv)
	}
	dep := map[string]bool{}
	for _, v := range pv.Versions {
		dep[v.Version] = v.Deprecated
	}
	if !dep["1.0.0"] || dep["1.1.0"] || dep["1.2.0"] {
		t.Errorf("deprecated = %v", dep)
	}
}

func TestPackumentStatuses(t *testing.T) {
	for status, want := range map[int]error{404: ErrNotFound, 401: ErrDenied, 403: ErrDenied} {
		_, err := serve(t, status, "{}", nil).Packument(context.Background(), "left-pad")
		if !errors.Is(err, want) {
			t.Errorf("%d: %v", status, err)
		}
	}
	if _, err := serve(t, 500, "x", nil).Packument(context.Background(), "left-pad"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("500: %v", err)
	}
}

func TestPackumentRefusesBadNames(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	for _, n := range []string{"", "../x", "a b", "@s", "a/b", "x?y=1", "@s/x/y", ".hidden", strings.Repeat("a", 215)} {
		if _, err := c.Packument(context.Background(), n); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q accepted: %v", n, err)
		}
	}
}

func TestNewest(t *testing.T) {
	pv := Packument{Latest: "1.5.0", Versions: []Version{
		{Version: "1.2.3"}, {Version: "1.3.0"}, {Version: "1.4.0", Deprecated: true}, {Version: "1.5.0-beta.1"},
		{Version: "1.5.0"}, {Version: "2.0.0"}, {Version: "junk"},
	}}
	if got := pv.Newest("1.2.3"); got != "1.5.0" {
		t.Errorf("Newest = %q: stop at latest, skip prerelease", got)
	}
	if got := pv.Newest("1.5.0"); got != "" {
		t.Errorf("nothing above latest: %q", got)
	}
	pv.Versions[4].Deprecated = true // 1.5.0 deprecated: 1.3.0 is the newest usable
	if got := pv.Newest("1.2.3"); got != "1.3.0" {
		t.Errorf("deprecated skipped: %q", got)
	}
	nolatest := Packument{Versions: []Version{{Version: "1.0.0"}, {Version: "3.0.0"}}}
	if got := nolatest.Newest("1.0.0"); got != "3.0.0" {
		t.Errorf("no latest tag: %q", got)
	}
}

func TestPackumentReadsDistAndShape(t *testing.T) {
	c := serve(t, 200, `{"dist-tags":{"latest":"1.1.0"},"versions":{
	  "1.0.0":{"dist":{"tarball":"https://r/x/-/x-1.0.0.tgz","integrity":"sha512-AAA"},"dependencies":{"a":"^1"},"engines":{"node":">=4"}},
	  "1.1.0":{"dist":{"tarball":"https://r/x/-/x-1.1.0.tgz","integrity":"sha512-BBB"},"engines":{"node":">=4"},"dependencies":{"a":"^1"},"optionalDependencies":{}},
	  "1.2.0":{"dependencies":{"a":"^1","b":"^2"}}}}`, nil)
	pv, err := c.Packument(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := pv.Lookup("1.0.0")
	b, _ := pv.Lookup("1.1.0")
	n, _ := pv.Lookup("1.2.0")
	if a.Tarball != "https://r/x/-/x-1.0.0.tgz" || a.Integrity != "sha512-AAA" || b.Integrity != "sha512-BBB" || n.Integrity != "" {
		t.Errorf("dist: %+v %+v %+v", a, b, n)
	}
	if a.Shape != b.Shape {
		t.Errorf("key order and an empty optionalDependencies do not change the shape:\n%s\n%s", a.Shape, b.Shape)
	}
	if a.Shape == n.Shape {
		t.Error("a new dependency changes the shape")
	}
	if _, ok := pv.Lookup("9.9.9"); ok {
		t.Error("lookup of an unknown version")
	}
}
