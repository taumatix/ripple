// Package app is the long-running service: webhook receiver, feed poller and job runner.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/webhook"
)

type App struct {
	Engine  *engine.Engine
	Watcher *engine.Watcher
	Secret  []byte
	Log     *slog.Logger
	// PollEvery is the feed poll and job run period; zero disables the loop (tests call Tick).
	PollEvery time.Duration
	// Queue holds received events until they are handled; nil means a Queue with
	// its defaults over Engine.Store. Its Store and Process are filled in when unset.
	Queue *Queue

	once sync.Once
	jobs engine.JobRunner
}

func (a *App) init() {
	a.once.Do(func() {
		if a.Queue == nil {
			a.Queue = &Queue{}
		}
		if a.Queue.Store == nil {
			a.Queue.Store = a.Engine.Store
		}
		if a.Queue.Process == nil {
			a.Queue.Process = a.process
		}
		if a.Queue.Log == nil {
			a.Queue.Log = a.Log
		}
		a.jobs.Engine = a.Engine
	})
}

// maxDeliveryID is the longest X-GitHub-Delivery used as-is in an event id (GitHub sends a 36-character GUID).
const maxDeliveryID = 64

// Handle persists a verified event, one queued event per repo, so the HTTP response
// does not wait on a clone and a restart does not lose it. ErrBacklogFull (the
// receiver answers 503) when the backlog is at its cap.
func (a *App) Handle(ctx context.Context, ev webhook.Event) error {
	a.init()
	prefix := ev.Delivery
	switch {
	case prefix == "":
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		prefix = "nodelivery-" + hex.EncodeToString(b)
	case len(prefix) > maxDeliveryID:
		// The header is unsigned and unbounded; a store key is not (DynamoDB: 2 KB).
		h := sha256.Sum256([]byte(prefix))
		prefix = "sha256-" + hex.EncodeToString(h[:])
	}
	evs := make([]model.Event, 0, len(ev.Repos))
	for i, repo := range ev.Repos {
		evs = append(evs, model.Event{ID: fmt.Sprintf("%s/%d", prefix, i), InstallationID: ev.InstallationID,
			Kind: ev.Kind, Repo: repo, Ref: ev.Ref, Files: ev.Files})
	}
	return a.Queue.Add(ctx, evs...)
}

// Handler serves POST /webhook and GET /healthz.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/webhook", &webhook.Handler{Secret: a.Secret, Sink: a, Log: a.Log})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

// Run serves HTTP on ln and runs the background loops until ctx ends.
func (a *App) Run(ctx context.Context, ln net.Listener) error {
	a.init()
	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: time.Minute}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := a.Queue.Run(ctx); err != nil {
			a.Log.Error("event queue", "err", err)
		}
	}()
	if a.PollEvery > 0 {
		wg.Add(2)
		go func() { defer wg.Done(); a.pollLoop(ctx) }()
		go func() { defer wg.Done(); a.jobs.Run(ctx, a.PollEvery) }()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// Tick runs one feed poll and one pass over due jobs; exported for tests and for
// operators who drive it from cron.
func (a *App) Tick(ctx context.Context) {
	if n, err := a.Watcher.Poll(ctx); err != nil {
		a.Log.Error("feed poll", "err", err)
	} else if n > 0 {
		a.Log.Info("feed poll enqueued jobs", "jobs", n)
	}
	if _, err := a.Engine.RunDue(ctx); err != nil && ctx.Err() == nil {
		a.Log.Error("run due jobs", "err", err)
	}
}

// pollLoop polls the feed every PollEvery. Jobs run on their own loop (see
// engine.JobRunner), so a slow job never delays the next poll.
func (a *App) pollLoop(ctx context.Context) {
	t := time.NewTicker(a.PollEvery)
	defer t.Stop()
	for {
		if n, err := a.Watcher.Poll(ctx); err != nil {
			if ctx.Err() == nil {
				a.Log.Error("feed poll", "err", err)
			}
		} else if n > 0 {
			a.Log.Info("feed poll enqueued jobs", "jobs", n)
			a.jobs.Wake()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// process handles one queued event.
func (a *App) process(ctx context.Context, ev model.Event) error {
	switch ev.Kind {
	case "repos_added":
		return a.Engine.Scan(ctx, model.Repo{FullName: ev.Repo, InstallationID: ev.InstallationID})
	case "repos_removed":
		return a.Engine.Remove(ctx, ev.Repo)
	case "push":
		return a.push(ctx, ev.Repo, ev)
	}
	a.Log.Warn("unknown queued event kind; dropped", "kind", ev.Kind, "event", ev.ID)
	return nil
}

// push re-scans a registered repo when its default branch changed a file a manager
// or the config cares about.
func (a *App) push(ctx context.Context, name string, ev model.Event) error {
	repo, ok, err := a.Engine.Store.GetRepo(ctx, name)
	if err != nil || !ok {
		return err
	}
	if repo.DefaultBranch == "" || ev.Ref != "refs/heads/"+repo.DefaultBranch {
		return nil
	}
	relevant := len(ev.Files) == 0
	if cfg, cerr := engine.RepoConfig(repo.Config); cerr != nil {
		// An invalid file registers no dependencies; only a change to the file can fix it.
		for _, f := range ev.Files {
			relevant = relevant || f == config.FileName || f == config.LegacyFileName
		}
	} else {
		ms, err := manager.Build(cfg)
		if err != nil {
			return fmt.Errorf("push %s: %w", name, err)
		}
		for _, f := range ev.Files {
			relevant = relevant || manager.Relevant(ms, f)
		}
	}
	if !relevant {
		return nil
	}
	repo.InstallationID = ev.InstallationID
	return a.Engine.Scan(ctx, repo)
}
