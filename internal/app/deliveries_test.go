package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
	"github.com/taumatix/ripple/internal/store"
)

const deliveryPush = `{"ref":"refs/heads/main","installation":{"id":7},"repository":{"id":4242,"full_name":"acme/web","default_branch":"main"},"commits":[{"modified":["go.mod"]}]}`

var deliverySecret = []byte("s3cret")

// postSigned sends one signed GitHub push delivery to h over HTTP and returns the status.
func postSigned(h http.Handler, delivery string) int {
	mac := hmac.New(sha256.New, deliverySecret)
	mac.Write([]byte(deliveryPush))
	r := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(deliveryPush)))
	r.Header.Set("X-GitHub-Event", "push")
	r.Header.Set("X-GitHub-Delivery", delivery)
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// replica is a fresh process: its own receiver and in-memory delivery table, over st.
func replica(st store.Store, backlog int) *App {
	return &App{Secret: deliverySecret, Log: slog.New(slog.DiscardHandler), Queue: &Queue{Store: st, Process: newGate().process, Backlog: backlog}}
}

// A delivery handled before a restart, or by another replica, is dropped by a process that
// never saw it: the table lives in the Store.
func TestDeliveryHandledOnceAcrossRestartsAndReplicas(t *testing.T) {
	st := store.NewMemory()
	if c := postSigned(replica(st, 0).Handler(), "d1"); c != 202 {
		t.Fatalf("first delivery = %d", c)
	}
	drain(t, st) // handled and gone from the queue, so only the delivery table remembers it
	if c := postSigned(replica(st, 0).Handler(), "d1"); c != 202 {
		t.Fatalf("redelivery = %d", c)
	}
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("a redelivery after a restart was handled again: %v", got)
	}
	if c := postSigned(replica(st, 0).Handler(), "d2"); c != 202 || len(queued(t, st)) != 1 {
		t.Fatalf("a new delivery must still be handled: %d, %v", c, queued(t, st))
	}
}

// flakyStore refuses to persist events while down is set.
type flakyStore struct {
	*store.Memory
	down bool
}

func (f *flakyStore) EnqueueEvent(ctx context.Context, e model.Event) (bool, error) {
	if f.down {
		return false, errors.New("store unavailable")
	}
	return f.Memory.EnqueueEvent(ctx, e)
}

func (f *flakyStore) EnqueueSequenced(ctx context.Context, e model.Event) (model.Event, bool, error) {
	if f.down {
		return e, false, errors.New("store unavailable")
	}
	return f.Memory.EnqueueSequenced(ctx, e)
}

// A delivery that could not be queued is forgotten, so the host's retry is handled.
func TestRefusedDeliveryCanBeRetried(t *testing.T) {
	st := &flakyStore{Memory: store.NewMemory(), down: true}
	a := replica(st, 0)
	if c := postSigned(a.Handler(), "d1"); c != 503 {
		t.Fatalf("an event that cannot be stored must answer 503, got %d", c)
	}
	st.down = false
	if c := postSigned(replica(st, 0).Handler(), "d1"); c != 202 || len(queued(t, st)) != 1 {
		t.Fatalf("retry of a refused delivery = %d, queued %v", c, queued(t, st))
	}
}

func TestDeliveryTTLNegativeDisablesTheStoreTable(t *testing.T) {
	st := store.NewMemory()
	a := replica(st, 0)
	a.DeliveryTTL = -1
	postSigned(a.Handler(), "d1")
	drain(t, st)
	b := replica(st, 0)
	b.DeliveryTTL = -1
	postSigned(b.Handler(), "d1")
	if len(queued(t, st)) != 1 {
		t.Fatalf("with the table off a fresh process handles the repeat: %v", queued(t, st))
	}
}

// A delivery arriving while the queue loop starts must not race on the queue's defaults:
// the receiver reads Queue.Now that Queue.Run fills in (run under -race).
func TestDeliveryWhileQueueStartsDoesNotRace(t *testing.T) {
	st := store.NewMemory()
	a := replica(st, 0)
	a.init() // as App.Run does before it starts anything
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Queue.Run(ctx) }()
	if c := postSigned(a.Handler(), "race-1"); c != 202 {
		t.Fatalf("delivery = %d", c)
	}
	cancel()
	<-done
}

// Hosts whose payloads carry no delivery header (Gerrit, SCM-Manager) derive an id from
// what they record, so the store's table drops a redelivery after a restart too.
func TestRedeliveryOfAGerritUpdateIsDroppedAcrossReplicas(t *testing.T) {
	st := store.NewMemory()
	body := `{"type":"ref-updated","refUpdate":{"oldRev":"1111111111111111111111111111111111111111","newRev":"2222222222222222222222222222222222222222","refName":"refs/heads/main","project":"acme/app"}}`
	post := func() {
		a := replica(st, 0)
		a.Hosts = []*platform.Host{{Name: "gerrit", Receiver: func(s platform.Sink) http.Handler {
			return &gerrit.Receiver{Secret: []byte("s"), Sink: s, Log: slog.New(slog.DiscardHandler)}
		}}}
		r := httptest.NewRequest(http.MethodPost, "/webhook/gerrit?token=s", strings.NewReader(body))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d", w.Code)
		}
	}
	post()
	drain(t, st)
	post()
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("a redelivery after a restart was handled again: %v", got)
	}
}
