package gitws

// A scan-only workspace (platform.WithScanOnly) is a partial clone with no working
// tree: `--filter=blob:none --no-checkout`. It holds the commit and its trees, so
// Walk lists every path from `git ls-tree`, and a blob is fetched only when it is
// read. Prefetch loads the paths a scan will read in one request, through a
// sparse checkout of exactly those files, and ReadFile then reads them from disk.
//
// Anything the host cannot do (no partial clone, a refused fetch by object id)
// surfaces as platform.ErrScanFallback and the engine scans a full checkout.
// See docs/bounds.md for what this saves.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/taumatix/ripple/internal/platform"
)

var errScanOnly = errors.New("workspace is scan-only: it can be read, not changed")

type treeEntry struct{ mode, oid string }

// tree lists the regular files of HEAD, in the order a directory walk visits them.
func (w *workspace) tree(ctx context.Context) ([]string, map[string]treeEntry, error) {
	w.treeMu.Lock()
	defer w.treeMu.Unlock()
	if w.paths != nil {
		return w.paths, w.entries, nil
	}
	out, err := w.git(ctx, w.repoDir, "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", platform.ErrScanFallback, err)
	}
	entries := map[string]treeEntry{}
	for _, rec := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // mode type oid
		if len(f) != 3 || f[1] != "blob" {
			continue // a submodule is a commit, not a file
		}
		entries[p] = treeEntry{mode: f[0], oid: f[2]}
	}
	paths := make([]string, 0, len(entries))
	for p, e := range entries {
		if (e.mode == "100644" || e.mode == "100755") && !inSkippedDir(p) {
			paths = append(paths, p)
		}
	}
	slices.SortFunc(paths, comparePaths)
	w.paths, w.entries = paths, entries
	return paths, entries, nil
}

// inSkippedDir is Walk's rule: .git, vendor and node_modules directories are not entered.
func inSkippedDir(p string) bool {
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		switch d {
		case ".git", "vendor", "node_modules":
			return true
		}
	}
	return false
}

// comparePaths orders paths as fs.WalkDir visits them: component by component, not
// byte by byte (git sorts "a-b" before "a/c"; a walk visits "a/c" first).
func comparePaths(a, b string) int {
	return slices.Compare(strings.Split(a, "/"), strings.Split(b, "/"))
}

func (w *workspace) walkTree(fn func(string) error) error {
	paths, _, err := w.tree(context.Background())
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := fn(p); err != nil {
			return err
		}
	}
	return nil
}

// readBlob reads a path that is not on disk straight from the object database,
// fetching the blob from the remote if it is missing.
func (w *workspace) readBlob(p string) ([]byte, error) {
	ctx := context.Background()
	_, entries, err := w.tree(ctx)
	if err != nil {
		return nil, err
	}
	e, ok := entries[p]
	if !ok {
		return nil, platform.ErrNotExist
	}
	if e.mode != "100644" && e.mode != "100755" {
		// A symbolic link may point at a file inside the repo; only a real checkout resolves it.
		return nil, fmt.Errorf("%w: %s is not a regular file", platform.ErrScanFallback, p)
	}
	out, err := w.gitNetIO(ctx, w.repoDir, gitIO{maxOut: maxFileBytes}, "cat-file", "blob", e.oid)
	if errors.Is(err, errTooLarge) {
		return nil, fmt.Errorf("%s: larger than %d bytes", p, maxFileBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", platform.ErrScanFallback, err)
	}
	return []byte(out), nil
}

// Prefetch implements platform.Prefetcher: it fetches every path's blob in one
// request by checking out just those files. A full checkout has them already.
func (w *workspace) Prefetch(ctx context.Context, paths []string) error {
	if !w.light {
		return nil
	}
	_, entries, err := w.tree(ctx)
	if err != nil {
		return err
	}
	var pat bytes.Buffer
	for _, p := range paths {
		if _, ok := entries[p]; ok && sparseSafe(p) {
			pat.WriteString("/" + p + "\n")
		}
	}
	if pat.Len() == 0 {
		return nil
	}
	if _, err := w.gitNetIO(ctx, w.repoDir, gitIO{stdin: pat.String()}, "sparse-checkout", "set", "--no-cone", "--stdin"); err != nil {
		return fmt.Errorf("%w: %v", platform.ErrScanFallback, err)
	}
	if _, err := w.gitNet(ctx, w.repoDir, "checkout", "-q", "HEAD"); err != nil {
		return fmt.Errorf("%w: %v", platform.ErrScanFallback, err)
	}
	return nil
}

// sparseSafe reports whether a path can be written as a sparse-checkout pattern
// that matches only itself. Others are read one blob at a time.
func sparseSafe(p string) bool {
	return !strings.ContainsAny(p, "*?[]\\\n\r") && !strings.HasSuffix(p, " ")
}
