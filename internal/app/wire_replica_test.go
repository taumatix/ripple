package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/engine"
)

func TestWireReplicaSharesOneIdentity(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	for _, name := range []string{"", "ripple-2"} {
		eng, q := &engine.Engine{}, &Queue{}
		wireReplica(eng, q, &config.Server{Replica: name, LeaseTTL: 45 * time.Second, Store: "memory"}, log)
		if eng.Replica == "" || eng.Replica != q.Owner {
			t.Fatalf("replica %q: engine %q, queue %q", name, eng.Replica, q.Owner)
		}
		if name != "" && eng.Replica != name {
			t.Errorf("configured name not used: %q", eng.Replica)
		}
		if eng.LeaseTTL != 45*time.Second || q.LeaseTTL != 45*time.Second {
			t.Errorf("lease ttl: %v %v", eng.LeaseTTL, q.LeaseTTL)
		}
	}
	if !strings.Contains(buf.String(), "replica=ripple-2") || !strings.Contains(buf.String(), "lease_ttl=45s") {
		t.Errorf("replica not logged:\n%s", buf.String())
	}
}
