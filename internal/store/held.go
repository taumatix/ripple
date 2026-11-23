package store

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrLeaseLost is the cause of a Held's context being cancelled, and what Fence returns,
// when another owner holds the name now.
var ErrLeaseLost = errors.New("lease lost to another replica")

// Held is a lease kept renewed in the background. Ctx is cancelled (cause ErrLeaseLost)
// when the lease is lost; Fence is the synchronous check before an irreversible step.
type Held struct {
	Ctx context.Context

	s      Leases
	ttl    time.Duration
	log    *slog.Logger
	cancel context.CancelCauseFunc
	stop   chan struct{}
	done   sync.WaitGroup

	mu    sync.Mutex
	lease Lease
}

// Hold tries once to acquire name for owner. When another owner holds it the result is
// (nil, nil). The returned Held renews every ttl/3 until Release; the returned Ctx
// derives from ctx.
func Hold(ctx context.Context, s Leases, log *slog.Logger, name, owner string, ttl time.Duration) (*Held, error) {
	l, ok, err := s.AcquireLease(ctx, name, owner, ttl)
	if err != nil || !ok {
		return nil, err
	}
	if l.Token > 1 && log != nil {
		// Release deletes the row, so a token above 1 means the previous holder did not give
		// the name up: it died or stalled past its ttl.
		log.Info("lease taken over", "lease", name, "owner", owner, "token", l.Token)
	}
	h := &Held{s: s, ttl: ttl, log: log, lease: l, stop: make(chan struct{})}
	h.Ctx, h.cancel = context.WithCancelCause(ctx)
	h.done.Add(1)
	go h.renew()
	return h, nil
}

// Lease returns the current grant.
func (h *Held) Lease() Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lease
}

// renew extends the lease every ttl/3. A refusal means another owner holds it; a store
// error is retried until the lease's own expiry has passed, since by then another
// replica may hold it and the work must stop.
func (h *Held) renew() {
	defer h.done.Done()
	t := time.NewTicker(h.ttl / 3)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-h.Ctx.Done():
			return
		case <-t.C:
		}
		if err := h.Fence(); err != nil {
			if h.log != nil {
				h.log.Warn("lease not renewed; stopping the work", "lease", h.Lease().Name)
			}
			h.cancel(ErrLeaseLost)
			return
		}
	}
}

// Fence renews the lease now and returns ErrLeaseLost if it is no longer ours. A transient
// store error is not a loss while the lease has not run out.
func (h *Held) Fence() error {
	cur := h.Lease()
	l, ok, err := h.s.RenewLease(context.WithoutCancel(h.Ctx), cur, h.ttl)
	switch {
	case err != nil && time.Now().Before(cur.Expires):
		return nil
	case err != nil || !ok:
		return ErrLeaseLost
	}
	h.mu.Lock()
	h.lease = l
	h.mu.Unlock()
	return nil
}

// Release stops renewing and gives the name back, even when the work's context is done.
func (h *Held) Release() {
	close(h.stop)
	h.done.Wait()
	if err := h.s.ReleaseLease(context.WithoutCancel(h.Ctx), h.Lease()); err != nil && h.log != nil {
		h.log.Warn("release lease", "lease", h.Lease().Name, "err", err)
	}
	h.cancel(nil)
}
