package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// A release whose Requires-Python excludes the lowest Python the repo declares is
// skipped with a reason (like a Cargo MSRV), never proposed; a repo that declares no
// Python, or a newer one, is not held back.
func TestPipRequiresPythonSkipsReleaseThePythonCannotRun(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		idx.Publish("requests", "2.31.0")
		e.gh.AddRepo("acme/old", map[string]string{".ripple.yaml": pipConfig,
			"pyproject.toml": "[project]\nname = \"a\"\nrequires-python = \">=3.9\"\ndependencies = [\"requests==2.31.0\"]\n"})
		e.gh.AddRepo("acme/new", map[string]string{".ripple.yaml": pipConfig,
			"pyproject.toml": "[project]\nname = \"a\"\nrequires-python = \">=3.11\"\ndependencies = [\"requests==2.31.0\"]\n"})
		e.gh.AddRepo("acme/none", map[string]string{".ripple.yaml": pipConfig, "requirements.txt": "requests==2.31.0\n"})
		start(t, e, "acme/old", "acme/new", "acme/none")
		e.clock.Advance(time.Minute)
		idx.PublishRequiresPython("requests", "2.33.0", ">=3.10")
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/old")); n != 0 {
			t.Fatalf("python 3.9 cannot run 2.33.0: %d PRs", n)
		}
		j, ok, err := e.st.GetJob(context.Background(), model.JobKey("acme/old", model.DatasourcePip, "requests", "2.33.0"))
		if err != nil || !ok || j.State != model.JobSkipped || !strings.Contains(j.Reason, "Requires-Python >=3.10") || !strings.Contains(j.Reason, "pyproject.toml") {
			t.Errorf("job = %+v, %v, %v", j, ok, err)
		}
		for _, repo := range []string{"acme/new", "acme/none"} {
			head, _ := onePR(t, e, repo, false)
			file := map[string]string{"acme/new": "pyproject.toml", "acme/none": "requirements.txt"}[repo]
			got, _ := e.gh.FileAt(repo, head, file)
			if !strings.Contains(got, "requests==2.33.0") {
				t.Errorf("%s: not moved:\n%s", repo, got)
			}
		}
	})
}
