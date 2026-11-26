package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/terraform"
)

// terraformPackagesCursor holds the packages some repo uses (newline separated). The registry
// has no feed ripple reads, so TerraformWatcher polls only the packages a scanned .tf file names.
const terraformPackagesCursor = "terraform:packages"

// TerraformRegistry is the read side of the Terraform registry; terraform.Client implements it.
type TerraformRegistry interface {
	Versions(ctx context.Context, pkg string) ([]string, error)
}

// applicableTerraform judges a registry version for a pom.xml dependency: the same package, a
// version text ripple can move, and a release newer than it.
func applicableTerraform(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceTerraform || d.Package != pkg {
		return "", false
	}
	if !manager.ParseTerraformDeclared(d.Version) || !manager.ParseTerraformSpec(version) {
		return "", false
	}
	if strings.Count(d.Version, ".") == 1 {
		// `~> 1.2` is rewritten to the release's X.Y, so a release counts only when that differs
		// from the declaration (1.2.9 would leave the text as it is), and it is judged by it.
		version = strings.Join(strings.SplitN(version, ".", 3)[:2], ".")
		if semver.Compare("v"+version, "v"+d.Version) <= 0 {
			return "", false
		}
	}
	return rules.Classify("v"+d.Version, "v"+version)
}

// terraformTargets lists, newest first, the newest release of each update type that is an
// update of d.
func terraformTargets(releases []string, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range releases {
		typ, ok := applicableTerraform(d, pkg, v)
		if ok && (newest[typ] == "" || semver.Compare("v"+v, "v"+newest[typ]) > 0) {
			newest[typ] = v
		}
	}
	var out []cargoTarget
	for typ, v := range newest {
		out = append(out, cargoTarget{v, typ})
	}
	sort.Slice(out, func(i, j int) bool { return semver.Compare("v"+out[i].version, "v"+out[j].version) > 0 })
	return out
}

// TerraformWatcher turns the versions on the Terraform registry of the packages in use into jobs, like
// NpmWatcher does for packages: one read per package per poll through Engine.Terraform, one job
// per (repo, package, target version).
type TerraformWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a package, nor
// while Engine.Terraform is nil.
func (w *TerraformWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Terraform == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, terraformPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceTerraform, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceTerraform, terraformPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		vs, err := e.Terraform.Versions(ctx, pkg)
		if err != nil {
			if errors.Is(err, terraform.ErrDenied) || errors.Is(err, terraform.ErrNotFound) {
				e.log().Info("package not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("package not read", "package", pkg, "err", err)
			}
			continue
		}
		releases := terraform.Releases(vs)
		for _, d := range deps {
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range terraformTargets(releases, d, pkg) {
				if rs == nil {
					if rs, err = e.repoState(ctx, repos, d.Repo); err != nil {
						return n, err
					}
					if !rs.found {
						break
					}
				}
				// The cooldown runs from the first poll that saw the version, and EnqueueJob
				// keeps the first ReleasedAt (created_at is not read).
				if x, ok := e.decide(rs, d, c.typ, at); ok && x.Enabled {
					target, dec = c.version, x
					break
				}
			}
			if target == "" {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceTerraform, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceTerraform, Package: pkg, Version: target,
				ReleasedAt: at, State: model.JobPending, NotBefore: dec.NotBefore,
			})
			if err != nil {
				return n, err
			}
			if inserted {
				n++
			}
		}
	}
	return n, nil
}

// noteTerraformLock records the .terraform.lock.hcl beside an edited Terraform file, but only for
// a provider: a lock file pins providers, never modules.
func (r *runner) noteTerraformLock(d model.Dependency) {
	if !strings.HasPrefix(d.Package, "providers/") {
		return
	}
	p := path.Join(path.Dir(d.File), ".terraform.lock.hcl")
	if _, err := r.ws.ReadFile(p); err == nil {
		if r.terraformLocks == nil {
			r.terraformLocks = map[string]bool{}
		}
		r.terraformLocks[p] = true
	}
}

// terraformNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) terraformNotes() string {
	if len(r.terraformLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.terraformLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## .terraform.lock.hcl not regenerated\n\nripple edits the version constraint only: it never runs Terraform. This lock file still pins the old provider version and its checksums, so run `terraform init -upgrade` and push to the branch before merging:\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
