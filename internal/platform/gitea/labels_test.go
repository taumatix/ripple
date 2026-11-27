package gitea_test

import (
	"bytes"
	"context"
	"log/slog"
	"sort"
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
		g.LabelCreateForbidden = true
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
		if !strings.Contains(out, "labels not applied") || !strings.Contains(out, "dependencies") || strings.Contains(out, "label=security") {
			t.Errorf("want a warning naming only the missing label, got %q", out)
		}
	})
}

// A label the repository lacks is created (POST .../labels) and then applied, by name where the
// instance takes names and by id on Forgejo 7 (ROADMAP 281).
func TestCreatePRCreatesTheLabelsTheRepositoryLacks(t *testing.T) {
	for _, tc := range []struct {
		name             string
		forgejo, idsOnly bool
	}{{"gitea", false, false}, {"forgejo", true, false}, {"forgejo7 ids only", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			g := newLabelEnv(t, tc.forgejo)
			g.LabelIDsOnly = tc.idsOnly
			g.AddLabel(supRepo, "security")
			c := client(g)
			c.Log = slog.New(slog.NewTextHandler(&logs, nil))
			if _, err := c.CreatePR(context.Background(), model.Repo{FullName: supRepo}, platform.PRSpec{
				Head: supBranch, Base: "main", Title: "t", Labels: []string{"security", "dependencies"}}); err != nil {
				t.Fatal(err)
			}
			got := g.PRs(supRepo)[0].Labels
			sort.Strings(got)
			if strings.Join(got, ",") != "dependencies,security" {
				t.Errorf("applied labels %v, want both", got)
			}
			if logs.Len() != 0 {
				t.Errorf("unexpected log output %q", logs.String())
			}
		})
	}
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

// Forgejo 7.0.16 (Gitea 1.21 underneath) takes label ids only: POST .../issues/{n}/labels with names
// answers 422 "cannot unmarshal ... of type int64" (hosts-gitea.yml, TestRealLabelByName). The client
// then looks the ids up (GET .../labels) and posts those.
func TestCreatePRFallsBackToLabelIDsWhereNamesAreRefused(t *testing.T) {
	var logs bytes.Buffer
	g := newLabelEnv(t, true)
	g.LabelIDsOnly = true
	g.AddLabel(supRepo, "security")
	c := client(g)
	c.Log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := c.CreatePR(context.Background(), model.Repo{FullName: supRepo}, platform.PRSpec{
		Head: supBranch, Base: "main", Title: "t", Labels: []string{"security", "dependencies"}}); err != nil {
		t.Fatal(err)
	}
	got := g.PRs(supRepo)[0].Labels
	sort.Strings(got)
	if strings.Join(got, ",") != "dependencies,security" {
		t.Errorf("applied labels %v, want the existing one and the created one", got)
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
