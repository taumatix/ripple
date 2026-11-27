package app

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
)

// Tick is exported for tests and cron, and a test calls it right after Run has started in
// another goroutine. Run fills in the App's queue (init, under a sync.Once) while Tick's
// housekeeping reads the queue's clock, so Tick must go through the same Once or the two race
// (seen as a flaky DATA RACE in the e2e suite after an in-test restart). Run under -race.
func TestHousekeepingDoesNotRaceWithRunStartingTheApp(t *testing.T) {
	for i := 0; i < 200; i++ {
		st := store.NewMemory()
		a := &App{Engine: &engine.Engine{Store: st}, Log: slog.New(slog.DiscardHandler), Queue: &Queue{}}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); a.init() }() // what Run does first
		a.housekeep(context.Background())
		wg.Wait()
	}
}
