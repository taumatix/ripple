package store

import (
	"hash/fnv"
	"sort"
	"strconv"

	"github.com/taumatix/ripple/internal/model"
)

// Sharding spreads the two GSI partitions that concentrate traffic: the pending-job
// queue (every due-job poll and every enqueue) and a popular package's dependents.
//
// Shard 0 uses the key the table has always used (JOBS#pending, PKG#<ds>#<line>), so a
// table written before sharding needs no migration: its items are read as shard 0 and
// move to their proper shard the next time they are written (a job on its next
// UpdateJob, a repo's dependencies on its next scan).
const (
	// DefaultJobShards spreads the pending queue; a due-jobs poll costs this many Queries.
	DefaultJobShards = 8
	// DefaultDependentShards keeps one partition per package: ListDependents is the
	// hot read path and each shard multiplies its cost. Raise it only for a table where
	// one package is imported by so many repos that its partition throttles.
	DefaultDependentShards = 1
	// MaxShards bounds the fan-out of any one read.
	MaxShards = 64
)

func clampShards(n int) int { return min(max(n, 1), MaxShards) }

func shardOf(key string, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(n))
}

func pendingJobsPK(shard int) string {
	if shard == 0 {
		return "JOBS#pending"
	}
	return "JOBS#pending#" + strconv.Itoa(shard)
}

// dependentsPK is the GSI partition of a package's dependents; base is PKG#<ds>#<line>.
// Package names cannot contain the separator, so a suffixed key never collides.
func dependentsPK(base string, shard int) string {
	if shard == 0 {
		return base
	}
	return base + sep + strconv.Itoa(shard)
}

// mergeDueJobs merges per-shard results, each already in index order, into one list in
// the same order a single partition would give (NotBefore, then key), cut to limit.
// Each shard was asked for limit items, so the first limit of the union is complete.
func mergeDueJobs(parts [][]model.Job, limit int) []model.Job {
	var all []model.Job
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.SliceStable(all, func(i, k int) bool {
		a, b := ts(all[i].NotBefore)+sep+all[i].Key, ts(all[k].NotBefore)+sep+all[k].Key
		return a < b
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}
