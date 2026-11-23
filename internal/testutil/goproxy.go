package testutil

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/zip"
)

// GoProxy is a fake module proxy (the GOPROXY protocol) serving modules built from
// in-memory file sets, so the go command can tidy and build a migrated checkout
// without the network.
type GoProxy struct {
	URL string
	srv *httptest.Server
	mu  sync.Mutex
	mod map[string]map[string]map[string]string // path -> version -> file -> content

	user, pass string       // when set, every request must carry this basic auth
	reqs       []GoProxyReq // every request, for tests that assert who authenticated
}

// GoProxyReq is one request the proxy saw.
type GoProxyReq struct {
	Path       string
	Authorized bool // carried the required basic auth (true when none is required)
	HasAuth    bool // carried any Authorization header
}

// RequireAuth makes the proxy answer 401 unless a request carries user:pass basic auth.
func (p *GoProxy) RequireAuth(user, pass string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.user, p.pass = user, pass
}

// Requests returns a copy of every request seen so far.
func (p *GoProxy) Requests() []GoProxyReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]GoProxyReq(nil), p.reqs...)
}

func NewGoProxy(t testing.TB) *GoProxy {
	t.Helper()
	p := &GoProxy{mod: map[string]map[string]map[string]string{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	p.URL = p.srv.URL
	return p
}

// Publish adds one module version. go.mod is generated when files has none.
func (p *GoProxy) Publish(path, version string, files map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mod[path] == nil {
		p.mod[path] = map[string]map[string]string{}
	}
	cp := map[string]string{}
	for k, v := range files {
		cp[k] = v
	}
	if _, ok := cp["go.mod"]; !ok {
		cp["go.mod"] = "module " + path + "\n\ngo 1.21\n"
	}
	p.mod[path][version] = cp
}

func (p *GoProxy) serve(w http.ResponseWriter, r *http.Request) {
	u, pw, has := r.BasicAuth()
	p.mu.Lock()
	authorized := p.user == "" || (has && u == p.user && pw == p.pass)
	p.reqs = append(p.reqs, GoProxyReq{Path: r.URL.Path, Authorized: authorized, HasAuth: r.Header.Get("Authorization") != ""})
	p.mu.Unlock()
	if !authorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="proxy"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	escPath, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/@v/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	mpath, err := module.UnescapePath(escPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p.mu.Lock()
	versions := p.mod[mpath]
	p.mu.Unlock()
	if versions == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if rest == "list" {
		var vs []string
		for v := range versions {
			vs = append(vs, v)
		}
		sort.Strings(vs)
		_, _ = fmt.Fprint(w, strings.Join(vs, "\n"))
		return
	}
	dot := strings.LastIndex(rest, ".")
	escVer, kind := rest[:dot], rest[dot+1:]
	version, err := module.UnescapeVersion(escVer)
	files, found := versions[version]
	if err != nil || !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch kind {
	case "info":
		_, _ = fmt.Fprintf(w, `{"Version":%q,"Time":%q}`, version, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
	case "mod":
		_, _ = fmt.Fprint(w, files["go.mod"])
	case "zip":
		var files2 []zip.File
		for name, content := range files {
			files2 = append(files2, memFile{name: name, content: content})
		}
		sort.Slice(files2, func(i, j int) bool { return files2[i].Path() < files2[j].Path() })
		var buf bytes.Buffer
		if err := zip.Create(&buf, module.Version{Path: mpath, Version: version}, files2); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(buf.Bytes())
	default:
		http.NotFound(w, r)
	}
}

type memFile struct{ name, content string }

func (f memFile) Path() string { return f.name }
func (f memFile) Lstat() (os.FileInfo, error) {
	return memInfo{name: path.Base(f.name), size: int64(len(f.content))}, nil
}
func (f memFile) Open() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.content)), nil
}

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0o644 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
