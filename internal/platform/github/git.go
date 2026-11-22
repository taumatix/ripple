package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/mod/modfile"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
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

// Checkout shallow-clones the default branch into a private temp directory. The
// clone talks to git with an environment that cannot read host configuration,
// run hooks, or use any protocol other than the one in GitBase.
func (c *Client) Checkout(ctx context.Context, repo model.Repo) (platform.Workspace, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	gb, err := url.Parse(c.GitBase)
	if err != nil || gb.Scheme == "" {
		return nil, fmt.Errorf("invalid git base URL")
	}
	dir, err := os.MkdirTemp(c.WorkDir, "ripple-")
	if err != nil {
		return nil, err
	}
	w := &workspace{dir: dir, c: c, branch: repo.DefaultBranch, installation: repo.InstallationID}
	w.env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=" + gb.Scheme,
	}
	repoDir := filepath.Join(dir, "repo")
	cloneURL := strings.TrimRight(c.GitBase, "/") + "/" + repo.FullName + ".git"
	args := []string{"clone", "--depth", "1", "--no-tags", "--single-branch", "--no-recurse-submodules"}
	if repo.DefaultBranch != "" {
		if strings.HasPrefix(repo.DefaultBranch, "-") || strings.ContainsAny(repo.DefaultBranch, " \t\n:~^?*[\\") {
			w.Close()
			return nil, fmt.Errorf("refusing unsafe branch name %q", repo.DefaultBranch)
		}
		args = append(args, "--branch", repo.DefaultBranch)
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
	installation         int64
	c                    *Client
	env                  []string // credential-free; network commands add a header per command
	root                 *os.Root

	mu      sync.Mutex
	secrets []string // every token this workspace has used, for redaction
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
	return w.runGit(ctx, dir, w.env, args...)
}

// gitNet runs a git command that talks to the remote (clone, fetch, push). It asks
// the TokenSource for an installation token at that moment, so a job that outlives
// one token never sends a stale one, and hands it to git only through that one
// process's environment, never argv or the remote URL.
func (w *workspace) gitNet(ctx context.Context, dir string, args ...string) (string, error) {
	tok, err := w.c.Tokens.Token(ctx, w.installation)
	if err != nil {
		return "", err
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
	w.mu.Lock()
	w.secrets = append(w.secrets, tok, basic)
	w.mu.Unlock()
	env := append(append([]string(nil), w.env...),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http."+strings.TrimRight(w.c.GitBase, "/")+"/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
	)
	return w.runGit(ctx, dir, env, args...)
}

func (w *workspace) runGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "protocol.version=2"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
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
	name, email := w.c.AuthorName, w.c.AuthorEmail
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

var goCommands = map[string]bool{"mod tidy": true, "build ./...": true, "vet ./...": true}

const goTimeout = 10 * time.Minute

func (w *workspace) RunGo(ctx context.Context, dir string, args ...string) (string, error) {
	if !goCommands[strings.Join(args, " ")] {
		return "", fmt.Errorf("go %s is not allowed", strings.Join(args, " "))
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
	proxy := w.c.GoProxy
	if proxy == "" {
		proxy = "https://proxy.golang.org"
	}
	cache := w.c.GoCacheDir
	if cache == "" {
		cache = filepath.Join(w.dir, "gocache")
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + w.dir,
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
	if w.c.GoSumDB != "" {
		env = append(env, "GOSUMDB="+w.c.GoSumDB)
	}
	ctx, cancel := context.WithTimeout(ctx, goTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gobin, args...)
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
	var mods []string
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
		if name != "go.mod" && name != "go.sum" && name != "go.work" && !strings.HasSuffix(name, ".go") {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symbolic link", platform.ErrUnsafeCheckout, filepath.ToSlash(rel))
		}
		if name == "go.mod" {
			mods = append(mods, p)
		}
		return nil
	})
	if err != nil {
		return err
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
