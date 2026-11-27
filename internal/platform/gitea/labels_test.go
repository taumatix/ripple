package gitea_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/testutil"
)

// A label name the repository does not have is silently dropped by a real Gitea 1.27.3 and
// Forgejo 17.0.2: POST .../issues/{n}/labels answers 200 with `[]` (hosts-gitea.yml,
// TestRealLabelByName). The client used to assume it failed and log that; it now reads the
// answer and warns, naming the labels that were not applied.
func TestCreatePRWarnsAboutLabelsTheRepositoryLacks(t *testing.T) {
	eachFlavour(t, func(t *testing.T, forgejo bool) {
		var logs bytes.Buffer
		g := newLabelEnv(t, forgejo)
		c := client(g)
		c.Log = slog.New(slog.NewTextHandler(&logs, nil))
		g.AddLabel(supRepo, "security")
		_, err := c.CreatePR(context.Background(), model.Repo{FullName: supRepo}, platform.PRSpec{
			Head: supBranch, Base: "main", Title: "t", Labels: []string{"security", "dependencies"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := g.PRs(supRepo)[0].Labels; strings.Join(got, ",") != "security" {
			t.Errorf("applied labels %v, want only the one the repository has", got)
		}
		out := logs.String()
		if !strings.Contains(out, "labels not applied") || !strings.Contains(out, "dependencies") || strings.Contains(out, "security") {
			t.Errorf("want one warning naming only the missing label, got %q", out)
		}
	})
}

func TestCreatePRQuietWhenEveryLabelIsApplied(t *testing.T) {
	var logs bytes.Buffer
	g := newLabelEnv(t, false)
	g.AddLabel(supRepo, "dependencies")
	c := client(g)
	c.Log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := c.CreatePR(context.Background(), model.Repo{FullName: supRepo}, platform.PRSpec{
		Head: supBranch, Base: "main", Title: "t", Labels: []string{"dependencies"}}); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Errorf("unexpected log output %q", logs.String())
	}
}

func newLabelEnv(t *testing.T, forgejo bool) *testutil.Gitea {
	t.Helper()
	g := testutil.NewGitea(t, forgejo)
	g.AddRepo(supRepo, map[string]string{"go.mod": "module x\n"})
	g.PushBranch(supRepo, supBranch, gitws.AuthorEmail(gitws.Options{}))
	return g
}
