package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/taumatix/ripple/internal/feed"
)

const maxPagesPerPoll = 100

// Watcher polls one global feed. The cursor is the timestamp of the last release
// seen; the feed is inclusive so the boundary entry is seen twice, which FanOut's
// idempotent job keys absorb.
//
// Only releases of packages some repo depends on are stored: the feed carries every
// module version published on the internet, nearly all of it irrelevant here. The
// price is that a repo registered later has no history of releases from before it
// registered; the roadmap lists a backfill.
type Watcher struct {
	Engine *Engine
	Feed   feed.Feed
}

// Poll drains the feed from the stored cursor and returns how many jobs were enqueued.
// On first run it starts at the current time rather than replaying history.
func (w *Watcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	name := w.Feed.Name()
	cursor, err := e.Store.GetCursor(ctx, name)
	if err != nil {
		return 0, err
	}
	if cursor == "" {
		cursor = e.now().UTC().Format(time.RFC3339Nano)
		return 0, e.Store.SetCursor(ctx, name, cursor)
	}
	jobs := 0
	for page := 0; page < maxPagesPerPoll; page++ {
		rels, more, err := w.Feed.Fetch(ctx, cursor)
		if err != nil {
			return jobs, fmt.Errorf("fetch %s: %w", name, err)
		}
		for _, rel := range rels {
			deps, err := e.Store.ListDependents(ctx, rel.Datasource, rel.Package)
			if err != nil {
				return jobs, err
			}
			if len(deps) == 0 {
				continue
			}
			if _, err := e.Store.RecordRelease(ctx, rel); err != nil {
				return jobs, err
			}
			n, err := e.FanOut(ctx, rel)
			jobs += n
			if err != nil {
				return jobs, err
			}
		}
		if len(rels) == 0 {
			return jobs, nil
		}
		next := rels[len(rels)-1].Time.UTC().Format(time.RFC3339Nano)
		if next != cursor {
			if err := e.Store.SetCursor(ctx, name, next); err != nil {
				return jobs, err
			}
		}
		if !more || next == cursor {
			return jobs, nil
		}
		cursor = next
	}
	return jobs, nil
}
