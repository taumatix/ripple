package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// replicaStatus is GET /replica: the answering replica's name, whether it is the feed
// poller (and which other sources it polls), and how many leases this process has taken over from a holder that died or stalled.
// The service has no metrics endpoint; this is the one thing an operator can scrape. The
// count of replicas comes from their heartbeats (see beat).
func (a *App) replicaStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := struct {
		Replica    string `json:"replica"`
		FeedPoller bool   `json:"feed_poller"`
		// Pollers names the elected sources this replica polls now: feed, go-toolchain,
		// github-actions, docker, npm, cargo, pip (only those configured).
		Pollers         []string `json:"pollers"`
		LeaseTakeovers  int64    `json:"lease_takeovers"`
		LeaseTTLSeconds int      `json:"lease_ttl_seconds"`
		// EventsDeadLettered counts events this process gave up on since it started; EventsDead is
		// how many dead events the store holds now (every replica's, until requeued or purged),
		// left out when listing them fails.
		EventsDeadLettered int64 `json:"events_dead_lettered"`
		EventsDead         *int  `json:"events_dead,omitempty"`
		// The next three come from the replicas' heartbeats and are absent when the store has
		// no replica table. The takeover total adds up the live replicas only: a replica that
		// died takes its count with it.
		ReplicasLive   *int   `json:"replicas_live,omitempty"`
		FeedPollers    *int   `json:"feed_pollers,omitempty"`
		TakeoversTotal *int64 `json:"lease_takeovers_all_replicas,omitempty"`
		// SourcesUnpolled names the sources some live replica has configured and none polls
		// now (empty is healthy), so an alert can fire on a source nobody polls; it needs
		// every replica to run a version that reports its sources.
		SourcesUnpolled []string `json:"sources_unpolled,omitempty"`
	}{LeaseTakeovers: store.LeaseTakeovers()}
	if a.Engine != nil {
		out.Replica, out.LeaseTTLSeconds = a.Engine.ReplicaID(), int(a.Engine.LeaseDuration().Seconds())
	}
	if a.Watcher != nil {
		out.FeedPoller = a.Watcher.Elected()
	}
	if a.Queue != nil {
		out.EventsDeadLettered = a.Queue.DeadLettered()
	}
	if a.Engine != nil && a.Engine.Store != nil {
		if dead, err := a.Engine.Store.ListDeadEvents(r.Context()); err == nil {
			n := len(dead)
			out.EventsDead = &n
		} else {
			a.Log.Warn("list dead events", "err", err)
		}
	}
	out.Pollers = a.electedNames()
	if out.Pollers == nil {
		out.Pollers = []string{}
	}
	if t, ok := a.replicaTable(); ok {
		if live, err := t.LiveReplicas(r.Context()); err == nil {
			n, pollers, total := len(live), 0, int64(0)
			for _, x := range live {
				total += x.Takeovers
				if x.FeedPoller {
					pollers++
				}
			}
			out.ReplicasLive, out.FeedPollers, out.TakeoversTotal = &n, &pollers, &total
			out.SourcesUnpolled = unpolledSources(live)
		} else {
			a.Log.Warn("list live replicas", "err", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (a *App) replicaTable() (store.ReplicaTable, bool) {
	if a.Engine == nil {
		return nil, false
	}
	t, ok := a.Engine.Store.(store.ReplicaTable)
	return t, ok
}

// beat tells the store this replica is alive for a lease ttl. A failure is logged and
// nothing else: the count of replicas is for operators, and no work waits on it.
func (a *App) beat(ctx context.Context) {
	t, ok := a.replicaTable()
	if !ok {
		return
	}
	info := store.ReplicaInfo{ID: a.Engine.ReplicaID(), Takeovers: store.LeaseTakeovers()}
	// FeedPoller is true for a replica that polls any source, so feed_pollers counts the
	// replicas doing the polling.
	info.Polled = a.electedNames()
	info.FeedPoller = len(info.Polled) > 0
	info.Configured = a.sourceNames()
	if err := t.Heartbeat(ctx, info, a.Engine.LeaseDuration()); err != nil && ctx.Err() == nil {
		a.Log.Warn("replica heartbeat", "err", err)
	}
}

// retire removes this replica's heartbeat so the others stop counting it at once.
func (a *App) retire(ctx context.Context) {
	if t, ok := a.replicaTable(); ok {
		_ = t.RetireReplica(ctx, a.Engine.ReplicaID())
	}
}

// heartbeatLoop beats every third of the lease ttl until ctx ends, then retires.
func (a *App) heartbeatLoop(ctx context.Context) {
	defer a.retire(context.WithoutCancel(ctx))
	t := time.NewTicker(max(a.Engine.LeaseDuration()/3, time.Second))
	defer t.Stop()
	for {
		a.beat(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// unpolledSources is every source a live replica has configured that no live replica polls,
// sorted.
func unpolledSources(live []store.ReplicaInfo) []string {
	polled, configured := map[string]bool{}, map[string]bool{}
	for _, r := range live {
		for _, s := range r.Polled {
			polled[s] = true
		}
		for _, s := range r.Configured {
			configured[s] = true
		}
	}
	var out []string
	for s := range configured {
		if !polled[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
