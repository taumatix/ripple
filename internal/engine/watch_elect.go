package engine

import (
	"context"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// feedLease names the claim that makes one replica poll a feed.
func feedLease(feedName string) string { return "feed:" + feedName }

// election is a replica's standing claim on the feed's poll.
type election struct {
	mu    sync.Mutex
	lease store.Lease
	held  bool
}

// PollElected is Poll for replicas that share a store: only the replica that holds the feed's
// claim polls, the others skip and report polled=false. The claim lasts ttl from the last
// poll, so give it a few poll intervals: the holder extends it on each poll, and when it dies
// another replica takes over once the claim lapses. Two replicas polling would be safe (job
// enqueue is insert-if-absent) but each release would be fetched twice.
func (w *Watcher) PollElected(ctx context.Context, ttl time.Duration) (jobs int, polled bool, err error) {
	return w.elect.poll(ctx, w.Engine, feedLease(w.Feed.Name()), ttl, w.Poll)
}

// Elected reports whether this replica held the feed's claim at its last PollElected.
func (w *Watcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up so another replica can poll at once; call it when stopping.
func (w *Watcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// poll takes or extends the claim named lease and, when this replica holds it, runs fn.
func (el *election) poll(ctx context.Context, e *Engine, lease string, ttl time.Duration, fn func(context.Context) (int, error)) (int, bool, error) {
	l, ok, err := e.Store.AcquireLease(ctx, lease, e.replicaID(), ttl)
	el.mu.Lock()
	switch {
	case err != nil || !ok:
		el.held = false
	default:
		if l.Token > 1 && l.Token != el.lease.Token {
			// The same owner re-acquiring a live claim keeps its token; a new token above 1 is a
			// takeover from a replica that died or stalled.
			store.NoteTakeover(e.log(), l, e.replicaID())
		}
		el.lease, el.held = l, true
	}
	el.mu.Unlock()
	if err != nil || !ok {
		return 0, false, err
	}
	n, err := fn(ctx)
	return n, true, err
}

func (el *election) isHeld() bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.held
}

func (el *election) stepDown(ctx context.Context, e *Engine) {
	el.mu.Lock()
	l, held := el.lease, el.held
	el.held = false
	el.mu.Unlock()
	if held {
		_ = e.Store.ReleaseLease(ctx, l)
	}
}

// Source poll names: the lease of each non-feed poller is "feed:<name>", like the Go module feed's.
const (
	toolchainPollName = "go-toolchain"
	actionsPollName   = "github-actions"
	dockerPollName    = "docker"
	npmPollName       = "npm"
	cargoPollName     = "cargo"
	pipPollName       = "pip"
	rubygemsPollName  = "rubygems"
	packagistPollName = "packagist"
	nugetPollName     = "nuget"
	mavenPollName     = "maven"
	terraformPollName = "terraform"
	helmPollName      = "helm"
)

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *ToolchainWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(toolchainPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *ToolchainWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *ToolchainWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *ActionsWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(actionsPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *ActionsWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *ActionsWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *DockerWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(dockerPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *DockerWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *DockerWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *NpmWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(npmPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *NpmWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *NpmWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *CargoWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(cargoPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *CargoWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *CargoWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *PipWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(pipPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *PipWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *PipWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// osvPollName names the OSV advisory poll's lease ("feed:osv"), like the other sources'.
const osvPollName = "osv"

// PollElected is Poll for replicas sharing a store: only the replica that holds the OSV
// poll's claim polls, the others skip and report polled=false; see Watcher.PollElected.
func (w *AdvisoryWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(osvPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *AdvisoryWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *AdvisoryWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *HelmWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(helmPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *HelmWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *HelmWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *RubygemsWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(rubygemsPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *RubygemsWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *RubygemsWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *PackagistWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(packagistPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *PackagistWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *PackagistWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *NugetWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(nugetPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *NugetWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *NugetWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *MavenWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(mavenPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *MavenWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *MavenWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }

// PollElected is Poll for replicas sharing a store; see Watcher.PollElected.
func (w *TerraformWatcher) PollElected(ctx context.Context, ttl time.Duration) (int, bool, error) {
	return w.elect.poll(ctx, w.Engine, feedLease(terraformPollName), ttl, w.Poll)
}

// Elected reports whether this replica held the claim at its last PollElected.
func (w *TerraformWatcher) Elected() bool { return w.elect.isHeld() }

// StepDown gives the claim up; call it when stopping.
func (w *TerraformWatcher) StepDown(ctx context.Context) { w.elect.stepDown(ctx, w.Engine) }
