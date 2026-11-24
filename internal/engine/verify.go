package engine

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

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
	wsOnlyDirs := map[string]bool{}
	for _, dir := range dirs {
		tidy := func() (string, bool, error) { return r.tidy(dir, run) }
		failure, ok, err := tidy()
		wsOnly := false
		if err == nil && !ok {
			// A module that reaches a sibling only through go.work cannot be tidied on its
			// own: retry with the siblings as local replaces, removed again afterwards.
			if f, ok2, err2, retried := r.tidyWithSiblings(dir, tidy); retried {
				failure, ok, err, wsOnly = f, ok2, err2, ok2
				wsOnlyDirs[dir] = ok2
			}
		}
		if err != nil || !ok {
			return failure, err
		}
		steps := [][]string{{"mod", "vendor"}, {"build", "./..."}, {"vet", "./..."}}
		if !r.hasVendor(dir) {
			steps = steps[1:]
		}
		if wsOnly {
			// go would judge this module on its own go.mod, where its sibling cannot be
			// found; the workspace step below builds and vets it with the workspace on.
			if r.hasVendor(dir) {
				r.notes = append(r.notes, fmt.Sprintf("_ripple did not refresh `%s/vendor`: the module needs the workspace to resolve its sibling modules, and `go mod vendor` ignores it._", dir))
			}
			steps = nil
		}
		for _, args := range steps {
			failure, ok, err := run(dir, false, []string{dir}, args...)
			if err == nil && !ok && args[0] != "mod" && r.hasSiblingRequire(dir) {
				// The module requires a workspace sibling without a replace, at a version
				// the proxy has but that is behind the workspace's copy: judged on its own
				// go.mod it cannot build. The workspace step below judges it with go.work on.
				r.notes = append(r.notes, fmt.Sprintf("_ripple built `%s` in workspace mode only: on its own it does not build against the published version of a sibling module it requires without a replace._", dir))
				break
			}
			if err != nil || !ok {
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
		syncable := true
		for _, u := range works[w] {
			syncable = syncable && !wsOnlyDirs[u]
		}
		if !syncable {
			// go work sync reads the go.mod of every requirement, the workspace's own
			// modules included, and fails on one only go.work can resolve.
			r.notes = append(r.notes, fmt.Sprintf("_ripple did not run `go work sync` in `%s`: a module there requires another workspace module without a replace, which it cannot resolve. Each module was tidied with the workspace's modules as temporary replaces, then built and vetted in workspace mode._", w))
		} else if failure, ok, err := run(w, true, works[w], "work", "sync"); err != nil || !ok {
			return failure, err
		}
		if r.hasVendor(w) {
			// A workspace's own vendor/ (go work vendor) is stale once any member changed.
			if failure, ok, err := run(w, true, works[w], "work", "vendor"); err != nil || !ok {
				return failure, err
			}
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
	v.GoBinary = r.namedGo(members)
	v.Image = r.namedGoImage(members)
	netStep := args[0] == "mod" || args[0] == "work"
	if netStep && r.e.GoProxyToken != "" {
		v.Username, v.Token, v.Credentialed = r.e.GoProxyUser, r.e.GoProxyToken, true
	}
	return v
}

// namedGo finds, in the server's toolchain directory, the Go release the go.mod files of
// members name (the toolchain line, else the go line), so a bump is checked by the Go it
// asks for. With several members the newest release wins, as it satisfies them all. It
// returns "" when no directory is set or none of them has an installed release, and
// then verification runs with the server's Go.
func (r *runner) namedGo(members []string) string {
	if r.e.GoToolchainDir == "" {
		return ""
	}
	var best, bestRel string
	for _, m := range members {
		data, err := r.ws.ReadFile(path.Join(m, "go.mod"))
		if err != nil {
			continue
		}
		f, err := modfile.ParseLax("go.mod", data, nil)
		if err != nil || f.Go == nil {
			continue
		}
		tc := ""
		if f.Toolchain != nil {
			tc = f.Toolchain.Name
		}
		bin, rel := platform.FindGo(r.e.GoToolchainDir, f.Go.Version, tc)
		if bin == "" && r.e.GoInstaller != nil {
			var err error
			if bin, rel, err = r.e.GoInstaller.Ensure(r.ctx, f.Go.Version, tc); err != nil {
				r.e.log().Warn("go toolchain download failed; verification uses the server's Go", "go", f.Go.Version, "toolchain", tc, "err", err)
				bin, rel = "", ""
			}
		}
		if bin != "" && (best == "" || semver.Compare("v"+rel, "v"+bestRel) > 0) {
			best, bestRel = bin, rel
		}
	}
	if best != "" {
		r.namedRelease = bestRel
	}
	return best
}

// namedGoImage is the configured container image for the newest Go minor the go.mod files
// of members name (the toolchain line, else the go line); "" when none is configured,
// and the sandbox's default image runs.
func (r *runner) namedGoImage(members []string) string {
	if len(r.e.GoImages) == 0 {
		return ""
	}
	best, bestMinor := "", ""
	for _, m := range members {
		data, err := r.ws.ReadFile(path.Join(m, "go.mod"))
		if err != nil {
			continue
		}
		f, err := modfile.ParseLax("go.mod", data, nil)
		if err != nil || f.Go == nil {
			continue
		}
		line := f.Go.Version
		if f.Toolchain != nil && f.Toolchain.Name != "default" {
			line = f.Toolchain.Name
		}
		minor := platform.GoMinor(line)
		img := r.e.GoImages[minor]
		if img != "" && (best == "" || semver.Compare("v"+minor, "v"+bestMinor) > 0) {
			best, bestMinor = img, minor
		}
	}
	r.namedImage = best
	return best
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

// relDir is the slash path from directory from to directory to, both relative to the
// repository root, written the way a go.mod replace needs it ("./x" or "../x").
func relDir(from, to string) string {
	f, t := splitDir(from), splitDir(to)
	i := 0
	for i < len(f) && i < len(t) && f[i] == t[i] {
		i++
	}
	parts := make([]string, 0, len(f)-i+len(t)-i)
	for range f[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, t[i:]...)
	if len(parts) == 0 {
		return "."
	}
	if parts[0] != ".." {
		return "./" + strings.Join(parts, "/")
	}
	return strings.Join(parts, "/")
}

func splitDir(d string) []string {
	if d == "." || d == "" {
		return nil
	}
	return strings.Split(d, "/")
}

// hasSiblingRequire reports whether the module in dir requires, without replacing
// it, another module that a go.work in the repository uses.
func (r *runner) hasSiblingRequire(dir string) bool {
	works, err := r.goWorks()
	if err != nil {
		return false
	}
	file := path.Join(dir, "go.mod")
	data, err := r.ws.ReadFile(file)
	if err != nil {
		return false
	}
	f, err := modfile.Parse(file, data, nil)
	if err != nil {
		return false
	}
	replaced := map[string]bool{}
	for _, rep := range f.Replace {
		replaced[rep.Old.Path] = true
	}
	for _, units := range works {
		member := false
		for _, u := range units {
			member = member || u == dir
		}
		if !member {
			continue
		}
		for _, u := range units {
			if u == dir {
				continue
			}
			sd, err := r.ws.ReadFile(path.Join(u, "go.mod"))
			if err != nil {
				continue
			}
			sib, err := modfile.ParseLax("go.mod", sd, nil)
			if err != nil || sib.Module == nil {
				continue
			}
			for _, req := range f.Require {
				if req.Mod.Path == sib.Module.Mod.Path && !replaced[req.Mod.Path] {
					return true
				}
			}
		}
	}
	return false
}

// tidyWithSiblings is the second try at go mod tidy for a module in dir that failed
// on its own. Modules that go.work uses and that dir requires without a replace
// (they are found through the workspace, so no proxy has them) are given a temporary
// local replace, tidy runs again, and the replace is taken out of go.mod again, so
// the file keeps its requirement exactly as the workspace expects it. retried is
// false when dir has no such sibling, which leaves the first failure to stand.
func (r *runner) tidyWithSiblings(dir string, tidy func() (string, bool, error)) (failure string, ok bool, err error, retried bool) {
	works, werr := r.goWorks()
	if werr != nil {
		return "", false, nil, false
	}
	file := path.Join(dir, "go.mod")
	orig, rerr := r.ws.ReadFile(file)
	if rerr != nil {
		return "", false, nil, false
	}
	f, perr := modfile.Parse(file, orig, nil)
	if perr != nil {
		return "", false, nil, false
	}
	required := map[string]bool{}
	for _, req := range f.Require {
		required[req.Mod.Path] = true
	}
	replaced := map[string]bool{}
	for _, rep := range f.Replace {
		replaced[rep.Old.Path] = true
	}
	temp := map[string]bool{}
	var roots []string
	for w := range works {
		roots = append(roots, w)
	}
	sort.Strings(roots)
	for _, w := range roots {
		uses := false
		for _, u := range works[w] {
			uses = uses || u == dir
		}
		if !uses {
			continue
		}
		for _, u := range works[w] {
			if u == dir {
				continue
			}
			data, err := r.ws.ReadFile(path.Join(u, "go.mod"))
			if err != nil {
				continue
			}
			sib, err := modfile.ParseLax("go.mod", data, nil)
			if err != nil || sib.Module == nil {
				continue
			}
			p := sib.Module.Mod.Path
			if required[p] && !replaced[p] && !temp[p] {
				if err := f.AddReplace(p, "", relDir(dir, u), ""); err != nil {
					return "", false, nil, false
				}
				temp[p] = true
			}
		}
	}
	if len(temp) == 0 {
		return "", false, nil, false
	}
	withReplaces, ferr := f.Format()
	if ferr != nil {
		return "", false, nil, false
	}
	if err := r.ws.WriteFile(file, withReplaces); err != nil {
		return "", false, err, true
	}
	failure, ok, err = tidy()
	if err == nil && ok {
		tidied, rerr := r.ws.ReadFile(file)
		if rerr != nil {
			return "", false, rerr, true
		}
		tf, perr := modfile.Parse(file, tidied, nil)
		if perr != nil {
			return "", false, perr, true
		}
		for p := range temp {
			if err := tf.DropReplace(p, ""); err != nil {
				return "", false, err, true
			}
		}
		tf.Cleanup()
		out, ferr := tf.Format()
		if ferr != nil {
			return "", false, ferr, true
		}
		if err := r.ws.WriteFile(file, out); err != nil {
			return "", false, err, true
		}
		return "", true, nil, true
	}
	_ = r.ws.WriteFile(file, orig)
	if err == nil {
		failure += "\n(retried with the workspace's other modules as temporary local replaces)"
	}
	return failure, false, err, true
}

// tidy runs go mod tidy in dir. With "tidy: minimal" for the directory the result is
// cut back to what the update made necessary (see minimalTidy).
func (r *runner) tidy(dir string, run func(dir string, workspace bool, members []string, args ...string) (string, bool, error)) (string, bool, error) {
	minimal := false
	if r.cfg != nil {
		if u, ok := r.cfg.UpdateFor("gomod", path.Join(dir, "go.mod")); ok && u != nil {
			minimal = u.Tidy == "minimal"
		}
	}
	if !minimal {
		return run(dir, false, []string{dir}, "mod", "tidy")
	}
	modFile, sumFile := path.Join(dir, "go.mod"), path.Join(dir, "go.sum")
	origMod, err := r.ws.ReadFile(modFile)
	if err != nil {
		return "", false, err
	}
	origSum, _ := r.ws.ReadFile(sumFile)
	failure, ok, err := run(dir, false, []string{dir}, "mod", "tidy")
	if err != nil || !ok {
		return failure, ok, err
	}
	tidyMod, err := r.ws.ReadFile(modFile)
	if err != nil {
		return "", false, err
	}
	tidySum, _ := r.ws.ReadFile(sumFile)
	mod, sum, err := minimalTidy(modFile, origMod, origSum, tidyMod, tidySum)
	if err != nil {
		// Unable to merge: keep what go mod tidy wrote, which is always correct.
		return "", true, nil
	}
	if err := r.ws.WriteFile(modFile, mod); err != nil {
		return "", false, err
	}
	if sum == nil {
		return "", true, nil
	}
	return "", true, r.ws.WriteFile(sumFile, sum)
}

// minimalTidy merges the go.mod and go.sum before go mod tidy ran with what it wrote.
// go.mod keeps every original line and gains what tidy added or raised (a requirement
// the update made necessary, or a version it had to select), plus tidy's go and
// toolchain lines. go.sum keeps every original line except those of a module version
// below the one the merged go.mod selects, and gains tidy's. A requirement tidy would have
// dropped stays, which is the point. sum is nil when neither file has a go.sum.
func minimalTidy(name string, origMod, origSum, tidyMod, tidySum []byte) (mod, sum []byte, err error) {
	orig, err := modfile.Parse(name, origMod, nil)
	if err != nil {
		return nil, nil, err
	}
	tidied, err := modfile.Parse(name, tidyMod, nil)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]*modfile.Require{}
	for _, req := range orig.Require {
		have[req.Mod.Path] = req
	}
	for _, req := range tidied.Require {
		cur, ok := have[req.Mod.Path]
		switch {
		case !ok:
			orig.AddNewRequire(req.Mod.Path, req.Mod.Version, req.Indirect)
		case cur.Mod.Version != req.Mod.Version:
			orig.AddRequire(req.Mod.Path, req.Mod.Version)
		}
	}
	if tidied.Go != nil {
		if err := orig.AddGoStmt(tidied.Go.Version); err != nil {
			return nil, nil, err
		}
	}
	if tidied.Toolchain != nil {
		if err := orig.AddToolchainStmt(tidied.Toolchain.Name); err != nil {
			return nil, nil, err
		}
	}
	orig.SortBlocks()
	orig.Cleanup()
	mod, err = orig.Format()
	if err != nil {
		return nil, nil, err
	}
	if len(origSum) == 0 && len(tidySum) == 0 {
		return mod, nil, nil
	}
	seen := map[string]bool{}
	var lines []string
	selected := map[string]string{}
	for _, req := range orig.Require {
		selected[req.Mod.Path] = req.Mod.Version
	}
	add := func(data []byte, dropStale bool) {
		for _, l := range strings.Split(string(data), "\n") {
			f := strings.Fields(l)
			if len(f) != 3 || seen[l] {
				continue
			}
			if dropStale {
				// a version below the one go.mod selects, which tidy no longer needs:
				// what a bump leaves behind, and what the update is meant to replace
				if v, ok := selected[f[0]]; ok && semver.Compare(strings.TrimSuffix(f[1], "/go.mod"), v) < 0 {
					continue
				}
			}
			seen[l] = true
			lines = append(lines, l)
		}
	}
	add(tidySum, false)
	add(origSum, true)
	sort.Slice(lines, func(i, j int) bool {
		a, b := strings.Fields(lines[i]), strings.Fields(lines[j])
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		return semverLess(strings.TrimSuffix(a[1], "/go.mod"), strings.TrimSuffix(b[1], "/go.mod"), a[1], b[1])
	})
	return mod, []byte(strings.Join(lines, "\n") + "\n"), nil
}

// semverLess orders go.sum lines of one module as go does: by version, the h1 line
// before the /go.mod line of the same version.
func semverLess(va, vb, fa, fb string) bool {
	if c := semver.Compare(va, vb); c != 0 {
		return c < 0
	}
	return !strings.HasSuffix(fa, "/go.mod") && strings.HasSuffix(fb, "/go.mod")
}
