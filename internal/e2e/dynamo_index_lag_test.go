package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// Entry 120: a repo that registered a dependency while the DynamoDB index still lags gets the
// release that lands before the index catches up. The repo's own backfill ran before the
// release existed, so only the fan-out can serve it, and it reads the index.
func TestReleaseReachesARepoTheDynamoIndexHasNotCaughtUp(t *testing.T) {
	ctx := context.Background()
	f := dynamofake.New()
	t.Cleanup(f.Close)
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
	if err != nil {
		t.Fatal(err)
	}
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(f.URL()) })
	d := store.NewDynamo(db, "lag", store.WithLeaseSkew(100*time.Millisecond))
	if err := d.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, d, defaultLimits())
	stubModule(e, "example.com/lib", "v1.0.0")
	e.gh.AddRepo(backfillApp, map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
	f.HoldIndex() // from here the index shows nothing new
	start(t, e, backfillApp)
	stubModule(e, "example.com/lib", "v1.1.0") // released only now: the scan's backfill could not know it
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick() // the poll fans out
	// The feed cursor moves past that release while the index still lags: the inclusive
	// boundary re-read must not be what saves it.
	e.clock.Advance(time.Minute)
	e.publish("example.com/nobody-imports-this", "v1.0.0")
	e.tick()
	f.ReleaseIndex() // the job queue is an index too: let it show the job
	e.tick()
	prs := e.gh.PRs(backfillApp)
	if len(prs) != 1 || !strings.Contains(prs[0].Title, "example.com/lib") || !strings.HasSuffix(prs[0].Title, " v1.1.0") {
		t.Fatalf("want one PR moving example.com/lib to v1.1.0 despite the lagging index, got %+v", prs)
	}
}
