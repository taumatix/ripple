package testutil

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type issuedToken struct {
	expires time.Time
	revoked bool
}

// GitHTTP is the fake GitHub's git smart-HTTP server: git-http-backend over the
// same bare repositories, accepting only a live installation token sent as HTTP
// Basic auth (user x-access-token), the way github.com does.
type GitHTTP struct {
	// URL is the git base URL (http://127.0.0.1:port), usable as Client.GitBase.
	URL string

	mu       sync.Mutex
	rejected int
	seen     []string
	sent     int64
}

// countWriter counts the bytes a response body carries.
type countWriter struct {
	http.ResponseWriter
	n  *int64
	mu *sync.Mutex
}

func (c countWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.mu.Lock()
	*c.n += int64(n)
	c.mu.Unlock()
	return n, err
}

// BytesServed is the total of response body bytes the server has sent, the
// closest a test gets to "what a clone downloaded".
func (h *GitHTTP) BytesServed() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sent
}

// ExpireTokens revokes every installation token minted so far, as if their
// lifetime had run out on the server.
func (g *GitHub) ExpireTokens() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range g.issued {
		t.revoked = true
	}
}

func (g *GitHub) tokenLive(tok string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.issued[tok]
	return ok && !t.revoked && time.Now().Before(t.expires)
}

// ServeGit starts the git smart-HTTP server for the repositories added with AddRepo.
func (g *GitHub) ServeGit() *GitHTTP {
	g.t.Helper()
	backend, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		g.t.Skipf("git not available: %v", err)
	}
	h := &cgi.Handler{
		Path: strings.TrimSpace(string(backend)) + "/git-http-backend",
		Env: []string{"GIT_PROJECT_ROOT=" + g.root, "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"},
	}
	gh := &GitHTTP{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := basicAuth(r)
		gh.mu.Lock()
		gh.seen = append(gh.seen, r.URL.String())
		gh.mu.Unlock()
		if !ok || user != "x-access-token" || !g.tokenLive(pass) {
			gh.mu.Lock()
			gh.rejected++
			gh.mu.Unlock()
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		// git-http-backend is CGI, which cannot take a chunked body; buffer it.
		if r.ContentLength < 0 {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			r.Body, r.ContentLength, r.TransferEncoding = io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
			r.Header.Del("Transfer-Encoding")
		}
		h.ServeHTTP(countWriter{w, &gh.sent, &gh.mu}, r)
	}))
	g.t.Cleanup(srv.Close)
	gh.URL = srv.URL
	return gh
}

func basicAuth(r *http.Request) (user, pass string, ok bool) {
	b, found := strings.CutPrefix(r.Header.Get("Authorization"), "Basic ")
	if !found {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(b)
	if err != nil {
		return "", "", false
	}
	user, pass, ok = strings.Cut(string(raw), ":")
	return
}

// Rejected is the number of requests refused for missing, unknown or expired credentials.
func (h *GitHTTP) Rejected() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rejected
}

// RequestURLs lists every request URL the server saw (path and query only).
func (h *GitHTTP) RequestURLs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}
