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
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/webhook"
)

type App struct {
	Engine  *engine.Engine
	Watcher *engine.Watcher
	// Toolchain polls the Go release list for repos that opted into go-toolchain
	// updates; nil leaves the feature out.
	Toolchain *engine.ToolchainWatcher
	Secret    []byte
	Log       *slog.Logger
	// PollEvery is the feed poll and job run period; zero disables the loop (tests call Tick).
	PollEvery time.Duration
	// Advisories polls OSV when set (RIPPLE_OSV). It runs on its own loop every
	// AdvisoryEvery, so an OSV outage or slowdown never delays the feed poll or the jobs.
	Advisories    *engine.AdvisoryWatcher
	AdvisoryEvery time.Duration
	// Queue holds received events until they are handled; nil means a Queue with
	// its defaults over Engine.Store. Its Store and Process are filled in when unset.
	Queue *Queue
	// Hosts are the registered code hosts. Nil means GitHub alone, its receiver
	// built from Secret at /webhook (the v0.1 shape). Each host's receiver is served
	// at /webhook/<name>, the default host's also at /webhook; a host that is a
	// platform.Lister is discovered by listing on every poll.
	Hosts []*platform.Host
	// DefaultHost names the host whose repos are stored unprefixed; empty means github.
	DefaultHost string

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

// maxDeliveryID is the longest delivery id used as-is in an event id (GitHub sends a 36-character GUID).
const maxDeliveryID = 64

func (a *App) defaultHost() string {
	if a.DefaultHost != "" {
		return a.DefaultHost
	}
	return github.HostName
}

// repoName is the stored name of a host's repo; see platform.Mux.
func (a *App) repoName(host, path string) string {
	if host == a.defaultHost() {
		return path
	}
	return host + ":" + path
}

// Handle queues a GitHub receiver's event as the default host's.
func (a *App) Handle(ctx context.Context, ev webhook.Event) error {
	return github.Sink{Sink: a}.Handle(ctx, ev)
}

// HandleEvent queues an event of the default host.
func (a *App) HandleEvent(ctx context.Context, ev platform.Event) error {
	return a.enqueue(ctx, a.defaultHost(), ev)
}

// hostSink is the sink one host's receiver writes to.
type hostSink struct {
	a    *App
	host string
}

func (h hostSink) HandleEvent(ctx context.Context, ev platform.Event) error {
	return h.a.enqueue(ctx, h.host, ev)
}

// enqueue persists a verified event, one queued event per repo, so the HTTP response
// does not wait on a clone and a restart does not lose it. ErrBacklogFull (the
// receiver answers 503) when the backlog is at its cap.
func (a *App) enqueue(ctx context.Context, host string, ev platform.Event) error {
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
	if host != a.defaultHost() {
		prefix = host + "/" + prefix
	}
	evs := make([]model.Event, 0, len(ev.Repos))
	for i, repo := range ev.Repos {
		if host == a.defaultHost() && strings.Contains(repo, ":") {
			// Unprefixed names must not look prefixed, or the Mux would route them to another host.
			a.log().Warn("repository name with a colon from the default host; dropped", "host", host, "repo", repo)
			continue
		}
		ref := ev.Ref
		switch ev.Kind {
		case platform.RepoRenamed:
			if ev.NewRepo == "" || (host == a.defaultHost() && strings.Contains(ev.NewRepo, ":")) {
				a.log().Warn("rename without a usable new name; dropped", "host", host, "repo", repo)
				continue
			}
			ref = a.repoName(host, ev.NewRepo)
		case platform.DefaultBranchChanged:
			if ev.DefaultBranch == "" {
				continue
			}
			ref = "refs/heads/" + ev.DefaultBranch
		}
		me := model.Event{ID: fmt.Sprintf("%s/%d", prefix, i), InstallationID: ev.Scope,
			Kind: string(ev.Kind), Repo: a.repoName(host, repo), Ref: ref, Files: ev.Files, DefaultBranch: ev.DefaultBranch, RepoID: ev.RepoID}
		if ev.Kind == platform.RepoRenamed {
			me.NewRepo = ref
		}
		evs = append(evs, me)
	}
	return a.Queue.Add(ctx, evs...)
}

// Handler serves POST /webhook, POST /webhook/<host> and GET /healthz.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	if a.Hosts == nil {
		mux.Handle("/webhook", &webhook.Handler{Secret: a.Secret, Sink: a, Log: a.Log})
	}
	for _, h := range a.Hosts {
		if h.Receiver == nil {
			continue
		}
		rh := h.Receiver(hostSink{a: a, host: h.Name})
		mux.Handle("/webhook/"+h.Name, rh)
		if h.Name == a.defaultHost() {
			mux.Handle("/webhook", rh)
		}
	}
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
	if a.Advisories != nil && a.AdvisoryEvery > 0 {
		wg.Add(1)
		go func() { defer wg.Done(); a.advisoryLoop(ctx) }()
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
	a.Discover(ctx)
	if n, err := a.Watcher.Poll(ctx); err != nil {
		a.Log.Error("feed poll", "err", err)
	} else if n > 0 {
		a.Log.Info("feed poll enqueued jobs", "jobs", n)
	}
	a.pollToolchain(ctx)
	a.pollAdvisories(ctx)
	if _, err := a.Engine.RunDue(ctx); err != nil && ctx.Err() == nil {
		a.Log.Error("run due jobs", "err", err)
	}
}

// pollAdvisories runs one OSV poll when enabled. A failure is logged and nothing else:
// normal updates never wait on OSV.
func (a *App) pollAdvisories(ctx context.Context) {
	if a.Advisories == nil {
		return
	}
	if n, err := a.Advisories.Poll(ctx); err != nil {
		if ctx.Err() == nil {
			a.Log.Warn("osv poll", "err", err)
		}
	} else if n > 0 {
		a.Log.Info("osv poll enqueued security jobs", "jobs", n)
		a.jobs.Wake()
	}
}

func (a *App) advisoryLoop(ctx context.Context) {
	t := time.NewTicker(a.AdvisoryEvery)
	defer t.Stop()
	for {
		a.pollAdvisories(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pollLoop polls the feed every PollEvery. Jobs run on their own loop (see
// engine.JobRunner), so a slow job never delays the next poll.
func (a *App) pollLoop(ctx context.Context) {
	t := time.NewTicker(a.PollEvery)
	defer t.Stop()
	for {
		a.Discover(ctx)
		if n, err := a.Watcher.Poll(ctx); err != nil {
			if ctx.Err() == nil {
				a.Log.Error("feed poll", "err", err)
			}
		} else if n > 0 {
			a.Log.Info("feed poll enqueued jobs", "jobs", n)
			a.jobs.Wake()
		}
		if a.pollToolchain(ctx) > 0 {
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
		if err := a.Engine.ForgetRename(ctx, ev.Repo); err != nil {
			return err
		}
		return a.Engine.Scan(ctx, model.Repo{FullName: ev.Repo, InstallationID: ev.InstallationID})
	case "repos_removed":
		return a.Engine.Remove(ctx, ev.Repo)
	case "push":
		return a.push(ctx, ev.Repo, ev)
	case string(platform.RepoRenamed):
		return a.Engine.Rename(ctx, ev.Repo, firstNonEmpty(ev.NewRepo, ev.Ref), ev.InstallationID)
	case string(platform.DefaultBranchChanged):
		return a.Engine.SetDefaultBranch(ctx, ev.Repo, firstNonEmpty(ev.DefaultBranch, strings.TrimPrefix(ev.Ref, "refs/heads/")), ev.InstallationID)
	}
	a.Log.Warn("unknown queued event kind; dropped", "kind", ev.Kind, "event", ev.ID)
	return nil
}

// firstNonEmpty picks the typed event field, falling back to the deprecated overloaded
// Ref for events queued before the typed fields existed (ripple:deprecated event-ref-overload).
func firstNonEmpty(typed, legacy string) string {
	if typed != "" {
		return typed
	}
	return legacy
}

// push re-scans a registered repo when its default branch changed a file a manager
// or the config cares about.
func (a *App) push(ctx context.Context, name string, ev model.Event) error {
	repo, ok, err := a.Engine.Store.GetRepo(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return a.pushUnknown(ctx, name, ev)
	}
	// The payload's default_branch is only a hint that the stored one may be stale (a
	// missed repository.edited); the engine confirms it with the host before storing.
	if healed, err := a.Engine.HealDefaultBranch(ctx, repo, ev.DefaultBranch, ev.InstallationID); healed || err != nil {
		return err
	}
	if repo.DefaultBranch == "" || ev.Ref != "refs/heads/"+repo.DefaultBranch {
		return nil
	}
	relevant := len(ev.Files) == 0
	if cfg, cerr := engine.RepoConfig(repo.Config); cerr != nil {
		// An invalid file registers no dependencies; only a change to the file can fix it.
		for _, f := range ev.Files {
			relevant = relevant || f == config.FileName || (f == config.LegacyFileName && platform.ReadsLegacyConfig(a.Engine.Platform, repo))
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

// pushUnknown handles a verified push for a name ripple does not have. A name that was
// renamed away from (a late delivery under the old name) is the renamed repo's push. For a
// host that gave an immutable repository id the push beat the event that registers or
// renames the repo, so it is registered under the name the push carries; for any other
// host an unknown repo is ignored, as before.
func (a *App) pushUnknown(ctx context.Context, name string, ev model.Event) error {
	if target, ok := a.Engine.RenamedTo(ctx, name); ok {
		return a.push(ctx, target, ev)
	}
	if ev.RepoID == 0 || ev.InstallationID == 0 {
		return nil
	}
	return a.Engine.RegisterFromPush(ctx, name, ev.InstallationID)
}

// pollToolchain polls the Go release list and returns how many jobs it enqueued.
func (a *App) pollToolchain(ctx context.Context) int {
	if a.Toolchain == nil {
		return 0
	}
	n, err := a.Toolchain.Poll(ctx)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("go release poll", "err", err)
	case n > 0:
		a.Log.Info("go release poll enqueued jobs", "jobs", n)
	}
	return n
}
