package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

var dashNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func TestRenderDashboardIsDeterministicAndOrderIndependent(t *testing.T) {
	a := model.Job{Key: "r|go|a|v1", Package: "a", Version: "v1", State: model.JobPending, NotBefore: dashNow.Add(time.Hour)}
	b := model.Job{Key: "r|go|b|v2", Package: "b", Version: "v2", State: model.JobFailed, Attempts: 5, Reason: "boom"}
	x, y := renderDashboard([]model.Job{a, b}, dashNow), renderDashboard([]model.Job{b, a}, dashNow)
	if x != y {
		t.Fatalf("order changed the body:\n%s\n---\n%s", x, y)
	}
	for _, want := range []string{DashboardMarker + "\n", "## Deferred (1)", "`a` `v1` until 2026-11-23 13:00 UTC", "## Failed (1)", "after 5 attempts: `boom`"} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %q in\n%s", want, x)
		}
	}
	if strings.Contains(x, "2026-11-23 12:00") {
		t.Error("the body must not carry the current time")
	}
}

func TestRenderDashboardNeutralisesUntrustedFields(t *testing.T) {
	j := model.Job{Key: "k", Package: "evil`\n@everyone <script>|x", Version: "v1", State: model.JobFailed, Reason: "line1\nline2 `` [click](http://x) " + strings.Repeat("z", 1000)}
	out := renderDashboard([]model.Job{j}, dashNow)
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "evil") && (strings.Contains(l, "<") || strings.Count(l, "`")%2 != 0) {
			t.Errorf("unsafe line: %q", l)
		}
	}
	if strings.Contains(out, "line1\nline2") || strings.Contains(out, strings.Repeat("z", 300)) {
		t.Error("reason must be one line and bounded")
	}
}

func TestRenderDashboardStaysUnderGitHubsLimitWithACountedNotice(t *testing.T) {
	var jobs []model.Job
	for i := 0; i < 3000; i++ {
		jobs = append(jobs, model.Job{Key: fmt.Sprintf("k%05d", i), Package: fmt.Sprintf("example.com/module%d", i), Version: "v1.2.3",
			State: model.JobFailed, Attempts: 5, Reason: strings.Repeat("r", 300)})
	}
	out := renderDashboard(jobs, dashNow)
	if len(out) > 65536 {
		t.Fatalf("body is %d bytes", len(out))
	}
	shown := strings.Count(out, "\n- ")
	want := fmt.Sprintf("... and %d more not shown", 3000-shown)
	if shown == 0 || !strings.Contains(out, want) {
		t.Fatalf("shown=%d, want notice %q at the end:\n%s", shown, want, out[len(out)-200:])
	}
}

func TestRenderDashboardEmpty(t *testing.T) {
	if out := renderDashboard(nil, dashNow); !strings.Contains(out, "Nothing is pending") {
		t.Fatal(out)
	}
}
