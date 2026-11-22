// Package gitws is the git checkout every platform shares: a shallow clone of the
// default branch in a private directory, confined file access, commit-and-push of a
// ripple/ branch, and the allow-listed go commands. A platform supplies the clone URL
// and a credential; nothing here knows which host it talks to.
package gitws

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/sandbox"
)

const (
	maxFileBytes = 1 << 20
	gitTimeout   = 5 * time.Minute
)

var branchName = regexp.MustCompile(`^ripple/[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func checkBranch(b string) error {
	if !branchName.MatchString(b) || strings.Contains(b, "..") || strings.Contains(b, "//") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") || len(b) > 200 {
		return fmt.Errorf("refusing unsafe branch name %q", b)
	}
	return nil
}

// Options are what every checkout on one host shares.
type Options struct {
	// GitBase is the clone URL prefix, e.g. https://github.com. Every clone URL must
	// start with it; the credential header is scoped to it and its scheme is the only
	// protocol git may use.
	GitBase string
	// AuthorName and AuthorEmail identify commits.
	AuthorName, AuthorEmail string
	// WorkDir is where checkouts are created; empty means the OS temp dir.
	WorkDir string
	// GoProxy is the only module source the go command may use (GOPROXY); empty
	// means https://proxy.golang.org. GoSumDB is GOSUMDB, empty means the default.
	GoProxy, GoSumDB string
	// GoCacheDir shares the build cache between checkouts; empty means a private
	// cache per checkout, which is slower but isolates repositories from each other.
	GoCacheDir string
	// Sandbox confines the go command and local git commands run on an untrusted
	// checkout. Nil means no confinement. Network git commands (clone, fetch, push)
	// run unconfined: they hold the credential and execute no repository code.
	Sandbox sandbox.Backend
}

// CredentialFunc returns the credential for one network git command. It is called
// per command, so a job that outlives one short-lived token still pushes.
type CredentialFunc func(ctx context.Context) (platform.Credential, error)

// Checkout shallow-clones cloneURL into a private temp directory, on branch when it
// is set and on the remote's default branch otherwise. The clone talks to git with an
// environment that cannot read host configuration, run hooks, or use any protocol
// other than the one in o.GitBase.
func Checkout(ctx context.Context, o Options, cloneURL, branch string, cred CredentialFunc) (platform.Workspace, error) {
	gb, err := url.Parse(o.GitBase)
	if err != nil || gb.Scheme == "" {
		return nil, fmt.Errorf("invalid git base URL")
	}
	base := strings.TrimRight(o.GitBase, "/")
	if !strings.HasPrefix(cloneURL, base+"/") {
		return nil, fmt.Errorf("clone URL is not under the git base URL")
	}
	if branch != "" && (strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, " \t\n:~^?*[\\")) {
		return nil, fmt.Errorf("refusing unsafe branch name %q", branch)
	}
	dir, err := os.MkdirTemp(o.WorkDir, "ripple-")
	if err != nil {
		return nil, err
	}
	w := &workspace{dir: dir, o: o, base: base, cred: cred, branch: branch}
	w.env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"TMPDIR=" + dir, // writable under the sandbox; the macOS git shim caches here
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=" + gb.Scheme,
	}
	repoDir := filepath.Join(dir, "repo")
	args := []string{"clone", "--depth", "1", "--filter=blob:limit=1m", "--no-tags", "--single-branch", "--no-recurse-submodules"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", cloneURL, repoDir)
	if _, err := w.gitNet(ctx, dir, args...); err != nil {
		w.Close()
		return nil, err
	}
	w.repoDir = repoDir
	if w.branch == "" {
		out, err := w.git(ctx, repoDir, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			w.Close()
			return nil, err
		}
		w.branch = strings.TrimSpace(out)
	}
	if w.root, err = os.OpenRoot(repoDir); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

type workspace struct {
	dir, repoDir, branch string
	o                    Options
	base                 string
	cred                 CredentialFunc
	env                  []string // credential-free; network commands add a header per command
	root                 *os.Root

	mu      sync.Mutex
	secrets []string // every credential this workspace has used, for redaction
}

func (w *workspace) DefaultBranch() string { return w.branch }

func (w *workspace) redact(s string) string {
	w.mu.Lock()
	secrets := append([]string(nil), w.secrets...)
	w.mu.Unlock()
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "***")
		}
	}
	return s
}

// git runs a local git command: no credentials are in its environment.
func (w *workspace) git(ctx context.Context, dir string, args ...string) (string, error) {
	return w.runGit(ctx, dir, w.env, true, args...)
}

// gitNet runs a git command that talks to the remote (clone, fetch, push). It asks
// for a credential at that moment and hands it to git only through that one
// process's environment, never argv or the remote URL. An empty secret sends none.
func (w *workspace) gitNet(ctx context.Context, dir string, args ...string) (string, error) {
	env := w.env
	if w.cred != nil {
		c, err := w.cred(ctx)
		if err != nil {
			return "", err
		}
		if c.Secret != "" {
			basic := base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Secret))
			w.mu.Lock()
			w.secrets = append(w.secrets, c.Secret, basic)
			w.mu.Unlock()
			env = append(append([]string(nil), w.env...),
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=http."+w.base+"/.extraHeader",
				"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
			)
		}
	}
	return w.runGit(ctx, dir, env, false, args...)
}

func (w *workspace) runGit(ctx context.Context, dir string, env []string, confined bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "protocol.version=2"}, args...)
	argv := append([]string{gitBinary()}, full...)
	if confined {
		var err error
		if argv, err = w.confine(sandbox.Policy{Workdir: dir, Writable: w.dirOutside(dir)}, argv); err != nil {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, snippet([]byte(w.redact(errb.String()))))
	}
	return out.String(), nil
}

func cleanPath(p string) (string, error) {
	if !fs.ValidPath(p) || p == "." {
		return "", fmt.Errorf("invalid path %q", p)
	}
	if p == ".git" || strings.HasPrefix(p, ".git/") {
		return "", fmt.Errorf("refusing to touch %q", p)
	}
	return p, nil
}

func (w *workspace) ReadFile(p string) ([]byte, error) {
	p, err := cleanPath(p)
	if err != nil {
		return nil, err
	}
	f, err := w.root.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, platform.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFileBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", p, maxFileBytes)
	}
	return b, nil
}

func (w *workspace) WriteFile(p string, data []byte) error {
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	if dir := path.Dir(p); dir != "." {
		if err := w.root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := w.root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (w *workspace) Walk(fn func(string) error) error {
	return fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return fn(p)
	})
}

func (w *workspace) CommitAndPush(ctx context.Context, branch, message string) (bool, error) {
	if err := checkBranch(branch); err != nil {
		return false, err
	}
	st, err := w.git(ctx, w.repoDir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(st) == "" {
		return false, nil
	}
	name, email := w.o.AuthorName, w.o.AuthorEmail
	if name == "" {
		name = "ripple[bot]"
	}
	if email == "" {
		email = "ripple@users.noreply.invalid"
	}
	for _, a := range [][]string{
		{"checkout", "-B", branch},
		{"add", "-A"},
		{"-c", "user.name=" + name, "-c", "user.email=" + email, "commit", "--no-verify", "--no-gpg-sign", "-m", message},
		{"push", "--force", "origin", "HEAD:refs/heads/" + branch},
	} {
		run := w.git
		if a[0] == "push" {
			run = w.gitNet
		}
		if _, err := run(ctx, w.repoDir, a...); err != nil {
			return false, err
		}
	}
	return true, nil
}

var goCommands = map[string]bool{"mod tidy": true, "mod vendor": true, "build ./...": true, "vet ./...": true, "work sync": true}

const goTimeout = 10 * time.Minute

func (w *workspace) RunGo(ctx context.Context, dir string, args ...string) (string, error) {
	if !goCommands[strings.Join(args, " ")] {
		return "", fmt.Errorf("go %s is not allowed", strings.Join(args, " "))
	}
	inWork := platform.InWorkspaceMode(ctx)
	if args[0] == "work" && !inWork {
		return "", fmt.Errorf("go %s needs workspace mode", strings.Join(args, " "))
	}
	if !fs.ValidPath(dir) || dir == ".git" || strings.HasPrefix(dir, ".git/") {
		return "", fmt.Errorf("invalid directory %q", dir)
	}
	root, err := filepath.EvalSymlinks(w.repoDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, abs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("directory %q escapes the checkout", dir)
	}
	if err := checkGoSafe(root); err != nil {
		return "", err
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		return "", platform.ErrNoToolchain
	}
	proxy := w.o.GoProxy
	if proxy == "" {
		proxy = "https://proxy.golang.org"
	}
	cache := w.o.GoCacheDir
	if cache == "" {
		cache = filepath.Join(w.dir, "gocache")
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + w.dir,
		"TMPDIR=" + w.dir,
		"GOPATH=" + filepath.Join(w.dir, "gopath"),
		"GOCACHE=" + cache,
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOFLAGS=-buildvcs=false -modcacherw",
		"CGO_ENABLED=0",
		"GOPROXY=" + proxy,
		"GOVCS=*:off",
	}
	if w.o.GoSumDB != "" {
		env = append(env, "GOSUMDB="+w.o.GoSumDB)
	}
	if inWork {
		env = slices.DeleteFunc(env, func(kv string) bool { return kv == "GOWORK=off" })
	}
	ctx, cancel := context.WithTimeout(ctx, goTimeout)
	defer cancel()
	pol := sandbox.Policy{Workdir: abs, Writable: w.dirOutside(abs)}
	if cache != "" && !strings.HasPrefix(cache, w.dir+string(filepath.Separator)) {
		pol.Writable = append(pol.Writable, cache)
	}
	if real, err := filepath.EvalSymlinks(gobin); err == nil {
		pol.ReadOnly = append(pol.ReadOnly, filepath.Dir(filepath.Dir(real)))
	}
	if w.sandboxed() {
		gate, err := sandbox.NewGate(proxy)
		if err != nil {
			return "", err
		}
		defer gate.Close()
		pol.LoopbackPorts = []int{gate.Port()}
		for i, e := range env {
			if strings.HasPrefix(e, "GOPROXY=") {
				env[i] = "GOPROXY=" + gate.URL
			}
		}
	}
	argv, err := w.confine(pol, append([]string{gobin}, args...))
	if err != nil {
		return "", fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = abs
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	text := w.redact(out.String())
	if len(text) > 16<<10 {
		text = "...\n" + text[len(text)-16<<10:]
	}
	if runErr != nil {
		return text, fmt.Errorf("go %s: %w", strings.Join(args, " "), runErr)
	}
	return text, nil
}

// gitBinary names the git to run. On macOS /usr/bin/git is a shim that asks xcrun to
// locate the real one and writes a cache outside the workspace, which the sandbox
// refuses; the toolchain's own binary does neither, so it is used when present.
func gitBinary() string {
	if runtime.GOOS == "darwin" {
		d := os.Getenv("DEVELOPER_DIR")
		if d == "" {
			d = "/Library/Developer/CommandLineTools"
		}
		if real := filepath.Join(d, "usr", "bin", "git"); isExecutable(real) {
			if p, err := exec.LookPath("git"); err != nil || p == "/usr/bin/git" {
				return real
			}
		}
	}
	return "git"
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// sandboxed reports whether commands run under a real confinement backend.
func (w *workspace) sandboxed() bool {
	return w.o.Sandbox != nil && w.o.Sandbox.Name() != "none"
}

// confine wraps argv in the configured sandbox; with none configured it is unchanged.
func (w *workspace) confine(p sandbox.Policy, argv []string) ([]string, error) {
	if w.o.Sandbox == nil {
		return argv, nil
	}
	return w.o.Sandbox.Wrap(p, argv)
}

// dirOutside returns the workspace root as an extra writable path when dir is a
// subdirectory of it, so a command started in the checkout can still write beside it.
func (w *workspace) dirOutside(dir string) []string {
	if dir == w.dir {
		return nil
	}
	return []string{w.dir}
}

func (w *workspace) Reset(ctx context.Context) error {
	if _, err := w.git(ctx, w.repoDir, "checkout", "--force", w.branch); err != nil {
		return err
	}
	_, err := w.git(ctx, w.repoDir, "clean", "-fdxq")
	return err
}

func (w *workspace) Close() error {
	if w.root != nil {
		w.root.Close()
	}
	return os.RemoveAll(w.dir)
}

// checkGoSafe refuses a checkout the go tool must not be run in. The repository is
// untrusted: a go.mod, go.sum, go.work or .go file that is a symlink lets "go mod
// tidy" write through it to a path outside the checkout, and a replace directive
// with an absolute or escaping local target makes go read arbitrary server paths
// whose contents then surface in error output. No go process has started when this
// returns an error. root must already be symlink-resolved.
func checkGoSafe(root string) error {
	var mods, works []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "go.mod" && name != "go.sum" && name != "go.work" && name != "go.work.sum" && !strings.HasSuffix(name, ".go") {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symbolic link", platform.ErrUnsafeCheckout, filepath.ToSlash(rel))
		}
		switch name {
		case "go.mod":
			mods = append(mods, p)
		case "go.work":
			works = append(works, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, work := range works {
		if err := checkWorkSafe(root, work); err != nil {
			return err
		}
	}
	for _, mod := range mods {
		data, err := os.ReadFile(mod)
		if err != nil {
			return err
		}
		f, err := modfile.Parse(mod, data, nil)
		if err != nil {
			continue // go will report the syntax error itself
		}
		for _, r := range f.Replace {
			if !modfile.IsDirectoryPath(r.New.Path) {
				continue
			}
			rel, _ := filepath.Rel(root, mod)
			target := filepath.FromSlash(r.New.Path)
			if filepath.IsAbs(target) || strings.HasPrefix(r.New.Path, "/") {
				return fmt.Errorf("%w: %s replaces %s with the absolute path %s", platform.ErrUnsafeCheckout, filepath.ToSlash(rel), r.Old.Path, r.New.Path)
			}
			abs := filepath.Join(filepath.Dir(mod), target)
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				abs = resolved
			}
			if r2, err := filepath.Rel(root, abs); err != nil || r2 == ".." || strings.HasPrefix(r2, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%w: %s replaces %s with %s, which is outside the repository", platform.ErrUnsafeCheckout, filepath.ToSlash(rel), r.Old.Path, r.New.Path)
			}
		}
	}
	return nil
}

// checkWorkSafe refuses a go.work that points outside the repository: with the
// workspace on, a use directive or a directory replace is read like a module.
func checkWorkSafe(root, work string) error {
	data, err := os.ReadFile(work)
	if err != nil {
		return err
	}
	f, err := modfile.ParseWork(work, data, nil)
	if err != nil {
		return nil // go will report the syntax error itself
	}
	rel, _ := filepath.Rel(root, work)
	rel = filepath.ToSlash(rel)
	inside := func(p string) bool {
		if filepath.IsAbs(filepath.FromSlash(p)) || strings.HasPrefix(p, "/") {
			return false
		}
		abs := filepath.Join(filepath.Dir(work), filepath.FromSlash(p))
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		r, err := filepath.Rel(root, abs)
		return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
	}
	for _, u := range f.Use {
		if !inside(u.Path) {
			return fmt.Errorf("%w: %s uses %s, which is outside the repository", platform.ErrUnsafeCheckout, rel, u.Path)
		}
	}
	for _, r := range f.Replace {
		if modfile.IsDirectoryPath(r.New.Path) && !inside(r.New.Path) {
			return fmt.Errorf("%w: %s replaces %s with %s, which is outside the repository", platform.ErrUnsafeCheckout, rel, r.Old.Path, r.New.Path)
		}
	}
	return nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
