package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/registry"
	"github.com/taumatix/ripple/internal/rules"
)

// dockerPackagesCursor holds the container images some repo uses ("registry/repo",
// newline separated). A registry has no feed of changes, so DockerWatcher polls only
// the images a scanned Dockerfile names, never a registry as a whole.
const dockerPackagesCursor = "docker:packages"

// TagRegistry is the read side of a container registry: the tags of an image and the
// manifest digest of one tag. registry.Client implements it.
type TagRegistry interface {
	Tags(ctx context.Context, pkg string) ([]string, error)
	Digest(ctx context.Context, pkg, tag string) (string, error)
}

// CreatedRegistry is the optional part of a TagRegistry that reads when an image was
// built (its config's "created"); registry.Client implements it. Without it, or when
// the image records no time or the read fails, a tag's release time is the first poll
// that saw it.
type CreatedRegistry interface {
	Created(ctx context.Context, pkg, tag string) (time.Time, error)
}

// applicableImage judges a registry tag for a docker dependency: the same image, the
// same tag shape (see registry.ParseTag), and newer.
func applicableImage(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceDocker || d.Package != pkg {
		return "", false
	}
	curSV, curShape, ok1 := registry.ParseTag(d.Version)
	sv, shape, ok2 := registry.ParseTag(version)
	if !ok1 || !ok2 || curShape != shape {
		return "", false
	}
	return rules.Classify(curSV, sv)
}

// DockerWatcher turns the tags of the container images in use into jobs, like
// ActionsWatcher does for actions: it reads each image's tags once per poll through
// Engine.Registry and enqueues one job per (repo, image, target tag).
type DockerWatcher struct {
	Engine *Engine

	elect election // PollElected's claim

	createdMu sync.Mutex
	created   map[string]time.Time // "pkg:tag" -> image creation time, kept once read
}

// releasedAt is when tag of pkg was released: the image config's creation time when
// the registry can say, else the moment of this poll (the cooldown then runs from first
// sight, and EnqueueJob keeps the first ReleasedAt on a replay).
func (w *DockerWatcher) releasedAt(ctx context.Context, pkg, tag string) time.Time {
	cr, ok := w.Engine.Registry.(CreatedRegistry)
	if !ok {
		return w.Engine.now()
	}
	key := pkg + ":" + tag
	w.createdMu.Lock()
	t, hit := w.created[key]
	w.createdMu.Unlock()
	if hit {
		return t
	}
	t, err := cr.Created(ctx, pkg, tag)
	if err != nil || t.IsZero() || t.After(w.Engine.now()) {
		if err != nil {
			w.Engine.log().Warn("image creation time not read, using first sight", "package", pkg, "tag", tag, "err", err)
		}
		return w.Engine.now()
	}
	w.createdMu.Lock()
	if w.created == nil {
		w.created = map[string]time.Time{}
	}
	w.created[key] = t
	w.createdMu.Unlock()
	return t
}

// Poll returns how many jobs were new. It makes no request while no repo uses an
// image, nor while Engine.Registry is nil.
func (w *DockerWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Registry == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, dockerPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceDocker, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceDocker, dockerPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		tags, err := e.Registry.Tags(ctx, pkg)
		if err != nil {
			if errors.Is(err, registry.ErrDenied) || errors.Is(err, registry.ErrNotFound) {
				e.log().Info("image not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("image tags not read", "package", pkg, "err", err)
			}
			continue
		}
		for _, d := range deps {
			target := registry.NewestOfShape(d.Version, tags)
			if target == "" {
				continue
			}
			typ, ok := applicable(d, model.DatasourceDocker, pkg, target)
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
			// A registry publishes no date with a tag: the image config's creation time
			// stands in for it, else the cooldown runs from the first poll that saw it.
			at := w.releasedAt(ctx, pkg, target)
			dec, ok := e.decide(rs, d, typ, at)
			if !ok || !dec.Enabled {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceDocker, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceDocker, Package: pkg, Version: target,
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

// imageVersion is the version string Apply takes for a docker job: the tag, plus the
// manifest digest when it can be read. A failed read is not an error here: only a line
// pinned to a digest needs one, and the manager refuses to rewrite such a line without
// it, so a digest is never guessed or left stale.
func (r *runner) imageVersion(pkg, tag string) string {
	key := pkg + ":" + tag
	if d, ok := r.digests[key]; ok {
		return tag + "@" + d
	}
	if r.e.Registry == nil {
		return tag
	}
	d, err := r.e.Registry.Digest(r.ctx, pkg, tag)
	if err != nil || d == "" {
		r.e.log().Warn("image digest not read", "repo", r.repo.FullName, "package", pkg, "tag", tag, "err", err)
		return tag
	}
	if r.digests == nil {
		r.digests = map[string]string{}
	}
	r.digests[key] = d
	return tag + "@" + d
}
