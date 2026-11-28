//go:build live

package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/livetest"
	"github.com/taumatix/ripple/internal/model"
)

// A real pseudo-version dependency against the real proxy (ROADMAP 216). github.com/pkg/errors
// is read only and its history is immutable: the pseudo-version below names commit e1ac100e4667
// (2018-02-17), which is NOT contained in the tag v0.8.1 although v0.8.1 was published a year
// later (2019-01-03); it is in v0.9.0. Containment was checked with `git merge-base
// --is-ancestor` on a clone on 2026-11-28; the test itself cannot check it, which is the point:
// the proxy knows when a release was published, not what it contains.

const (
	livePseudoPkg     = "github.com/pkg/errors"
	livePseudoVersion = "v0.8.1-0.20180217175701-e1ac100e4667"
)

func liveProxyInfo(t *testing.T, ref string) (version string, at time.Time) {
	t.Helper()
	c, _ := livetest.Client()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://proxy.golang.org/"+livePseudoPkg+"/@v/"+ref+".info", nil)
	req.Header.Set("User-Agent", livetest.UserAgent)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var info struct {
		Version string
		Time    time.Time
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(b, &info) != nil {
		t.Fatalf("%s: status %d body %q", ref, resp.StatusCode, b)
	}
	return info.Version, info.Time
}

func TestLivePseudoVersionMoveUsesTheProxysPublishTimes(t *testing.T) {
	// The fixture is a real pseudo-version: the proxy derives the same string from the commit.
	if v, _ := liveProxyInfo(t, "e1ac100e4667"); v != livePseudoVersion {
		t.Fatalf("proxy names commit e1ac100e4667 %s, fixture says %s", v, livePseudoVersion)
	}
	d := model.Dependency{Datasource: model.DatasourceGo, Package: livePseudoPkg, Version: livePseudoVersion}
	for _, tc := range []struct {
		release string
		want    bool
	}{
		{"v0.8.0", false}, // older than the commit's base: refused
		{"v0.8.1", true},  // published after the commit, but does not contain it: still accepted
		{"v0.9.0", true},  // published after the commit and contains it
	} {
		_, at := liveProxyInfo(t, tc.release)
		_, ok := applicablePseudoRelease(d, livePseudoPkg, tc.release, at)
		if ok != tc.want {
			t.Errorf("release %s (published %s): accepted=%v, want %v", tc.release, at.Format(time.RFC3339), ok, tc.want)
		}
	}
}
