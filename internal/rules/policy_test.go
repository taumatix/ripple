package rules

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
)

func upd(t *testing.T, y string) *config.Update {
	c, err := config.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return &c.Updates[0]
}

func TestCooldownOnlyTightens(t *testing.T) {
	rel := sun.Add(-24 * time.Hour)
	d := Evaluate(lim, upd(t, ""), "example.com/lib", Minor, rel, sun)
	if want := rel.Add(72 * time.Hour); !d.NotBefore.Equal(want) {
		t.Fatalf("server default: NotBefore=%v want %v", d.NotBefore, want)
	}
	d = Evaluate(lim, upd(t, "updates:\n  - ecosystem: gomod\n    cooldown: 0\n"), "example.com/lib", Minor, rel, sun)
	if want := rel.Add(72 * time.Hour); !d.NotBefore.Equal(want) {
		t.Fatalf("a repo must not shorten the server minimum: %v", d.NotBefore)
	}
	d = Evaluate(lim, upd(t, "updates:\n  - ecosystem: gomod\n    cooldown: 10d\n"), "example.com/lib", Minor, rel, sun)
	if want := rel.Add(240 * time.Hour); !d.NotBefore.Equal(want) {
		t.Fatalf("longer cooldown ignored: %v", d.NotBefore)
	}
	d = Evaluate(lim, upd(t, ""), "example.com/lib", Minor, sun.Add(-100*time.Hour), sun)
	if !d.NotBefore.Equal(sun) {
		t.Fatalf("old release should be due now, got %v", d.NotBefore)
	}
}

func TestPRLimitOnlyTightens(t *testing.T) {
	for in, want := range map[int]int{2: 2, 50: 5, 0: 5} {
		if got := EffectivePRLimit(lim, &config.Repo{OpenPRLimit: in}); got != want {
			t.Errorf("limit %d -> %d want %d", in, got, want)
		}
	}
}

func TestIgnoreGroupAutomerge(t *testing.T) {
	u := upd(t, `updates:
  - ecosystem: gomod
    ignore:
      - dependency: example.com/skip
      - dependency: example.com/nomajor
        update-types: [major]
    groups:
      ex:
        patterns: ["example.com/*"]
    automerge: minor
`)
	l := lim
	l.MinimumReleaseAge = 0
	if d := Evaluate(l, u, "example.com/skip", Patch, sun, sun); d.Enabled {
		t.Fatal("ignored dependency enabled")
	}
	if d := Evaluate(l, u, "example.com/nomajor", Major, sun, sun); d.Enabled {
		t.Fatal("ignored major enabled")
	}
	if d := Evaluate(l, u, "example.com/nomajor", Minor, sun, sun); !d.Enabled {
		t.Fatal("minor should pass")
	}
	if d := Evaluate(l, u, "example.com/lib", Minor, sun, sun); !d.Automerge || d.Group != "ex" {
		t.Fatalf("%+v", d)
	}
	if d := Evaluate(l, u, "example.com/lib", Major, sun, sun); d.Automerge {
		t.Fatal("minor automerge must not cover major")
	}
	if d := Evaluate(l, u, "other.org/x", Patch, sun, sun); d.Group != "" {
		t.Fatalf("%+v", d)
	}
	l.AllowAutomerge = false
	if d := Evaluate(l, u, "example.com/lib", Patch, sun, sun); d.Automerge {
		t.Fatal("server forbids automerge")
	}
}
