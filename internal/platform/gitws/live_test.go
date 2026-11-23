//go:build live

package gitws_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

// RIPPLE_LIVE_SCAN_REPO=owner/name (a public GitHub repository) measures what a
// scan costs with and without the partial clone, over the public internet. It
// reports the size of the workspace directory, which is what the clone and the
// checkout wrote (the packed objects git downloaded plus the working tree).
//
//	RIPPLE_LIVE_SCAN_REPO=taumatix/ripple go test -tags live ./internal/platform/gitws -run LiveScan -v
func TestLiveScanCost(t *testing.T) {
	repo := os.Getenv("RIPPLE_LIVE_SCAN_REPO")
	if repo == "" {
		t.Skip("set RIPPLE_LIVE_SCAN_REPO=owner/name to measure against github.com")
	}
	ms, err := manager.Build(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, light := range []bool{false, true} {
		ctx := context.Background()
		if light {
			ctx = platform.WithScanOnly(ctx)
		}
		work := t.TempDir()
		start := time.Now()
		ws, err := gitws.Checkout(ctx, gitws.Options{GitBase: "https://github.com", WorkDir: work}, "https://github.com/"+repo+".git", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		var hits []string
		files := 0
		_ = ws.Walk(func(p string) error {
			files++
			for _, m := range ms {
				if m.Matches(p) {
					hits = append(hits, p)
					break
				}
			}
			return nil
		})
		if pf, ok := ws.(platform.Prefetcher); ok {
			if err := pf.Prefetch(ctx, hits); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range hits {
			if _, err := ws.ReadFile(p); err != nil {
				t.Fatal(err)
			}
		}
		took := time.Since(start)
		var total, pack int64
		_ = filepath.WalkDir(work, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if fi, err := d.Info(); err == nil {
					total += fi.Size()
					if filepath.Ext(p) == ".pack" {
						pack += fi.Size()
					}
				}
			}
			return nil
		})
		ws.Close()
		t.Logf("scan-only=%-5v files=%d matched=%d  pack=%d KiB  workspace=%d KiB  time=%s", light, files, len(hits), pack>>10, total>>10, took.Round(10*time.Millisecond))
	}
}
