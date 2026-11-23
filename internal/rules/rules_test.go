package rules

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		cur, next string
		want      UpdateType
		ok        bool
	}{
		{"v1.0.0", "v1.0.1", Patch, true},
		{"v1.0.0", "v1.1.0", Minor, true},
		{"v1.9.0", "v2.0.0", Major, true},
		{"v0.1.0", "v0.2.0", Minor, true},
		{"v1.0.0", "v1.0.0", "", false},
		{"v1.1.0", "v1.0.9", "", false},
		{"v1.0.0", "v1.1.0-rc.1", "", false},
		{"v1.1.0-rc.1", "v1.1.0-rc.2", Patch, true},
		{"v1.0.0", "v1.0.1-0.20261119235051-eae4af9b156a", "", false},
		{"v0.0.0-20261119235051-eae4af9b156a", "v1.0.0", "", false},
		{"v1.0.0", "garbage", "", false},
		{"v1.0.0+incompatible", "v1.1.0+incompatible", Minor, true},
	}
	for _, c := range cases {
		got, ok := Classify(c.cur, c.next)
		if got != c.want || ok != c.ok {
			t.Errorf("Classify(%s,%s) = %q,%v want %q,%v", c.cur, c.next, got, ok, c.want, c.ok)
		}
	}
}

// 2026-11-22 is a Sunday.
var sun = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func TestSchedule(t *testing.T) {
	s := &config.Schedule{Days: []string{"mon"}, From: "22:00", To: "23:00", Timezone: "Europe/Paris"}
	// Sunday 12:00 UTC -> next window is Monday 22:00 Paris (CET, UTC+1) = 21:00 UTC.
	got := NextInWindow(s, sun)
	if want := time.Date(2026, 11, 23, 21, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if !InWindow(s, got) || InWindow(s, got.Add(-time.Minute)) || InWindow(s, got.Add(time.Hour)) {
		t.Fatal("window edges wrong")
	}
	wrap := &config.Schedule{From: "22:00", To: "05:00"}
	for h, want := range map[int]bool{21: false, 22: true, 23: true, 0: true, 4: true, 5: false, 12: false} {
		if InWindow(wrap, time.Date(2026, 11, 23, h, 0, 0, 0, time.UTC)) != want {
			t.Errorf("hour %d != %v", h, want)
		}
	}
	if !InWindow(nil, sun) {
		t.Fatal("nil schedule is always open")
	}
}

var lim = Limits{MinimumReleaseAge: 72 * time.Hour, OpenPRLimit: 5, AllowAutomerge: true}

func TestEvaluateSecuritySkipsOnlyTheCooldown(t *testing.T) {
	cd := &config.Duration{Duration: 5 * 24 * time.Hour}
	u := &config.Update{Cooldown: cd}
	l := Limits{MinimumReleaseAge: 72 * time.Hour}
	if d := EvaluateSecurity(l, u, "example.com/lib", Patch, sun); !d.Enabled || !d.NotBefore.Equal(sun) {
		t.Fatalf("cooldown not skipped: %+v", d)
	}
	if l.MinimumReleaseAge != 72*time.Hour || u.Cooldown != cd {
		t.Fatal("inputs were modified")
	}
	// Ignore rules still win.
	u = &config.Update{Ignore: []config.Ignore{{Dependency: "example.com/*"}}}
	if d := EvaluateSecurity(l, u, "example.com/lib", Patch, sun); d.Enabled {
		t.Fatal("ignored dependency enabled")
	}
	// So does the schedule: sun is a Sunday, the window opens Monday.
	u = &config.Update{Schedule: &config.Schedule{Days: []string{"mon"}}}
	if d := EvaluateSecurity(l, u, "example.com/lib", Patch, sun); !d.NotBefore.After(sun) {
		t.Fatalf("schedule ignored: %+v", d)
	}
}
