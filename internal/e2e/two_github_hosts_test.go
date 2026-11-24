package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const secondSecret = "the second GitHub's own secret"

// Two GitHubs on one server (roadmap entry 63): github.com's host is the default and
// its repos keep their plain names; a second host ("ghes") has its own fake GitHub,
// App, webhook secret and path, and its repos are stored as "ghes:owner/repo". Each
// repo's update opens its PR on its own GitHub and on no other.
func TestTwoGitHubHostsEachServeTheirOwnRepos(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		second := testutil.NewGitHub(t)
		var hosts []*platform.Host
		e := newEnvOpts(t, st, defaultLimits(), opts{
			engine: func(eng *engine.Engine) {
				first := eng.Platform
				c1 := eng.Platform.(*testutil.CountingPlatform).Inner.(*github.Client)
				c2 := &github.Client{APIBase: second.APIURL, GitBase: second.GitBase, Tokens: second.Auth, WorkDir: t.TempDir(),
					GoProxy: c1.GoProxy, GoSumDB: "off", GoCacheDir: c1.GoCacheDir, Now: c1.Now, Log: slog.New(slog.DiscardHandler)}
				h1 := github.NewHost(c1, []byte(secret), slog.New(slog.DiscardHandler))
				h1.Platform = first
				h2 := github.NewNamedHost("ghes", c2, []byte(secondSecret), slog.New(slog.DiscardHandler))
				mux, err := platform.NewMux(github.HostName, h1, h2)
				if err != nil {
					t.Fatal(err)
				}
				eng.Platform = mux
				hosts = []*platform.Host{h1, h2}
			},
			tweak: func(a *app.App) { a.Hosts, a.DefaultHost = hosts, github.HostName },
		})
		stubModule(e, "example.com/lib", "v1.0.0", "v1.0.1")
		files := func(mod string) map[string]string {
			return map[string]string{"go.mod": gomod(mod, "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")}
		}
		e.gh.AddRepo("acme/app", files("example.com/app"))
		second.AddRepo("corp/svc", files("example.com/svc"))

		e.install("acme/app")
		body, _ := json.Marshal(map[string]any{"action": "created", "installation": map[string]any{"id": 7}, "repositories": repoList("corp/svc")})
		m := hmac.New(sha256.New, []byte(secondSecret))
		m.Write(body)
		post := func(path, sig string) int {
			req, _ := http.NewRequest(http.MethodPost, e.hook+path, bytes.NewReader(body))
			req.Header.Set("X-GitHub-Event", "installation")
			req.Header.Set("X-Hub-Signature-256", sig)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			return resp.StatusCode
		}
		good := "sha256=" + hex.EncodeToString(m.Sum(nil))
		if code := post("/ghes", sign(body)); code == http.StatusAccepted {
			t.Fatalf("the second host accepted a delivery signed with the first host's secret")
		}
		if code := post("/ghes", good); code != http.StatusAccepted {
			t.Fatalf("second host's webhook: %d", code)
		}
		e.waitRepo("ghes:corp/svc")
		e.tick()
		e.publish("example.com/lib", "v1.0.1")
		e.tick()

		if prs := e.gh.PRs("acme/app"); len(prs) != 1 {
			t.Errorf("first GitHub: want one PR for acme/app, got %+v", prs)
		}
		if prs := second.PRs("corp/svc"); len(prs) != 1 {
			t.Errorf("second GitHub: want one PR for corp/svc, got %+v", prs)
		}
		if prs := e.gh.PRs("corp/svc"); len(prs) != 0 {
			t.Errorf("the second host's repo got a PR on the first GitHub: %+v", prs)
		}
		if _, ok, _ := e.st.GetRepo(t.Context(), "corp/svc"); ok {
			t.Errorf("the second host's repo is stored without its host prefix")
		}
	})
}
