package app

import (
	"encoding/json"
	"net/http"

	"github.com/taumatix/ripple/internal/store"
)

// replicaStatus is GET /replica: the answering replica's name, whether it is the feed
// poller, and how many leases this process has taken over from a holder that died or stalled.
// The service has no metrics endpoint; this is the one thing an operator can scrape. The
// number of replicas is not here, since no replica can list the others (see ROADMAP).
func (a *App) replicaStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := struct {
		Replica         string `json:"replica"`
		FeedPoller      bool   `json:"feed_poller"`
		LeaseTakeovers  int64  `json:"lease_takeovers"`
		LeaseTTLSeconds int    `json:"lease_ttl_seconds"`
	}{LeaseTakeovers: store.LeaseTakeovers()}
	if a.Engine != nil {
		out.Replica, out.LeaseTTLSeconds = a.Engine.ReplicaID(), int(a.Engine.LeaseDuration().Seconds())
	}
	if a.Watcher != nil {
		out.FeedPoller = a.Watcher.Elected()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
