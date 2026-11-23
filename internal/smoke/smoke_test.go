// Package smoke drives ripple over its real interface: `ripple serve` runs against a fake
// GitHub (REST, GraphQL, and repositories over smart HTTP via git-http-backend) and a fake
// module index, receives HMAC-signed webhooks over HTTP, and must open exactly one pull request.
//
// The unit under test is the built container image (RIPPLE_SMOKE_IMAGE, run with
// RIPPLE_SMOKE_RUNTIME=docker, the default, on the host network: ripple accepts plain http
// only for loopback, so the fakes live on the host's 127.0.0.1). Setting RIPPLE_SMOKE_BINARY
// to a ripple executable runs the same test against that process instead, for machines
// with no host-network container runtime (Apple `container`). With neither set it skips.
package smoke

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/testutil"
)

const secret = "smoke-secret"

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// repoHTTP serves the fake's bare repositories under root over smart HTTP, because
// ripple refuses file:// clone URLs. It returns the base URL.
func repoHTTP(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend not found at %s", backend)
	}
	h := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=ripple",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

// syncBuffer is a goroutine-safe buffer for a child process's output.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestRippleOpensExactlyOnePR(t *testing.T) {
	image, binary := os.Getenv("RIPPLE_SMOKE_IMAGE"), os.Getenv("RIPPLE_SMOKE_BINARY")
	if image == "" && binary == "" {
		t.Skip("neither RIPPLE_SMOKE_IMAGE nor RIPPLE_SMOKE_BINARY is set (`make test-smoke`, CI)")
	}
	rt := os.Getenv("RIPPLE_SMOKE_RUNTIME")
	if rt == "" {
		rt = "docker"
	}

	gh := testutil.NewGitHub(t)
	idx := testutil.NewIndex(t)
	const repo = "acme/uses-lib"
	gh.AddRepo(repo, map[string]string{"go.mod": "module example.com/uses-lib\n\ngo 1.22\n\nrequire (\n\texample.com/lib v1.0.0\n)\n"})
	gitRoot := strings.TrimPrefix(gh.GitBase, "file://")
	if out, err := exec.Command("chmod", "-R", "a+rwX", gitRoot).CombinedOutput(); err != nil {
		t.Fatalf("chmod: %v\n%s", err, out)
	}
	gitURL := repoHTTP(t, gitRoot)

	secrets := t.TempDir()
	if err := os.WriteFile(filepath.Join(secrets, "key.pem"), gh.KeyPEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.Chmod(work, 0o777); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	// RIPPLE_FEED_INTERVAL has minute granularity: the second feed poll comes a minute after the first.
	cfg := func(keyFile, workDir string) []string {
		return []string{
			"HOME=" + workDir, "RIPPLE_WORK_DIR=" + workDir,
			fmt.Sprintf("RIPPLE_LISTEN=127.0.0.1:%d", port),
			"RIPPLE_WEBHOOK_SECRET=" + secret,
			"RIPPLE_GITHUB_APP_ID=" + testutil.AppID,
			"RIPPLE_GITHUB_KEY_FILE=" + keyFile,
			"RIPPLE_GITHUB_API_URL=" + gh.APIURL,
			"RIPPLE_GITHUB_GIT_URL=" + gitURL,
			"RIPPLE_FEED_URL=" + idx.URL,
			"RIPPLE_FEED_INTERVAL=1m",
			"RIPPLE_MINIMUM_RELEASE_AGE=0",
		}
	}
	if binary != "" {
		cmd := exec.Command(binary, "serve")
		cmd.Env = append(os.Environ(), cfg(filepath.Join(secrets, "key.pem"), work)...)
		var buf syncBuffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("ripple log:\n%s", buf.String())
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	} else {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		name := "ripple-smoke-" + hex.EncodeToString(b)
		args := []string{"run", "-d", "--name", name, "--network", "host",
			"-v", secrets + ":/secrets:ro", "-v", work + ":/work"}
		for _, kv := range cfg("/secrets/key.pem", "/work") {
			args = append(args, "-e", kv)
		}
		args = append(args, image, "serve")
		if out, err := exec.Command(rt, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s run: %v\n%s", rt, err, out)
		}
		t.Cleanup(func() {
			if t.Failed() {
				out, _ := exec.Command(rt, "logs", name).CombinedOutput()
				t.Logf("container log:\n%s", out)
			}
			_ = exec.Command(rt, "rm", "-f", name).Run()
		})
	}

	hook := fmt.Sprintf("http://127.0.0.1:%d/webhook", port)
	waitFor(t, "ripple to listen", 60*time.Second, func() bool {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			c.Close()
		}
		return err == nil
	})

	post := func(event, body, sig string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, hook, strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	deliver := func(event, body string) {
		t.Helper()
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(body))
		if code := post(event, body, "sha256="+hex.EncodeToString(m.Sum(nil))); code != http.StatusAccepted {
			t.Fatalf("%s webhook: %d, want 202", event, code)
		}
	}

	// A wrong signature is refused, so the 202s below prove the signed deliveries were verified.
	if code := post("push", `{}`, "sha256=00"); code != http.StatusUnauthorized {
		t.Fatalf("badly signed push: %d, want 401", code)
	}

	deliver("installation", `{"action":"created","installation":{"id":7},"repositories":[{"full_name":"`+repo+`"}]}`)
	// The watcher records its starting cursor on the first feed poll; a release must come after it.
	waitFor(t, "the first feed poll", 120*time.Second, func() bool { return idx.Requests() > 0 })

	gh.Commit(repo, map[string]string{"README.md": "hello\n"})
	deliver("push", `{"ref":"refs/heads/main","installation":{"id":7},"repository":{"full_name":"`+repo+`","default_branch":"main"},"commits":[{"modified":["README.md"]}]}`)

	idx.Publish("example.com/lib", "v1.1.0", time.Now().Add(2*time.Second))

	waitFor(t, "one pull request", 200*time.Second, func() bool { return len(gh.PRs(repo)) >= 1 })
	// Further feed polls and webhooks must not add a second one.
	time.Sleep(5 * time.Second)
	prs := gh.PRs(repo)
	if len(prs) != 1 {
		t.Fatalf("want exactly 1 PR, got %d: %+v", len(prs), prs)
	}
	if prs[0].Base != "main" || !strings.HasPrefix(prs[0].Head, "ripple/go/") {
		t.Errorf("unexpected PR %+v", prs[0])
	}
	// The branch carries the repository's go.mod. Its content is not asserted: every Go update runs
	// `go mod tidy`, which verifies modules against GOSUMDB, and the server has no setting to turn that
	// off for the fake module here, so the unimported example.com/lib requirement is tidied away. The
	// go.mod and go.sum contents are covered by internal/e2e, which can set GoSumDB.
	if got, ok := gh.FileAt(repo, prs[0].Head, "go.mod"); !ok || !strings.Contains(got, "module example.com/uses-lib") {
		t.Errorf("go.mod missing or wrong on %s:\n%s", prs[0].Head, got)
	}
}

func waitFor(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}
