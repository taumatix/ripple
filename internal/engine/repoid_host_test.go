package engine

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// A repository id is unique within one host only: the same number on a second GitHub
// must not find, or be mistaken for, the first host's repo.
func TestRepoIDOwnerIsPerHost(t *testing.T) {
	ctx := context.Background()
	e := &Engine{Store: store.NewMemory()}
	for _, r := range []model.Repo{{FullName: "acme/app", RepoID: 42}, {FullName: "ghes:corp/svc", RepoID: 42}} {
		if err := e.Store.UpsertRepo(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := e.Store.SetCursor(ctx, repoIDKey(r.FullName, r.RepoID), r.FullName); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{"acme/renamed": "acme/app", "ghes:corp/renamed": "ghes:corp/svc"} {
		if got, ok := e.RepoIDOwner(ctx, name, 42); !ok || got != want {
			t.Errorf("owner of 42 seen as %s: %q, %v; want %q", name, got, ok, want)
		}
	}
	if k := repoIDKey("acme/app", 42); k != "repoid|42" {
		t.Errorf("the default host's key changed: %q", k)
	}
}
