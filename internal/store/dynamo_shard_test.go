package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
)

// A partition key is hot when one value takes a disproportionate share of the writes.
// Spread 20000 jobs and 20000 dependents of one package and fail if any shard carries
// more than 1.5x its fair share.
func TestShardsSpreadLoad(t *testing.T) {
	const n, total = 8, 20000
	jobs, deps := map[string]int{}, map[string]int{}
	for i := 0; i < total; i++ {
		key := model.JobKey(fmt.Sprintf("acme/repo%d", i), "go", "example.com/lib", "v1.2.3")
		jobs[pendingJobsPK(shardOf(key, n))]++
		repo := fmt.Sprintf("org%d/repo%d", i%97, i)
		deps[dependentsPK("PKG#go#example.com/lib", shardOf(repo, n))]++
	}
	for name, m := range map[string]map[string]int{"jobs": jobs, "dependents": deps} {
		if len(m) != n {
			t.Errorf("%s: %d partition keys, want %d", name, len(m), n)
		}
		for pk, c := range m {
			if float64(c) > 1.5*float64(total)/n {
				t.Errorf("%s: hot partition %q holds %d of %d", name, pk, c, total)
			}
		}
	}
}

// Existing tables keep working: shard 0 is the unsharded key, so items written before
// sharding are found by the first query of every read.
func TestShardZeroIsLegacyKey(t *testing.T) {
	if got := pendingJobsPK(0); got != "JOBS#pending" {
		t.Errorf("pendingJobsPK(0) = %q", got)
	}
	if got := dependentsPK("PKG#go#x.org/y", 0); got != "PKG#go#x.org/y" {
		t.Errorf("dependentsPK(.., 0) = %q", got)
	}
	if pendingJobsPK(3) == pendingJobsPK(0) || dependentsPK("PKG#go#a", 3) == dependentsPK("PKG#go#a", 0) {
		t.Error("shards must differ")
	}
}

func TestShardOfIsStableAndInRange(t *testing.T) {
	if shardOf("k", 1) != 0 {
		t.Error("one shard is always 0")
	}
	for i := 0; i < 1000; i++ {
		k := fmt.Sprint(i)
		if s := shardOf(k, 5); s < 0 || s >= 5 || s != shardOf(k, 5) {
			t.Fatalf("shardOf(%q)=%d", k, s)
		}
	}
}

func TestClampShards(t *testing.T) {
	for in, want := range map[int]int{-3: 1, 0: 1, 1: 1, 8: 8, 64: 64, 65: 64, 1000: 64} {
		if got := clampShards(in); got != want {
			t.Errorf("clampShards(%d)=%d want %d", in, got, want)
		}
	}
}

func TestMergeDueJobsOrdersAcrossShardsAndLimits(t *testing.T) {
	j := func(key string, sec int) model.Job {
		return model.Job{Key: key, NotBefore: time.Date(2026, 11, 22, 12, 0, sec, 0, time.UTC)}
	}
	a, b, c, d := j("a", 1), j("b", 2), j("c", 3), j("d", 3)
	got := mergeDueJobs([][]model.Job{{b, d}, {a}, {c}}, 3)
	if len(got) != 3 || got[0].Key != "a" || got[1].Key != "b" || got[2].Key != "c" {
		t.Fatalf("got %v", got)
	}
	if got := mergeDueJobs([][]model.Job{{d}, {c}}, 10); got[0].Key != "c" {
		t.Fatalf("ties break by key: %v", got)
	}
}

func TestConfigShardLimitsMatchStore(t *testing.T) {
	if config.DefaultDynamoJobShards != DefaultJobShards || config.DefaultDynamoDependentShards != DefaultDependentShards || config.MaxDynamoShards != MaxShards || config.DefaultDynamoQueryConcurrency != DefaultQueryConcurrency {
		t.Fatal("internal/config shard constants drifted from internal/store")
	}
}
