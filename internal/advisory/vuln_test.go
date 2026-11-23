package advisory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// samples are recorded from api.osv.dev on 2026-11-23; see docs/vendor/osv/README.md.
func sample(t *testing.T, name string) *Vuln {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "vendor", "osv", "samples", name))
	if err != nil {
		t.Fatal(err)
	}
	var v Vuln
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestFixForRecordedAdvisory(t *testing.T) {
	v := sample(t, "GO-2026-6894.json")
	// The recorded fix is a pseudo-version; the OSV value has no "v" prefix and ripple adds it.
	fixed, ok := v.FixFor("github.com/anacrolix/torrent", "v1.50.0")
	if !ok || fixed != "v1.61.1-0.20260911233437-76452a2c8a2f" {
		t.Fatalf("got %q %v", fixed, ok)
	}
	if _, ok := v.FixFor("github.com/anacrolix/torrent", "v1.61.1-0.20260911233437-76452a2c8a2f"); ok {
		t.Fatal("the fixed version itself is not affected")
	}
	if _, ok := v.FixFor("github.com/other/module", "v1.0.0"); ok {
		t.Fatal("another module is not affected")
	}
}

func TestFixForPicksTheFixOfTheRangeContainingTheVersion(t *testing.T) {
	v := sample(t, "GO-2024-3228.json") // [2.3.1,2.14.4) [2.15.0,2.15.3) [2.16.0,2.16.1)
	mod := "github.com/coder/coder/v2"
	for _, c := range []struct {
		cur, want string
		ok        bool
	}{
		{"v2.3.1", "v2.14.4", true},
		{"v2.14.3", "v2.14.4", true},
		{"v2.14.4", "", false},
		{"v2.15.1", "v2.15.3", true},
		{"v2.16.0", "v2.16.1", true},
		{"v2.16.1", "", false},
		{"v2.3.0", "", false},
	} {
		got, ok := v.FixFor(mod, c.cur)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q %v, want %q %v", c.cur, got, ok, c.want, c.ok)
		}
	}
}

func TestUnfixedAdvisoryHasNoFix(t *testing.T) {
	v := sample(t, "GO-2026-4740.withdrawn.json")
	if _, ok := v.FixFor("github.com/shamaton/msgpack/v2", "v2.0.0"); ok {
		t.Fatal("no fixed event, so nothing to propose")
	}
	if !v.Withdrawn() {
		t.Fatal("withdrawn not detected")
	}
}

func TestStdlibIsNotAModule(t *testing.T) {
	v := sample(t, "GO-2026-6608.json")
	if _, ok := v.FixFor("stdlib", "v1.26.0"); ok {
		t.Fatal("stdlib must never be proposed as a go.mod bump")
	}
	if got := v.Modules(); len(got) != 0 {
		t.Fatalf("stdlib/toolchain listed as modules: %v", got)
	}
	got := sample(t, "GO-2024-3228.json").Modules()
	if len(got) != 2 || got[0] != "github.com/coder/coder" || got[1] != "github.com/coder/coder/v2" {
		t.Fatalf("got %v", got)
	}
}

func TestSeverityAndLinks(t *testing.T) {
	v := sample(t, "GHSA-2wrx-84qj-4pcg.trimmed.json")
	if v.SeverityLabel() != "MODERATE" {
		t.Fatalf("label %q", v.SeverityLabel())
	}
	if v.CVSS() != "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:N/I:N/A:H" {
		t.Fatalf("cvss %q", v.CVSS())
	}
	if sample(t, "GO-2026-6894.json").SeverityLabel() != "" {
		t.Fatal("Go records carry no severity")
	}
	if got := v.URL(); got != "https://osv.dev/vulnerability/GHSA-2wrx-84qj-4pcg" {
		t.Fatalf("url %q", got)
	}
}

func TestEvaluationAlgorithmEdges(t *testing.T) {
	mk := func(events string) *Vuln {
		var v Vuln
		raw := `{"id":"X-1","affected":[{"package":{"name":"m.example/x","ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":` + events + `}]}]}`
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return &v
	}
	// introduced "0" sorts before everything
	if f, ok := mk(`[{"introduced":"0"},{"fixed":"1.2.3"}]`).FixFor("m.example/x", "v0.0.1"); !ok || f != "v1.2.3" {
		t.Fatalf("introduced 0: %q %v", f, ok)
	}
	// events out of order are sorted, as the evaluation pseudocode says
	if f, ok := mk(`[{"fixed":"1.2.3"},{"introduced":"1.0.0"}]`).FixFor("m.example/x", "v1.1.0"); !ok || f != "v1.2.3" {
		t.Fatalf("unsorted: %q %v", f, ok)
	}
	// last_affected is a ceiling, not a fix: affected at v1.1.0, nothing to propose
	if f, ok := mk(`[{"introduced":"1.0.0"},{"last_affected":"1.1.5"}]`).FixFor("m.example/x", "v1.1.0"); ok {
		t.Fatalf("last_affected offered a fix: %q", f)
	}
	// a limit below the version means the range does not apply
	if _, ok := mk(`[{"introduced":"0"},{"fixed":"2.0.0"},{"limit":"1.5.0"}]`).FixFor("m.example/x", "v1.6.0"); ok {
		t.Fatal("version beyond limit must be unaffected")
	}
	if _, ok := mk(`[{"introduced":"0"},{"fixed":"2.0.0"}]`).FixFor("m.example/x", "not-a-version"); ok {
		t.Fatal("invalid current version")
	}
}
