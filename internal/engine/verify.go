package engine

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/platform"
)

// migration is one major bump: a requirement that moved to a new module path.
type migration struct{ from, to string }

// goMods lists the directories of every go.mod in the checkout (vendor/ excluded).
func (r *runner) goMods() ([]string, error) {
	var dirs []string
	err := r.ws.Walk(func(p string) error {
		if path.Base(p) == "go.mod" {
			dirs = append(dirs, path.Dir(p))
		}
		return nil
	})
	return dirs, err
}

// goWorks returns, per go.work file, the directory it sits in and the module
// directories it uses.
func (r *runner) goWorks() (map[string][]string, error) {
	works := map[string][]string{}
	var files []string
	if err := r.ws.Walk(func(p string) error {
		if path.Base(p) == "go.work" {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := r.ws.ReadFile(f)
		if err != nil {
			return nil, err
		}
		wf, err := modfile.ParseWork(f, data, nil)
		if err != nil {
			continue // go reports the syntax error when the workspace is used
		}
		base := path.Dir(f)
		uses := []string{}
		for _, u := range wf.Use {
			uses = append(uses, path.Join(base, u.Path))
		}
		works[base] = uses
	}
	return works, nil
}

// rewriteWorks applies this PR's major bumps to the replace directives of every go.work.
func (r *runner) rewriteWorks() error {
	if len(r.migrations) == 0 {
		return nil
	}
	works, err := r.goWorks()
	if err != nil {
		return err
	}
	for dir := range works {
		file := path.Join(dir, "go.work")
		content, err := r.ws.ReadFile(file)
		if err != nil {
			return err
		}
		out, changed := content, false
		for _, m := range r.migrations {
			var c bool
			if out, c, err = manager.RewriteGoWork(out, m.from, m.to); err != nil {
				return err
			}
			changed = changed || c
		}
		if changed {
			if err := r.ws.WriteFile(file, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// verifyDirs is every module directory to refresh: those whose go.mod was edited,
// plus any other module that requires an updated dependency or one of the edited
// modules (directly or indirectly), since their go.mod and go.sum go stale too.
func (r *runner) verifyDirs(olds []string) ([]string, error) {
	set := map[string]bool{}
	names := map[string]bool{}
	for d := range r.touched {
		set[d] = true
		if data, err := r.ws.ReadFile(path.Join(d, "go.mod")); err == nil {
			if f, err := modfile.ParseLax("go.mod", data, nil); err == nil && f.Module != nil {
				names[f.Module.Mod.Path] = true
			}
		}
	}
	for _, o := range olds {
		names[o] = true
	}
	all, err := r.goMods()
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if set[d] {
			continue
		}
		data, err := r.ws.ReadFile(path.Join(d, "go.mod"))
		if err != nil {
			return nil, err
		}
		f, err := modfile.ParseLax(path.Join(d, "go.mod"), data, nil)
		if err != nil {
			continue
		}
		for _, req := range f.Require {
			if names[req.Mod.Path] {
				set[d] = true
				break
			}
		}
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func (r *runner) hasVendor(dir string) bool {
	_, err := r.ws.ReadFile(path.Join(dir, "vendor", "modules.txt"))
	return err == nil
}

// verify refreshes go.sum (and vendor/) of every module an update touched, then
// builds and vets it, and does the same for any workspace using those modules. It
// returns the output of the first step that failed, or "" when all passed.
func (r *runner) verify(olds []string) (string, error) {
	dirs, err := r.verifyDirs(olds)
	if err != nil {
		return "", err
	}
	run := func(dir string, workspace bool, members []string, args ...string) (string, bool, error) {
		ctx := r.ctx
		if workspace {
			ctx = platform.WithWorkspaceMode(ctx)
		}
		ctx = platform.WithGoVerify(ctx, r.goVerify(members, args))
		out, err := r.ws.RunGo(ctx, dir, args...)
		if err == nil {
			return "", true, nil
		}
		if errors.Is(err, platform.ErrNoToolchain) || errors.Is(err, platform.ErrNoCompiler) || errors.Is(err, platform.ErrUnsafeCheckout) {
			return "", false, err
		}
		return fmt.Sprintf("$ go %s   (in %s)\n%s\n%v", strings.Join(args, " "), dir, out, err), false, nil
	}
	for _, dir := range dirs {
		steps := [][]string{{"mod", "tidy"}}
		if r.hasVendor(dir) {
			steps = append(steps, []string{"mod", "vendor"})
		}
		steps = append(steps, []string{"build", "./..."}, []string{"vet", "./..."})
		for _, args := range steps {
			if failure, ok, err := run(dir, false, []string{dir}, args...); err != nil || !ok {
				return failure, err
			}
		}
		r.noteUncompiledCgo(dir)
	}
	works, err := r.goWorks()
	if err != nil {
		return "", err
	}
	var roots []string
	for w := range works {
		roots = append(roots, w)
	}
	sort.Strings(roots)
	inSet := map[string]bool{}
	for _, d := range dirs {
		inSet[d] = true
	}
	for _, w := range roots {
		used := false
		for _, u := range works[w] {
			used = used || inSet[u]
		}
		if !used {
			continue
		}
		if failure, ok, err := run(w, true, works[w], "work", "sync"); err != nil || !ok {
			return failure, err
		}
		for _, u := range works[w] {
			for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
				if failure, ok, err := run(u, true, []string{u}, args...); err != nil || !ok {
					return failure, err
				}
			}
		}
	}
	return "", nil
}

// goVerify is what RunGo needs to know for one step over the module directories
// members: the union of their updates' cgo and private-modules settings, plus the
// server's proxy credential, which only a step that executes no repository code
// (tidy, vendor, work sync) may carry.
func (r *runner) goVerify(members []string, args []string) platform.GoVerify {
	v := platform.GoVerify{CC: r.e.GoCC}
	seen := map[string]bool{}
	for _, m := range members {
		u, ok := r.cfg.UpdateFor("gomod", path.Join(m, "go.mod"))
		if !ok {
			continue
		}
		v.CGO = v.CGO || u.CGO
		for _, p := range u.PrivateModules {
			if !seen[p] {
				seen[p] = true
				v.Private = append(v.Private, p)
			}
		}
	}
	sort.Strings(v.Private)
	netStep := args[0] == "mod" || args[0] == "work"
	if netStep && r.e.GoProxyToken != "" {
		v.Username, v.Token, v.Credentialed = r.e.GoProxyUser, r.e.GoProxyToken, true
	}
	return v
}

var importC = regexp.MustCompile(`(?m)^\s*(?:import\s+)?"C"\s*$`)

// noteUncompiledCgo records, for the PR body, that a module with cgo files was built
// with cgo off, so a green build does not read as proof the C parts compile.
func (r *runner) noteUncompiledCgo(dir string) {
	if u, ok := r.cfg.UpdateFor("gomod", path.Join(dir, "go.mod")); ok && u.CGO {
		return
	}
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	found := ""
	_ = r.ws.Walk(func(p string) error {
		if found != "" || !strings.HasSuffix(p, ".go") || !strings.HasPrefix(p, prefix) {
			return nil
		}
		if data, err := r.ws.ReadFile(p); err == nil && importC.Match(data) {
			found = p
		}
		return nil
	})
	if found == "" {
		return
	}
	r.notes = append(r.notes, fmt.Sprintf("_ripple built `%s` with cgo off, so the C code in `%s` was not compiled. Set `cgo: true` in `.ripple.yaml` for this directory to verify it with a C compiler._", dir, found))
}
