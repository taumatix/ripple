package engine

import (
	"context"
	"reflect"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

func dep(ds, pkg string) model.Dependency {
	return model.Dependency{Repo: "acme/app", File: "f", Datasource: ds, Package: pkg}
}

// Two replicas scanning different repos each add a package. With one newline cursor the
// second write-back replaced the first; with a cursor per package both survive.
func TestPolledPackagesKeepEveryReplicasAdditions(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	a, b := &Engine{Store: st}, &Engine{Store: st}
	if err := a.notePackages(ctx, model.DatasourceDocker, dockerPackagesCursor, []model.Dependency{dep(model.DatasourceDocker, "ghcr.io/x/a")}); err != nil {
		t.Fatal(err)
	}
	if err := b.notePackages(ctx, model.DatasourceDocker, dockerPackagesCursor, []model.Dependency{dep(model.DatasourceDocker, "ghcr.io/x/b"), dep(model.DatasourceNpm, "left-pad")}); err != nil {
		t.Fatal(err)
	}
	if err := b.notePackages(ctx, model.DatasourceNpm, npmPackagesCursor, []model.Dependency{dep(model.DatasourceNpm, "left-pad")}); err != nil {
		t.Fatal(err)
	}
	got, err := a.polledPackages(ctx, dockerPackagesCursor)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ghcr.io/x/a", "ghcr.io/x/b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("docker set = %v, want %v", got, want)
	}
	if got, _ := a.polledPackages(ctx, npmPackagesCursor); !reflect.DeepEqual(got, []string{"left-pad"}) {
		t.Errorf("npm set = %v", got)
	}
}

// A set written by an earlier version (one newline cursor) keeps polling, and dropping a
// package removes it from both forms.
func TestPolledPackagesReadLegacyCursorAndDropFromBoth(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	e := &Engine{Store: st}
	if err := st.SetCursor(ctx, actionsPackagesCursor, "up/old\nup/gone"); err != nil {
		t.Fatal(err)
	}
	if err := e.noteActionPackages(ctx, []model.Dependency{dep(model.DatasourceGitHubTags, "up/new")}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.polledPackages(ctx, actionsPackagesCursor)
	if want := []string{"up/gone", "up/new", "up/old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("union = %v, want %v", got, want)
	}
	for _, p := range []string{"up/gone", "up/new"} {
		if err := e.dropActionPackage(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := e.polledPackages(ctx, actionsPackagesCursor); !reflect.DeepEqual(got, []string{"up/old"}) {
		t.Errorf("after drops = %v, want only up/old", got)
	}
}

// onlyStore hides the optional interfaces of the store it wraps, as a third-party Store does.
type onlyStore struct{ store.Store }

func TestPolledPackagesFallBackWithoutCursorLister(t *testing.T) {
	ctx := context.Background()
	e := &Engine{Store: onlyStore{store.NewMemory()}}
	if _, ok := e.Store.(store.CursorLister); ok {
		t.Fatal("wrapper must not expose CursorLister")
	}
	for _, p := range []string{"up/b", "up/a", "up/b"} {
		if err := e.noteActionPackages(ctx, []model.Dependency{dep(model.DatasourceGitHubTags, p)}); err != nil {
			t.Fatal(err)
		}
	}
	if raw, _ := e.Store.GetCursor(ctx, actionsPackagesCursor); raw != "up/a\nup/b" {
		t.Errorf("legacy cursor = %q", raw)
	}
	if got, _ := e.polledPackages(ctx, actionsPackagesCursor); !reflect.DeepEqual(got, []string{"up/a", "up/b"}) {
		t.Errorf("set = %v", got)
	}
}

// The first poll after an upgrade folds the legacy newline cursor into one key per package and
// deletes it, so every later poll reads only the keys.
func TestPolledPackagesFoldLegacyCursor(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	e := &Engine{Store: st}
	if err := st.SetCursor(ctx, npmPackagesCursor, "left-pad\nis-odd"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCursor(ctx, packageKey(npmPackagesCursor, "is-even"), "1"); err != nil {
		t.Fatal(err)
	}
	got, err := e.polledPackages(ctx, npmPackagesCursor)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"is-even", "is-odd", "left-pad"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("set = %v, want %v", got, want)
	}
	if raw, _ := st.GetCursor(ctx, npmPackagesCursor); raw != "" {
		t.Errorf("legacy cursor survived the fold: %q", raw)
	}
	for _, p := range []string{"left-pad", "is-odd", "is-even"} {
		if v, _ := st.GetCursor(ctx, packageKey(npmPackagesCursor, p)); v == "" {
			t.Errorf("no key for %s after the fold", p)
		}
	}
	again, _ := e.polledPackages(ctx, npmPackagesCursor)
	if !reflect.DeepEqual(again, got) {
		t.Errorf("second read = %v, want %v", again, got)
	}
}

// A store that cannot list cursors keeps the legacy list: there is nothing to fold into.
func TestPolledPackagesNoFoldWithoutCursorLister(t *testing.T) {
	ctx := context.Background()
	e := &Engine{Store: onlyStore{store.NewMemory()}}
	if err := e.Store.SetCursor(ctx, npmPackagesCursor, "left-pad"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.polledPackages(ctx, npmPackagesCursor); !reflect.DeepEqual(got, []string{"left-pad"}) {
		t.Fatalf("set = %v", got)
	}
	if raw, _ := e.Store.GetCursor(ctx, npmPackagesCursor); raw != "left-pad" {
		t.Errorf("legacy cursor = %q, want it kept", raw)
	}
}

// Every datasource drops a package nobody uses, and keeps one that someone still does.
func TestDropPackageEveryDatasource(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ ds, cursor string }{
		{model.DatasourceDocker, dockerPackagesCursor},
		{model.DatasourceNpm, npmPackagesCursor},
		{model.DatasourceCargo, cargoPackagesCursor},
		{model.DatasourcePip, pipPackagesCursor},
	} {
		t.Run(c.ds, func(t *testing.T) {
			st := store.NewMemory()
			e := &Engine{Store: st}
			if err := e.notePackages(ctx, c.ds, c.cursor, []model.Dependency{dep(c.ds, "used"), dep(c.ds, "unused")}); err != nil {
				t.Fatal(err)
			}
			if err := st.ReplaceDependencies(ctx, "acme/app", []model.Dependency{dep(c.ds, "used")}); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{"used", "unused"} {
				if err := e.dropPackage(ctx, c.ds, c.cursor, p); err != nil {
					t.Fatal(err)
				}
			}
			if got, _ := e.polledPackages(ctx, c.cursor); !reflect.DeepEqual(got, []string{"used"}) {
				t.Errorf("after drops = %v, want only used", got)
			}
		})
	}
}
