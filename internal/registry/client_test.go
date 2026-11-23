package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/testutil"
)

func clientFor(f *testutil.Registry) *Client {
	return &Client{HTTP: http.DefaultClient, BaseURL: func(string) string { return f.URL }, Insecure: true}
}

func TestTagsFollowsTheAnonymousTokenChallenge(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("library/nginx", "1.25.0")
	f.AddTag("library/nginx", "1.25.1")
	got, err := clientFor(f).Tags(context.Background(), "docker.io/library/nginx")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1.25.0", "1.25.1"}; !slices.Equal(got, want) {
		t.Errorf("tags %v want %v", got, want)
	}
	reqs := strings.Join(f.Requests(), ",")
	if !strings.Contains(reqs, "GET /token") {
		t.Errorf("no token requested: %s", reqs)
	}
}

func TestTagsPagesThroughTheLinkHeader(t *testing.T) {
	f := testutil.NewRegistry(t)
	var want []string
	for i := 0; i < 7; i++ {
		tag := fmt.Sprintf("1.%d.0", i)
		f.AddTag("org/img", tag)
		want = append(want, tag)
	}
	f.PageSize(3)
	got, err := clientFor(f).Tags(context.Background(), "ghcr.io/org/img")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("tags %v want %v", got, want)
	}
}

func TestDigestIsTheManifestDigest(t *testing.T) {
	f := testutil.NewRegistry(t)
	d := f.AddTag("org/img", "2.0.0")
	got, err := clientFor(f).Digest(context.Background(), "ghcr.io/org/img", "2.0.0")
	if err != nil || got != d {
		t.Fatalf("digest %q err %v want %q", got, err, d)
	}
}

func TestUnknownRepoAndDeniedTokenAreErrors(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("org/private", "1.0.0")
	f.DenyToken("org/private")
	c := clientFor(f)
	if _, err := c.Tags(context.Background(), "ghcr.io/org/nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown repo: %v", err)
	}
	if _, err := c.Tags(context.Background(), "ghcr.io/org/private"); !errors.Is(err, ErrDenied) {
		t.Errorf("private repo: %v", err)
	}
}

func TestNeverSendsCredentials(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("org/img", "1.0.0")
	var auth []string
	c := &Client{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			auth = append(auth, r.Header.Get("Authorization"))
		}
		return http.DefaultTransport.RoundTrip(r)
	})}, BaseURL: func(string) string { return f.URL }, Insecure: true}
	if _, err := c.Tags(context.Background(), "ghcr.io/org/img"); err != nil {
		t.Fatal(err)
	}
	if len(auth) != 1 || auth[0] != "" {
		t.Errorf("token request carried %q", auth)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDefaultClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("reached a loopback server") }))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://") // 127.0.0.1:port, written in a Dockerfile
	c := &Client{}
	if _, err := c.Tags(context.Background(), host+"/x/y"); err == nil {
		t.Fatal("a registry on a loopback address must be refused")
	}
}

func TestPlainHTTPIsRefusedUnlessInsecure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("plain http was used") }))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: func(string) string { return srv.URL }}
	if _, err := c.Tags(context.Background(), "ghcr.io/x/y"); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("got %v", err)
	}
}

func TestChallengeRealmOffTheNetworkRulesIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="ftp://example.com/token",service="s"`)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: func(string) string { return srv.URL }, Insecure: true}
	if _, err := c.Tags(context.Background(), "ghcr.io/x/y"); err == nil || !strings.Contains(err.Error(), "realm") {
		t.Errorf("got %v", err)
	}
}
