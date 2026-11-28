//go:build gitlab

package gitlab_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
)

// The job sleeps so the pipeline is still running when auto-merge is requested; a pipeline that had
// already finished would turn the request into an immediate merge or a refusal, a different path.
const (
	passingCI = "test:\n  script:\n    - sleep 20\n    - exit 0\n"
	failingCI = "test:\n  script:\n    - sleep 20\n    - exit 1\n"
)

// TestRealAutoMergeWithAPipeline drives merge-when-pipeline-succeeds with a real runner
// (scripts/ci-gitlab-runner.sh): a passing pipeline merges the merge request, a failing one leaves it open
// (GitLab adds no "aborted the automatic merge" note, which the first version of this test assumed).
func TestRealAutoMergeWithAPipeline(t *testing.T) {
	if os.Getenv("RIPPLE_TEST_GITLAB_RUNNER") == "" {
		t.Skip("RIPPLE_TEST_GITLAB_RUNNER is not set: no runner is registered")
	}
	f, repo := fixtureWithRepo(t, "pipeline")
	ctx := t.Context()
	pid := url.PathEscape(repo.FullName)
	f.api.must("PUT", "projects/"+pid, map[string]any{"only_allow_merge_if_pipeline_succeeds": true})

	open := func(branch, ci string) platform.PR {
		t.Helper()
		f.pushFiles(t, repo.FullName, branch, map[string]string{".gitlab-ci.yml": ci})
		// The pipeline exists a moment after the push; auto-merge asked before it does is a different case.
		for end := time.Now().Add(60 * time.Second); ; time.Sleep(time.Second) {
			var ps []struct{ ID int }
			f.api.mustJSON("GET", "projects/"+pid+"/pipelines?ref="+url.QueryEscape(branch), nil, &ps)
			if len(ps) > 0 {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("no pipeline for %s within 60s", branch)
			}
		}
		pr, err := f.client.CreatePR(ctx, repo, platform.PRSpec{Head: branch, Base: "main", Title: "Update " + branch})
		if err != nil {
			t.Fatal(err)
		}
		err = f.client.RequestAutoMerge(ctx, repo, pr)
		t.Logf("FACT auto_merge requested while the %s pipeline runs: error=%v", branch, err)
		if err != nil {
			t.Fatal(err)
		}
		return pr
	}
	state := func(pr platform.PR) (string, string) {
		var m struct {
			State  string `json:"state"`
			Status string `json:"detailed_merge_status"`
		}
		if err := json.Unmarshal([]byte(rawMR(t, f, repo.FullName, pr.Number)), &m); err != nil {
			t.Fatal(err)
		}
		return m.State, m.Status
	}

	pass := open("ripple/pass", passingCI)
	for end := time.Now().Add(5 * time.Minute); ; time.Sleep(3 * time.Second) {
		s, ds := state(pass)
		if s == "merged" {
			t.Logf("FACT a passing pipeline merged the MR (state %s)", s)
			break
		}
		if time.Now().After(end) {
			t.Fatalf("MR !%d not merged within 5 minutes: state %s, detailed_merge_status %s", pass.Number, s, ds)
		}
	}

	fail := open("ripple/fail", failingCI)
	// The first CI run showed no "aborted the automatic merge" note within 5 minutes of the request, so the
	// test no longer waits for one: it waits for the pipeline to fail, then records what the MR looks like.
	var status string
	for end := time.Now().Add(5 * time.Minute); ; time.Sleep(3 * time.Second) {
		var ps []struct {
			Status string `json:"status"`
		}
		f.api.mustJSON("GET", "projects/"+pid+"/pipelines?ref="+url.QueryEscape("ripple/fail"), nil, &ps)
		if len(ps) > 0 {
			status = ps[0].Status
		}
		if status == "failed" {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the ripple/fail pipeline is %q after 5 minutes, want failed", status)
		}
	}
	time.Sleep(20 * time.Second) // time for GitLab to react to the failed pipeline, if it reacts
	var ns []struct {
		Body   string `json:"body"`
		System bool   `json:"system"`
	}
	f.api.mustJSON("GET", fmt.Sprintf("projects/%s/merge_requests/%d/notes", pid, fail.Number), nil, &ns)
	var notes []string
	for _, n := range ns {
		if n.System {
			notes = append(notes, n.Body)
		}
	}
	s, ds := state(fail)
	t.Logf("FACT a failing pipeline: MR state %s, detailed_merge_status %s, system notes %q", s, ds, notes)
	t.Logf("FACT failing-pipeline MR as GitLab returns it: %s", rawMR(t, f, repo.FullName, fail.Number))
	if s != "opened" {
		t.Errorf("the MR with a failed pipeline is %s, want opened", s)
	}
	if ds != "ci_must_pass" {
		t.Errorf("detailed_merge_status = %q, want ci_must_pass", ds)
	}
	var armed struct {
		Armed bool `json:"merge_when_pipeline_succeeds"`
	}
	if err := json.Unmarshal([]byte(rawMR(t, f, repo.FullName, fail.Number)), &armed); err != nil {
		t.Fatal(err)
	}
	if !armed.Armed {
		t.Error("merge_when_pipeline_succeeds is false after the pipeline failed; docs say it stays armed")
	}
	for _, n := range notes {
		if strings.Contains(n, "aborted") {
			t.Errorf("system note %q: GitLab was recorded adding no abort note", n)
		}
	}
}
