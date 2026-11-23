package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestHoldLogsATakeoverButNotAFreshGrant(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	m := NewMemory()
	ctx := context.Background()

	// A holder that dies without releasing: its lease lapses and the next owner takes it over.
	if _, ok, err := m.AcquireLease(ctx, "repo:a/b", "dead", time.Millisecond); err != nil || !ok {
		t.Fatalf("seed: %v %v", ok, err)
	}
	time.Sleep(5 * time.Millisecond)
	h, err := Hold(ctx, m, log, "repo:a/b", "live", time.Minute)
	if err != nil || h == nil {
		t.Fatalf("hold: %v %v", h, err)
	}
	h.Release()
	if out := buf.String(); !strings.Contains(out, "lease taken over") || !strings.Contains(out, "repo:a/b") || !strings.Contains(out, "owner=live") {
		t.Errorf("takeover not logged:\n%s", out)
	}

	buf.Reset()
	h, err = Hold(ctx, m, log, "repo:c/d", "live", time.Minute)
	if err != nil || h == nil {
		t.Fatalf("hold: %v %v", h, err)
	}
	h.Release()
	if buf.Len() != 0 {
		t.Errorf("a fresh grant logged:\n%s", buf.String())
	}
}

func TestLeaseTakeoversCountsOnlyTakeovers(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	before := LeaseTakeovers()
	h, err := Hold(ctx, m, nil, "repo:a/b", "a", time.Minute)
	if err != nil || h == nil {
		t.Fatalf("hold: %v %v", h, err)
	}
	h.Release()
	if got := LeaseTakeovers() - before; got != 0 {
		t.Fatalf("a fresh grant counted as %d takeovers", got)
	}
	if _, ok, err := m.AcquireLease(ctx, "repo:c/d", "dead", time.Millisecond); err != nil || !ok {
		t.Fatalf("seed: %v %v", ok, err)
	}
	time.Sleep(5 * time.Millisecond)
	h, err = Hold(ctx, m, nil, "repo:c/d", "live", time.Minute)
	if err != nil || h == nil {
		t.Fatalf("hold: %v %v", h, err)
	}
	h.Release()
	if got := LeaseTakeovers() - before; got != 1 {
		t.Fatalf("takeovers = %d, want 1", got)
	}
}
