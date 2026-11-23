package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// Entry 145: a server configured with RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL compares the
// dependents counters with their index on its own and logs the gap, reading the table a
// page at a time (RIPPLE_DYNAMODB_SCAN_PAGE and _PAUSE_MS) and writing nothing; once the
// recount has repaired the table it logs that the counters match.
func TestServerLogsCounterDrift(t *testing.T) {
	f := dynamofake.New()
	t.Cleanup(f.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "t")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "t")
	t.Setenv("AWS_REGION", "us-east-1")
	env := map[string]string{"RIPPLE_WEBHOOK_SECRET": "s", "RIPPLE_GITHUB_APP_ID": "1", "RIPPLE_GITHUB_KEY_FILE": "/k",
		"RIPPLE_STORE": "dynamodb", "RIPPLE_DYNAMODB_TABLE": "drift", "RIPPLE_DYNAMODB_ENDPOINT": f.URL(),
		"RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS": "10", "RIPPLE_DYNAMODB_SCAN_PAGE": "2", "RIPPLE_DYNAMODB_SCAN_PAUSE_MS": "1",
		"RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL": "1m"}
	s, err := config.LoadServer(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.DynamoDriftCheck != time.Minute || s.DynamoScanPage != 2 || s.DynamoScanPause != time.Millisecond {
		t.Fatalf("config = %v %d %v", s.DynamoDriftCheck, s.DynamoScanPage, s.DynamoScanPause)
	}
	// A table written before the option existed has no counters: a second store with it off writes that.
	legacyCfg := *s
	legacyCfg.DynamoAdaptiveDependents = 0
	legacy, err := app.OpenStore(context.Background(), &legacyCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { legacy.Close() })
	for _, r := range []string{"acme/a", "acme/b", "acme/c"} {
		if err := legacy.ReplaceDependencies(context.Background(), r, []model.Dependency{{Repo: r, File: "go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/x", Version: "v1"}}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := app.OpenStore(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := st.(*store.Dynamo)
	scans := f.Calls("Scan")
	e := newEnvOpts(t, st, defaultLimits(), opts{tweak: func(a *app.App) {
		a.DriftCheckEvery = 50 * time.Millisecond // the config's 1m, shortened for the test
	}})
	e.eventually("the drift logged", func() bool { return e.logLines("dependent counters drifted", "packages=1") > 0 })
	if pages := f.Calls("Scan") - scans; pages < 2 {
		t.Fatalf("the check read the table in %d Scan call(s), want it paged by 2 items", pages)
	}
	if _, err := d.RecountAllDependents(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.eventually("the match logged", func() bool { return e.logLines("dependent counters match the index") > 0 })
}
