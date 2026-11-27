//go:build scmreal

package scmmanager_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

const mtRepo = "application/vnd.scmm-repository+json;v=2"

// gitReal runs git against the instance with the API key as the basic-auth password.
func (s *scm) gitReal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("ripple:"+s.apiKey(t)))
	full := append([]string{"-c", "http.extraHeader=" + auth, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.ReplaceAll(string(out), s.apiKey(t), "<token>"))
	}
	return string(out)
}

// TestRealDiscovery records what a bootstrapped instance answers about repositories, webhooks and
// pull requests, driving the client under test where it exists.
func TestRealDiscovery(t *testing.T) {
	s := realSCM(t)
	key := s.apiKey(t)

	code, _, body := s.call(t, http.MethodGet, s.base+"/api/v2/", mtIndex, "", nil)
	var idx struct {
		Links links `json:"_links"`
	}
	_ = json.Unmarshal(body, &idx)
	webhookURL := idx.Links.href("webHookConfig")
	if webhookURL == "" {
		t.Fatalf("no webHookConfig link: %v", idx.Links.names())
	}

	// A repository with its first commit, created the way the UI does. The namespace asked for is
	// not the namespace given: the default strategy puts it under the creating user.
	code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/repositories?initialize=true", "", mtRepo,
		map[string]any{"namespace": "acme", "name": "probe", "type": "git", "contextEntries": map[string]any{}})
	t.Logf("FACT create repository (namespace acme asked for) with initialize=true: %d location=%q %s", code, h.Get("Location"), clip(body))
	if code != http.StatusCreated {
		t.Fatalf("creating the repository: %d", code)
	}
	full := strings.TrimPrefix(h.Get("Location"), s.base+"/api/v2/repositories/")
	ns, name, _ := strings.Cut(full, "/")
	repo := model.Repo{FullName: full}

	c := &scmmanager.Client{BaseURL: s.base, Credentials: platform.StaticCredential{Secret: key}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repos, err := c.ListRepos(ctx)
	t.Logf("FACT ListRepos: %v err=%v", repos, err)
	heads, err := c.ListRepoHeads(ctx)
	t.Logf("FACT ListRepoHeads: %v err=%v", heads, err)
	def, err := c.DefaultBranch(ctx, full)
	t.Logf("FACT DefaultBranch: %q err=%v", def, err)
	repo.DefaultBranch = def

	// A webhook, through the plugin's REST resource, and a receiver for it.
	var mu sync.Mutex
	var got []string
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		h := r.Header.Clone()
		for k := range h {
			if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, scmmanager.SecretHeader) {
				h[k] = []string{"<redacted>"}
			}
		}
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.String()+" headers="+toJSON(h)+" body="+clip(b))
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	pattern := "http://host.docker.internal:" + port + "/hook?namespace=${repository.namespace}&name=${repository.name}"
	// The first attempt put the fields at the top of the entry and came back as
	// {"name":null,"unknown":true}: an entry is {name, configuration}, name being the Java class name
	// of the kind (WebHookDto and WebHook.java in scm-webhook-plugin).
	cfg := map[string]any{"webhooks": []map[string]any{{"name": "SimpleWebHook", "configuration": map[string]any{
		"urlPattern": pattern, "method": "AUTO", "executeOnEveryCommit": false, "sendCommitData": true,
		"headers": []map[string]any{{"key": scmmanager.SecretHeader, "value": "probe-secret", "concealed": true}}}}}}
	code, _, body = s.call(t, http.MethodPut, webhookURL, "", "application/json", cfg)
	t.Logf("FACT PUT global webhook config: %d %s", code, clip(body))
	code, _, body = s.call(t, http.MethodGet, webhookURL, "application/json", "", nil)
	t.Logf("FACT GET global webhook config after the PUT: %d %s", code, clip(body))

	// Push to main with real git.
	dir := t.TempDir()
	s.gitReal(t, dir, "clone", "-q", s.base+"/repo/"+ns+"/"+name, ".")
	t.Logf("FACT clone with the API key as basic password: ok; branches: %s", strings.TrimSpace(s.gitReal(t, dir, "branch", "-a")))
	if err := os.WriteFile(filepath.Join(dir, "m.txt"), []byte("m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.gitReal(t, dir, "add", "-A")
	s.gitReal(t, dir, "commit", "-q", "-m", "to main")
	s.gitReal(t, dir, "push", "-q", "origin", "HEAD:"+def)
	for i := 0; i < 150; i++ {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(3 * time.Second) // only to count a duplicate, which is the question
	mu.Lock()
	t.Logf("FACT webhook deliveries for one push to %s: %d", def, len(got))
	for _, g := range got {
		t.Logf("FACT webhook delivery: %s", g)
	}
	mu.Unlock()

	// A branch and a pull request through the client.
	s.gitReal(t, dir, "checkout", "-q", "-b", "ripple/probe")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.gitReal(t, dir, "add", "-A")
	s.gitReal(t, dir, "commit", "-q", "-m", "probe")
	s.gitReal(t, dir, "push", "-q", "origin", "ripple/probe")
	mu.Lock()
	t.Logf("FACT webhook deliveries after also pushing a branch: %d", len(got))
	mu.Unlock()
	pr, err := c.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/probe", Base: def, Title: "probe", Body: "probe"})
	t.Logf("FACT CreatePR: %+v err=%v", pr, err)
	prs, err := c.ListOpenPRs(ctx, repo, "ripple/")
	t.Logf("FACT ListOpenPRs: %+v err=%v", prs, err)
	_, err = c.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/probe", Base: def, Title: "again", Body: "x"})
	t.Logf("FACT CreatePR for the same branches again: err=%v", err)
	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/pull-requests/"+ns+"/"+name, "application/vnd.scmm-pullRequestCollection+json;v=2", "", nil)
	t.Logf("FACT pull request list: %d %s", code, clip(body))
	files, err := c.PushFiles(ctx, full, def, []string{heads[full]})
	t.Logf("FACT PushFiles for the initial head %s: %v err=%v", heads[full], files, err)
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
