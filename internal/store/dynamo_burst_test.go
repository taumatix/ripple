package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// A mass install (ROADMAP 113, 189): many repos register the same package at once. Every
// registration must succeed and the dependents list must be complete at once, whatever the
// index has caught up with. On DynamoDB Local this measures the burst with no throttling and
// no index lag, which is all Local can show; a real table's behaviour is ROADMAP 333.
func TestDynamoBurstOfRegistrationsOnOneHotPackage(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, f *dynamofake.Fake) {
		ctx := context.Background()
		// Adaptive shards with striped counters: the burst moves every counter the check compares.
		d := newDynamoTable(t, db, "ripple_test_"+suffix(), store.WithAdaptiveDependents(20), store.WithCounterStripes(4))
		const repos = 150
		var wg sync.WaitGroup
		errs := make(chan error, repos)
		start := time.Now()
		for i := 0; i < repos; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				repo := fmt.Sprintf("acme/r%03d", i)
				if err := d.ReplaceDependencies(ctx, repo, []model.Dependency{freshDep(repo, "v1.0.0")}); err != nil {
					errs <- fmt.Errorf("%s: %w", repo, err)
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		wrote := time.Since(start)
		got, err := d.ListDependents(ctx, "go", "example.com/lib")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != repos {
			t.Fatalf("%d dependents straight after the burst, want %d", len(got), repos)
		}
		drift, err := d.CheckDependentCounts(ctx)
		if err != nil || len(drift) != 0 {
			t.Fatalf("dependent counts drifted after the burst: %v, %v", drift, err)
		}
		t.Logf("%d concurrent registrations of one package in %v, all listed at once, no count drift", repos, wrote)
	})
}
