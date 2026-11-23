package engine

import (
	"context"
	"fmt"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
)

// ToolchainSource lists the Go releases the Go project supports, as semver
// ("v1.27.4"): the newest stable patch of each supported minor, in any order.
type ToolchainSource interface {
	Supported(ctx context.Context) ([]string, error)
}

// ToolchainWatcher turns the Go release list into jobs for the `go` directive and
// `toolchain` line of repos that opted in with go-toolchain. The list has no
// release dates and no cursor, so it is read whole on each poll and the job key
// (repo, package, target version) is what makes that idempotent.
type ToolchainWatcher struct {
	Engine *Engine
	Source ToolchainSource

	elect election // PollElected's claim
}

// Poll enqueues the jobs the current list calls for and returns how many were new.
// It does not touch the network while no repo has opted in.
func (w *ToolchainWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	var deps []model.Dependency
	for _, pkg := range []string{model.PackageGoDirective, model.PackageGoToolchain} {
		d, err := e.Store.ListDependents(ctx, model.DatasourceGoToolchain, pkg)
		if err != nil {
			return 0, fmt.Errorf("list dependents of go %s: %w", pkg, err)
		}
		deps = append(deps, d...)
	}
	if len(deps) == 0 {
		return 0, nil
	}
	versions, err := w.Source.Supported(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch go releases: %w", err)
	}
	latest, oldest := latestPerMinor(versions)
	n := 0
	repos := map[string]*repoState{}
	done := map[string]bool{}
	// A directive PR also lifts the toolchain line of the same go.mod to the same
	// release, so a separate toolchain PR for that file would only conflict with it.
	lifted := map[string]bool{}
	for _, d := range deps {
		if d.Package == model.PackageGoDirective && ToolchainTarget(d.Package, d.Version, latest, oldest) != "" {
			lifted[d.Repo+"|"+d.File] = true
		}
	}
	for _, d := range deps {
		target := ToolchainTarget(d.Package, d.Version, latest, oldest)
		if target == "" || (d.Package == model.PackageGoToolchain && lifted[d.Repo+"|"+d.File]) {
			continue
		}
		key := model.JobKey(d.Repo, model.DatasourceGoToolchain, d.Package, target)
		if done[key] {
			continue
		}
		typ, ok := applicable(d, model.DatasourceGoToolchain, d.Package, target)
		if !ok {
			continue
		}
		rs, err := e.repoState(ctx, repos, d.Repo)
		if err != nil {
			return n, err
		}
		if !rs.found {
			continue
		}
		// The list gives no release date, so the cooldown runs from the first poll
		// that saw the release; EnqueueJob keeps the first ReleasedAt on a replay.
		seen := e.now()
		dec, ok := e.decide(rs, d, typ, seen)
		if !ok || !dec.Enabled {
			continue
		}
		done[key] = true
		inserted, err := e.Store.EnqueueJob(ctx, model.Job{
			Key: key, Repo: d.Repo, Datasource: model.DatasourceGoToolchain, Package: d.Package, Version: target,
			ReleasedAt: seen, State: model.JobPending, NotBefore: dec.NotBefore,
		})
		if err != nil {
			return n, err
		}
		if inserted {
			n++
		}
	}
	return n, nil
}

// latestPerMinor maps each minor ("v1.27") to its newest listed patch, and names the
// oldest minor listed: the oldest release still supported.
func latestPerMinor(versions []string) (latest map[string]string, oldest string) {
	latest = map[string]string{}
	for _, v := range versions {
		if !semver.IsValid(v) {
			continue
		}
		mm := semver.MajorMinor(v)
		if cur, ok := latest[mm]; !ok || semver.Compare(v, cur) > 0 {
			latest[mm] = v
		}
		if oldest == "" || semver.Compare(mm, oldest) < 0 {
			oldest = mm
		}
	}
	return latest, oldest
}

// ToolchainTarget is the version a `toolchain` line or `go` directive at cur should
// move to, or "" for no update. latest and oldest come from latestPerMinor.
//
//   - A toolchain line follows the patch releases of its own minor, which carry the
//     security fixes; once its minor is out of support it moves to the oldest minor
//     that is still supported.
//   - A go directive never takes a patch (it states the least Go a dependant needs, so
//     only end of support is reason enough to raise it) and moves to the oldest
//     supported minor, the smallest step that leaves the unsupported releases.
//   - Neither ever moves backwards, and one newer than every supported release (an
//     unreleased or pre-release Go) is left alone.
func ToolchainTarget(pkg, cur string, latest map[string]string, oldest string) string {
	if !semver.IsValid(cur) || oldest == "" {
		return ""
	}
	mm := semver.MajorMinor(cur)
	var target string
	switch {
	case pkg == model.PackageGoToolchain && latest[mm] != "":
		target = latest[mm]
	case latest[mm] == "" && semver.Compare(mm, oldest) < 0 &&
		(pkg == model.PackageGoToolchain || pkg == model.PackageGoDirective):
		target = latest[oldest]
	}
	if target == "" || semver.Compare(target, cur) <= 0 {
		return ""
	}
	return target
}
