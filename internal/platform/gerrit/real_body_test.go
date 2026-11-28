//go:build gerrit

package gerrit_test

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

// TestRealListOpenPRsReportsTheBody (ROADMAP 229): the body ripple posts is stored by a real Gerrit as a change
// message, and ListOpenPRs reads it back as PR.Body, so the engine can tell a description a person changed.
// The raw stored message is recorded as a FACT (the fake assumes "Patch Set 1:\n\n<body>").
func TestRealListOpenPRsReportsTheBody(t *testing.T) {
	f := &realGerrit{e: realConfig(t)}
	f.Host(t, gitws.Options{WorkDir: t.TempDir()}, time.Now)
	path := f.Path("body")
	f.AddRepo(t, path, map[string]string{"go.mod": "module x\n"})
	c := realClient(f)
	repo := model.Repo{FullName: path, DefaultBranch: "main"}

	if _, err := checkoutAndPush(t, c, path, "ripple/body-v1", "a.txt", "a\n"); err != nil {
		t.Fatal(err)
	}
	body := "Bumps example.com/lib from 1.0.0 to 1.1.0.\n\n- release notes: https://example.com/notes\n"
	if _, err := c.CreatePR(t.Context(), repo, platform.PRSpec{Head: "ripple/body-v1", Base: "main", Body: body}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range f.changes(path) {
		for _, m := range ch.Messages {
			t.Logf("FACT stored change message for the posted body: %q", m.Message)
		}
	}
	prs, err := c.ListOpenPRs(t.Context(), repo, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("open PRs = %+v, want one", prs)
	}
	t.Logf("FACT ListOpenPRs body: %q", prs[0].Body)
	if prs[0].Body == "" || model.BodyHash(prs[0].Body) != model.BodyHash(body) {
		t.Errorf("body read back %q, want the posted %q (modulo trailing whitespace)", prs[0].Body, body)
	}
}
