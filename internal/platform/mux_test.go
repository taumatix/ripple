package platform_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// fakePlatform records the repo names it was called with.
type fakePlatform struct {
	seen  []string
	until time.Time
}

func (f *fakePlatform) Checkout(_ context.Context, r model.Repo) (platform.Workspace, error) {
	f.seen = append(f.seen, r.FullName)
	return nil, nil
}

func (f *fakePlatform) ListOpenPRs(_ context.Context, r model.Repo, _ string) ([]platform.PR, error) {
	f.seen = append(f.seen, r.FullName)
	return nil, nil
}

func (f *fakePlatform) CreatePR(_ context.Context, r model.Repo, _ platform.PRSpec) (platform.PR, error) {
	f.seen = append(f.seen, r.FullName)
	return platform.PR{}, nil
}

// autoFake adds the optional capabilities.
type autoFake struct{ fakePlatform }

func (a *autoFake) RequestAutoMerge(_ context.Context, r model.Repo, _ platform.PR) error {
	a.seen = append(a.seen, "automerge "+r.FullName)
	return nil
}
func (a *autoFake) RateLimitedUntil(model.Repo) time.Time { return a.until }
func (a *autoFake) ReadsLegacyConfig(model.Repo) bool     { return true }

func newMux(t *testing.T) (*platform.Mux, *autoFake, *fakePlatform) {
	t.Helper()
	gh, gl := &autoFake{}, &fakePlatform{}
	m, err := platform.NewMux("github", &platform.Host{Name: "github", Platform: gh}, &platform.Host{Name: "gitlab", Platform: gl})
	if err != nil {
		t.Fatal(err)
	}
	return m, gh, gl
}

func TestMuxRoutesByPrefixAndStripsIt(t *testing.T) {
	m, gh, gl := newMux(t)
	ctx := context.Background()
	_, _ = m.Checkout(ctx, model.Repo{FullName: "acme/app"})
	_, _ = m.Checkout(ctx, model.Repo{FullName: "gitlab:group/sub/project"})
	if len(gh.seen) != 1 || gh.seen[0] != "acme/app" {
		t.Errorf("default host saw %v", gh.seen)
	}
	if len(gl.seen) != 1 || gl.seen[0] != "group/sub/project" {
		t.Errorf("gitlab saw %v, want the name without its prefix", gl.seen)
	}
	if _, err := m.Checkout(ctx, model.Repo{FullName: "bitbucket:a/b"}); err == nil {
		t.Error("unknown host prefix accepted")
	}
}

func TestMuxRepoName(t *testing.T) {
	m, _, _ := newMux(t)
	if got := m.RepoName("github", "acme/app"); got != "acme/app" {
		t.Errorf("default host name = %q", got)
	}
	if got := m.RepoName("gitlab", "group/project"); got != "gitlab:group/project" {
		t.Errorf("gitlab name = %q", got)
	}
}

func TestMuxOptionalCapabilitiesFollowTheHost(t *testing.T) {
	m, gh, _ := newMux(t)
	ctx := context.Background()
	if err := platform.RequestAutoMerge(ctx, m, model.Repo{FullName: "gitlab:g/p"}, platform.PR{}); !errors.Is(err, platform.ErrNoAutoMerge) {
		t.Errorf("auto-merge on a host without it: %v, want ErrNoAutoMerge", err)
	}
	if err := platform.RequestAutoMerge(ctx, m, model.Repo{FullName: "acme/app"}, platform.PR{}); err != nil {
		t.Errorf("auto-merge on the default host: %v", err)
	}
	gh.until = time.Unix(100, 0)
	if got := platform.RateLimitedUntil(m, model.Repo{FullName: "acme/app"}); !got.Equal(gh.until) {
		t.Errorf("rate limit signal of the default host = %v", got)
	}
	if got := platform.RateLimitedUntil(m, model.Repo{FullName: "gitlab:g/p"}); !got.IsZero() {
		t.Errorf("one host's rate limit leaked to another: %v", got)
	}
	if !platform.ReadsLegacyConfig(m, model.Repo{FullName: "acme/app"}) || platform.ReadsLegacyConfig(m, model.Repo{FullName: "gitlab:g/p"}) {
		t.Error("legacy alias must follow the host")
	}
}

func TestNewMuxRefusesBadHosts(t *testing.T) {
	p := &fakePlatform{}
	for _, hosts := range [][]*platform.Host{
		{{Name: "Git:Lab", Platform: p}},
		{{Name: "gitlab", Platform: p}, {Name: "gitlab", Platform: p}},
		{{Name: "gitlab"}},
	} {
		if _, err := platform.NewMux("", hosts...); err == nil {
			t.Errorf("NewMux accepted %+v", hosts)
		}
	}
	if _, err := platform.NewMux("github", &platform.Host{Name: "gitlab", Platform: p}); err == nil {
		t.Error("default host that is not registered accepted")
	}
}
