// Package app is the long-running service: webhook receiver, feed poller and job runner.
package app

import (
	"context"
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

var errQueueFull = errors.New("event queue full")

type App struct {
	Engine  *engine.Engine
	Watcher *engine.Watcher
	Secret  []byte
	Log     *slog.Logger
	// PollEvery is the feed poll and job run period; zero disables the loop (tests call Tick).
	PollEvery time.Duration

	events chan webhook.Event
	once   sync.Once
}

func (a *App) init() {
	a.once.Do(func() { a.events = make(chan webhook.Event, 1024) })
}

// Handle queues a verified event so the HTTP response does not wait on a clone.
func (a *App) Handle(_ context.Context, ev webhook.Event) error {
	a.init()
	select {
	case a.events <- ev:
		return nil
	default:
		return errQueueFull
	}
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
	go func() { defer wg.Done(); a.eventLoop(ctx) }()
	if a.PollEvery > 0 {
		wg.Add(1)
		go func() { defer wg.Done(); a.tickLoop(ctx) }()
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

func (a *App) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-a.events:
			if err := a.process(ctx, ev); err != nil {
				a.Log.Error("event failed", "kind", ev.Kind, "err", err)
			}
		}
	}
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

func (a *App) tickLoop(ctx context.Context) {
	t := time.NewTicker(a.PollEvery)
	defer t.Stop()
	for {
		a.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) process(ctx context.Context, ev webhook.Event) error {
	var errs []error
	for _, name := range ev.Repos {
		switch ev.Kind {
		case "repos_added":
			if err := a.Engine.Scan(ctx, model.Repo{FullName: name, InstallationID: ev.InstallationID}); err != nil {
				errs = append(errs, err)
			}
		case "repos_removed":
			if err := a.Engine.Remove(ctx, name); err != nil {
				errs = append(errs, err)
			}
		case "push":
			if err := a.push(ctx, name, ev); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// push re-scans a registered repo when its default branch changed a file a manager
// or the config cares about.
func (a *App) push(ctx context.Context, name string, ev webhook.Event) error {
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
