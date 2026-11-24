package registry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/testutil"
)

func dockerConfig(entries ...string) []byte {
	return []byte(`{"auths":{` + strings.Join(entries, ",") + `}}`)
}

func authEntry(host, user, pass string) string {
	return fmt.Sprintf(`%q:{"auth":%q}`, host, base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
}

func TestParseDockerConfigNormalisesHostsAndReadsBothShapes(t *testing.T) {
	c, err := ParseDockerConfig(dockerConfig(
		authEntry("https://index.docker.io/v1/", "hubuser", "hub:pass"), // the password may hold a colon
		authEntry("GHCR.io", "ghuser", "ghpass"),
		`"registry.example.com:5000":{"username":"u","password":"p"}`,
		`"tok.example.com":{"identitytoken":"abc"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string][2]string{
		"docker.io":                 {"hubuser", "hub:pass"},
		"ghcr.io":                   {"ghuser", "ghpass"},
		"registry.example.com:5000": {"u", "p"},
	} {
		if u, p, ok := c.For(host); !ok || u != want[0] || p != want[1] {
			t.Errorf("%s: got %q %q %v", host, u, p, ok)
		}
	}
	if _, _, ok := c.For("tok.example.com"); ok {
		t.Error("an identity token is not a user and password")
	}
	if tok, ok := c.IdentityToken("tok.example.com"); !ok || tok != "abc" {
		t.Errorf("identity token %q %v", tok, ok)
	}
	if _, ok := c.IdentityToken("ghcr.io"); ok {
		t.Error("a user and password entry has no identity token")
	}
	if got := strings.Join(c.Hosts(), " "); !strings.Contains(got, "tok.example.com") {
		t.Errorf("hosts %q", got)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("warnings %v", c.Warnings)
	}
}

func TestParseDockerConfigWarnsAboutCredentialHelpers(t *testing.T) {
	c, err := ParseDockerConfig([]byte(`{"credsStore":"osxkeychain","auths":{"ghcr.io":{}}}`))
	if err != nil || len(c.Hosts()) != 0 || len(c.Warnings) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseDockerConfig([]byte(`not json`)); err == nil {
		t.Error("a file that is not JSON must be an error")
	}
	c, _ = ParseDockerConfig(dockerConfig(`"ghcr.io":{"auth":"!!!"}`))
	if len(c.Hosts()) != 0 || len(c.Warnings) != 1 {
		t.Errorf("a bad auth value is ignored with a warning: %v", c.Warnings)
	}
}

func TestCredentialsNeverPrint(t *testing.T) {
	c, _ := ParseDockerConfig(dockerConfig(authEntry("ghcr.io", "user", "s3cret-pass")))
	var b strings.Builder
	l := slog.New(slog.NewTextHandler(&b, nil))
	l.Info("x", "auth", c)
	out := fmt.Sprintf("%v %+v %#v %s", c, c, c, b.String())
	if strings.Contains(out, "s3cret-pass") || strings.Contains(out, base64.StdEncoding.EncodeToString([]byte("user:s3cret-pass"))) {
		t.Errorf("a credential leaked: %s", out)
	}
}

func authedClient(f *testutil.Registry, c *Credentials) *Client {
	cl := clientFor(f)
	cl.Auth = c
	return cl
}

func TestPrivateImageOnATokenRegistryNeedsTheLogin(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("corp/secret", "1.0.0")
	f.RequireLogin("corp/secret", "user", "pw")
	if _, err := clientFor(f).Tags(context.Background(), "ghcr.io/corp/secret"); !errors.Is(err, ErrDenied) {
		t.Fatalf("anonymous must be denied, got %v", err)
	}
	wrong, _ := ParseDockerConfig(dockerConfig(authEntry("ghcr.io", "user", "nope")))
	_, err := authedClient(f, wrong).Tags(context.Background(), "ghcr.io/corp/secret")
	if !errors.Is(err, ErrDenied) || strings.Contains(err.Error(), "nope") {
		t.Fatalf("a refused login is ErrDenied without echoing it: %v", err)
	}
	good, _ := ParseDockerConfig(dockerConfig(authEntry("ghcr.io", "user", "pw")))
	got, err := authedClient(f, good).Tags(context.Background(), "ghcr.io/corp/secret")
	if err != nil || len(got) != 1 || got[0] != "1.0.0" {
		t.Fatalf("tags %v err %v", got, err)
	}
	if d, err := authedClient(f, good).Digest(context.Background(), "ghcr.io/corp/secret", "1.0.0"); err != nil || d == "" {
		t.Errorf("digest %q %v", d, err)
	}
}

func TestLoginIsSentOnlyToTheRegistryItWasSavedFor(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("library/nginx", "1.25")
	good, _ := ParseDockerConfig(dockerConfig(authEntry("ghcr.io", "user", "pw")))
	if _, err := authedClient(f, good).Tags(context.Background(), "docker.io/library/nginx"); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.TokenRequests() {
		if strings.HasSuffix(r, "basic=true") {
			t.Errorf("a login for ghcr.io went to another registry's token request: %s", r)
		}
	}
}

func TestBasicChallengeIsAnsweredWithTheLogin(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("team/app", "3.1.0")
	f.RequireBasic("user", "pw")
	if _, err := clientFor(f).Tags(context.Background(), "reg.example.com/team/app"); !errors.Is(err, ErrDenied) {
		t.Fatalf("no login configured: %v", err)
	}
	good, _ := ParseDockerConfig(dockerConfig(authEntry("reg.example.com", "user", "pw")))
	got, err := authedClient(f, good).Tags(context.Background(), "reg.example.com/team/app")
	if err != nil || len(got) != 1 {
		t.Fatalf("tags %v err %v", got, err)
	}
}

func identityConfig(host, token string) []byte {
	return dockerConfig(fmt.Sprintf(`%q:{"identitytoken":%q}`, host, token))
}

func TestIdentityTokenIsGrantedAtTheTokenEndpoint(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("corp/secret", "1.0.0")
	f.RequireIdentityToken("corp/secret", "refresh-abc")
	if _, err := clientFor(f).Tags(context.Background(), "ghcr.io/corp/secret"); !errors.Is(err, ErrDenied) {
		t.Fatalf("anonymous must be denied, got %v", err)
	}
	wrong, _ := ParseDockerConfig(identityConfig("ghcr.io", "refresh-nope"))
	_, err := authedClient(f, wrong).Tags(context.Background(), "ghcr.io/corp/secret")
	if !errors.Is(err, ErrDenied) || strings.Contains(err.Error(), "refresh-nope") {
		t.Fatalf("a refused token is ErrDenied without echoing it: %v", err)
	}
	good, _ := ParseDockerConfig(identityConfig("ghcr.io", "refresh-abc"))
	cl := authedClient(f, good)
	got, err := cl.Tags(context.Background(), "ghcr.io/corp/secret")
	if err != nil || len(got) != 1 || got[0] != "1.0.0" {
		t.Fatalf("tags %v err %v", got, err)
	}
	if d, err := cl.Digest(context.Background(), "ghcr.io/corp/secret", "1.0.0"); err != nil || d == "" {
		t.Errorf("digest %q %v", d, err)
	}
	last := f.TokenRequests()[len(f.TokenRequests())-1]
	if !strings.Contains(last, "grant=true") || strings.Contains(last, "basic=true") {
		t.Errorf("the grant is a POST of the refresh token, never Basic: %s", last)
	}
}

func TestIdentityTokenGoesOnlyToItsOwnRegistryAndNeverToABasicChallenge(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("library/nginx", "1.25")
	good, _ := ParseDockerConfig(identityConfig("ghcr.io", "refresh-abc"))
	if _, err := authedClient(f, good).Tags(context.Background(), "docker.io/library/nginx"); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.TokenRequests() {
		if strings.Contains(r, "grant=") {
			t.Errorf("a token for ghcr.io was granted to another registry: %s", r)
		}
	}
	f2 := testutil.NewRegistry(t)
	f2.AddTag("team/app", "3.1.0")
	f2.RequireBasic("user", "pw")
	cfg, _ := ParseDockerConfig(identityConfig("reg.example.com", "refresh-abc"))
	if _, err := authedClient(f2, cfg).Tags(context.Background(), "reg.example.com/team/app"); !errors.Is(err, ErrDenied) {
		t.Errorf("an identity token cannot answer a Basic challenge: %v", err)
	}
}

func TestIdentityTokenNeverPrintsAndRedirectsAreNotFollowed(t *testing.T) {
	c, _ := ParseDockerConfig(identityConfig("ghcr.io", "refresh-secret"))
	out := fmt.Sprintf("%v %+v %#v", c, c, c)
	if strings.Contains(out, "refresh-secret") {
		t.Errorf("a token leaked: %s", out)
	}
	hits := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer elsewhere.Close()
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer realm.Close()
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm.URL+`/token",service="s",scope="repository:a/b:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer reg.Close()
	cl := &Client{Insecure: true, BaseURL: func(string) string { return reg.URL }, Auth: c}
	if _, err := cl.Tags(context.Background(), "ghcr.io/a/b"); err == nil || strings.Contains(err.Error(), "refresh-secret") {
		t.Errorf("a redirecting token endpoint is an error that does not echo the token: %v", err)
	}
	if hits != 0 {
		t.Errorf("the refresh token was re-sent to a redirect target %d times", hits)
	}
}
