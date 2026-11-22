package engine

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/store"
)

// A repo with many rows for the package is parsed once per release, whether or
// not its file covers them.
func TestFanOutParsesRepoConfigOncePerRepo(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	// The file only covers the regex manager, so no gomod row ever enables a job
	// and the early "done" exit never applies.
	if err := st.UpsertRepo(ctx, model.Repo{FullName: "acme/app", Config: []byte("updates:\n  - ecosystem: regex\n")}); err != nil {
		t.Fatal(err)
	}
	var deps []model.Dependency
	for _, f := range []string{"a/go.mod", "b/go.mod", "c/go.mod", "d/go.mod"} {
		deps = append(deps, model.Dependency{Repo: "acme/app", Manager: "gomod", File: f, Datasource: "go", Package: "example.com/lib", Version: "v1.0.0"})
	}
	if err := st.ReplaceDependencies(ctx, "acme/app", deps); err != nil {
		t.Fatal(err)
	}
	parses := 0
	e := &Engine{Store: st, Limits: rules.Limits{OpenPRLimit: 5}, Now: func() time.Time { return time.Unix(0, 0) },
		parseConfig: func(b []byte) (*config.Repo, error) { parses++; return RepoConfig(b) }}
	if _, err := e.FanOut(ctx, model.Release{Datasource: "go", Package: "example.com/lib", Version: "v1.1.0", Time: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	if parses != 1 {
		t.Fatalf("config parsed %d times for 4 dependency rows, want 1", parses)
	}
}
