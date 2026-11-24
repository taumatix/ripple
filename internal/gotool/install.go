// Package gotool installs Go releases for verification from a module proxy.
//
// The Go project publishes every release as the module golang.org/toolchain at
// v0.0.1-go<release>.<os>-<arch>, which is how the go command's own toolchain switching
// gets them. Installer does the same into the directory RIPPLE_GO_TOOLCHAIN_DIR names, so
// verification of a bump to a Go the host lacks needs no manual install: the zip is fetched
// by `go mod download`, which checks it against the checksum database, and unpacked as
// go<release>/bin/go, the layout platform.FindGo reads.
package gotool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/module"

	"github.com/taumatix/ripple/internal/platform"
)

// toolchainModule is the module the Go project publishes its releases as.
const toolchainModule = "golang.org/toolchain"

// maxList bounds the version list read from the proxy.
const maxList = 8 << 20

// downloadTimeout bounds one `go mod download` of a release (about 70 MB).
const downloadTimeout = 10 * time.Minute

// Installer installs Go releases into Dir on demand. The zero value of the optional fields
// is the host's own platform, the go command on PATH and the default checksum database.
type Installer struct {
	// Dir is the toolchain directory (RIPPLE_GO_TOOLCHAIN_DIR); a release lands in
	// Dir/go<release>.
	Dir string
	// Proxy is the module proxy releases come from (RIPPLE_GOPROXY).
	Proxy string
	// User and Token authenticate to Proxy (HTTP basic auth); the token is only ever sent
	// over https and is redacted from errors.
	User, Token string
	// SumDB is GOSUMDB for the download; empty is the go command's default, "off" skips it.
	SumDB string
	// GoBin is the go command that downloads; empty is "go" on PATH.
	GoBin string
	// OS and Arch name the release's platform; empty is the host's.
	OS, Arch string
	// HTTP lists the proxy's versions; nil is http.DefaultClient.
	HTTP *http.Client

	mu   sync.Mutex
	busy map[string]*sync.Mutex
}

// ErrNoRelease is returned when the proxy lists no release that satisfies the go.mod.
var ErrNoRelease = errors.New("no Go release on the proxy satisfies the go.mod")

// Ensure returns the go binary for the release a go.mod names (see platform.FindGo for
// how the go and toolchain lines choose), installing it from the proxy when the directory
// has none. release is the version without the "go" prefix.
func (i *Installer) Ensure(ctx context.Context, goLine, toolchainLine string) (bin, release string, err error) {
	if bin, release = platform.FindGo(i.Dir, goLine, toolchainLine); bin != "" {
		return bin, release, nil
	}
	want, err := i.resolve(ctx, goLine, toolchainLine)
	if err != nil {
		return "", "", err
	}
	// One install per release at a time in this process; across processes the final
	// rename decides, and the loser finds the release in place.
	l := i.lock(want)
	l.Lock()
	defer l.Unlock()
	if bin, release = platform.FindGo(i.Dir, "", "go"+want); bin != "" {
		return bin, release, nil
	}
	if err := i.install(ctx, want); err != nil {
		return "", "", err
	}
	if bin, release = platform.FindGo(i.Dir, "", "go"+want); bin == "" {
		return "", "", fmt.Errorf("go%s installed but %s/go%s/bin/go is missing", want, i.Dir, want)
	}
	return bin, release, nil
}

func (i *Installer) lock(release string) *sync.Mutex {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.busy == nil {
		i.busy = map[string]*sync.Mutex{}
	}
	if i.busy[release] == nil {
		i.busy[release] = &sync.Mutex{}
	}
	return i.busy[release]
}

func (i *Installer) platformSuffix() string {
	goos, arch := i.OS, i.Arch
	if goos == "" {
		goos = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	return goos + "-" + arch
}

var goLineRe = regexp.MustCompile(`^(?:go)?(1\.\d+)(?:\.(\d+))?$`)

// resolve picks the release to install: the toolchain line exactly; else the newest patch
// of the go line's minor the proxy lists for this platform that is not older than the line.
func (i *Installer) resolve(ctx context.Context, goLine, toolchainLine string) (string, error) {
	if toolchainLine != "" {
		m := goLineRe.FindStringSubmatch(toolchainLine)
		if m == nil {
			return "", fmt.Errorf("%w: toolchain %q is not a release", ErrNoRelease, toolchainLine)
		}
		return strings.TrimPrefix(toolchainLine, "go"), nil // "go1.25" is a release name of its own
	}
	m := goLineRe.FindStringSubmatch(goLine)
	if m == nil {
		return "", fmt.Errorf("%w: go line %q is not a release", ErrNoRelease, goLine)
	}
	min := 0
	if m[2] != "" {
		min, _ = strconv.Atoi(m[2])
	}
	versions, err := i.list(ctx)
	if err != nil {
		return "", err
	}
	best, bestRel := -1, ""
	for _, v := range versions {
		rel, ok := releaseOf(v, i.platformSuffix())
		if !ok {
			continue
		}
		rm := goLineRe.FindStringSubmatch(rel)
		if rm == nil || rm[1] != m[1] {
			continue
		}
		patch := 0
		if rm[2] != "" {
			patch, _ = strconv.Atoi(rm[2])
		}
		if patch >= min && patch > best {
			best, bestRel = patch, rel
		}
	}
	if best < 0 {
		return "", fmt.Errorf("%w: go %s on %s", ErrNoRelease, goLine, i.platformSuffix())
	}
	return bestRel, nil
}

// releaseOf reads "1.25.3" out of "v0.0.1-go1.25.3.linux-amd64" for suffix "linux-amd64".
func releaseOf(version, suffix string) (string, bool) {
	rest, ok := strings.CutPrefix(version, "v0.0.1-go")
	if !ok {
		return "", false
	}
	rel, ok := strings.CutSuffix(rest, "."+suffix)
	return rel, ok
}

func (i *Installer) list(ctx context.Context) ([]string, error) {
	esc, err := module.EscapePath(toolchainModule)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(i.Proxy, "/")+"/"+esc+"/@v/list", nil)
	if err != nil {
		return nil, err
	}
	if i.Token != "" {
		req.SetBasicAuth(i.User, i.Token)
	}
	c := i.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, i.redact(fmt.Errorf("list Go releases: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("list Go releases: proxy answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxList))
	if err != nil {
		return nil, fmt.Errorf("list Go releases: %w", err)
	}
	return strings.Fields(string(b)), nil
}

// install downloads go<release> with `go mod download` into a throwaway module cache (which
// is where the checksum database is consulted) and moves a copy to Dir/go<release>.
func (i *Installer) install(ctx context.Context, release string) error {
	if err := os.MkdirAll(i.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(i.Dir, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	cache := filepath.Join(tmp, "modcache")
	ver := "v0.0.1-go" + release + "." + i.platformSuffix()

	proxy := strings.TrimRight(i.Proxy, "/")
	if i.Token != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Scheme != "https" {
			return errors.New("refusing to send the proxy credential over a non-https proxy")
		}
		u.User = url.UserPassword(i.User, i.Token)
		proxy = u.String()
	}
	gobin := i.GoBin
	if gobin == "" {
		gobin = "go"
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gobin, "mod", "download", "-json", toolchainModule+"@"+ver)
	cmd.Dir = tmp
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "TMPDIR=" + tmp,
		"GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=-modcacherw", "GOVCS=*:off",
		"GOMODCACHE=" + cache, "GOPATH=" + filepath.Join(tmp, "gopath"), "GOCACHE=" + filepath.Join(tmp, "gocache"),
		"GOPROXY=" + proxy,
	}
	if i.SumDB != "" {
		cmd.Env = append(cmd.Env, "GOSUMDB="+i.SumDB)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return i.redact(fmt.Errorf("go%s: go mod download: %w: %s", release, err, tail(stderr.String()+out.String())))
	}
	var res struct{ Dir, Error string }
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Dir == "" {
		return i.redact(fmt.Errorf("go%s: go mod download answered %q", release, tail(out.String())))
	}
	if res.Error != "" {
		return i.redact(fmt.Errorf("go%s: %s", release, res.Error))
	}
	stage := filepath.Join(tmp, "go"+release)
	if err := copyTree(res.Dir, stage); err != nil {
		return fmt.Errorf("go%s: unpack: %w", release, err)
	}
	final := filepath.Join(i.Dir, "go"+release)
	if err := os.Rename(stage, final); err != nil {
		if _, serr := os.Stat(filepath.Join(final, "bin", "go")); serr == nil {
			return nil // another process installed it first
		}
		return fmt.Errorf("go%s: %w", release, err)
	}
	return nil
}

// copyTree copies src to dst, making what a Go release runs executable: the module zip
// extraction writes every file read-only without execute bits.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		r, err := os.Open(p)
		if err != nil {
			return err
		}
		defer r.Close()
		mode := os.FileMode(0o644)
		if slash := filepath.ToSlash(rel); strings.HasPrefix(slash, "bin/") || strings.HasPrefix(slash, "pkg/tool/") {
			mode = 0o755
		}
		w, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, r); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	})
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2<<10 {
		s = "..." + s[len(s)-2<<10:]
	}
	return s
}

// redact removes the proxy credential from err's text.
func (i *Installer) redact(err error) error {
	if i.Token == "" || err == nil {
		return err
	}
	msg := err.Error()
	for _, s := range []string{i.Token, url.QueryEscape(i.Token), url.PathEscape(i.Token)} {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	return errors.New(msg)
}
