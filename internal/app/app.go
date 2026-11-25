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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/metrics"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/webhook"
)

type App struct {
	closers []func() // run by Close, in reverse order
	Engine  *engine.Engine
	Watcher *engine.Watcher
	// Toolchain polls the Go release list for repos that opted into go-toolchain
	// updates; nil leaves the feature out.
	Toolchain *engine.ToolchainWatcher
	// Actions polls the tags of the GitHub Actions the repos use (ecosystem:
	// github-actions); nil leaves the feature out.
	Actions *engine.ActionsWatcher
	// Docker polls the tags of the container images the repos use (ecosystem:
	// docker); nil leaves the feature out.
	Docker *engine.DockerWatcher
	// Npm polls the versions of the npm packages the repos use (ecosystem: npm);
	// nil leaves the feature out.
	Npm *engine.NpmWatcher
	// Cargo polls the versions of the crates the repos use (ecosystem: cargo); nil
	// leaves the feature out.
	Cargo *engine.CargoWatcher
	// Pip polls the versions of the Python projects the repos use (ecosystem: pip); nil
	// leaves the feature out.
	Pip *engine.PipWatcher
	// Helm polls the versions of the charts the repos depend on (ecosystem: helm); nil
	// polls nothing.
	Helm *engine.HelmWatcher
	// Rubygems polls the versions of the gems the repos use (ecosystem: bundler); nil
	// polls nothing.
	Rubygems *engine.RubygemsWatcher
	// Packagist polls the versions of the packages the repos use (ecosystem: composer); nil
	// polls nothing.
	Packagist *engine.PackagistWatcher
	// metrics and httpLatency are set by EnableMetrics; nil leaves /metrics out.
	metrics     *metrics.Registry
	httpLatency *metrics.Histogram
	Secret      []byte
	Log         *slog.Logger
	// PollEvery is the feed poll and job run period; zero disables the loop (tests call Tick).
	PollEvery time.Duration
	// Advisories polls OSV when set (RIPPLE_OSV). It runs on its own loop every
	// AdvisoryEvery, so an OSV outage or slowdown never delays the feed poll or the jobs.
	Advisories    *engine.AdvisoryWatcher
	AdvisoryEvery time.Duration
	// DriftCheckEvery, when positive and the store is DynamoDB, has Run compare every
	// package's dependents counter with its index on that period and log the gap
	// (RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL). It is read only; `ripple dynamodb recount` repairs.
	DriftCheckEvery time.Duration
	// Queue holds received events until they are handled; nil means a Queue with
	// its defaults over Engine.Store. Its Store and Process are filled in when unset.
	Queue *Queue
	// Hosts are the registered code hosts. Nil means GitHub alone, its receiver
	// built from Secret at /webhook (the v0.1 shape). Each host's receiver is served
	// at /webhook/<name>, the default host's also at /webhook; a host that is a
	// platform.Lister is discovered by listing on every poll.
	Hosts []*platform.Host
	// DeliveryTTL is how long a handled delivery id is remembered in the Store, so a
	// restart or another replica drops the host's redelivery of it. Default 1h (the
	// receivers' in-memory window); negative turns the store table off. It applies only
	// to a store that implements store.DeliveryTable (all of this module's do) and to
	// deliveries that carry an id.
	DeliveryTTL time.Duration
	// DefaultHost names the host whose repos are stored unprefixed; empty means github.
	DefaultHost string

	once sync.Once
	jobs engine.JobRunner

	keepMu                       sync.Mutex
	lastDeliveryPurge, lastSweep time.Time
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
func (a *App) enqueue(ctx context.Context, host string, ev platform.Event) (retErr error) {
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
	if ev.Delivery != "" {
		fresh, release, err := a.claimDelivery(ctx, prefix)
		if err != nil {
			return err
		}
		if !fresh {
			a.log().Info("webhook delivery already handled; dropped", "host", host, "delivery", ev.Delivery)
			return nil
		}
		defer func() {
			if retErr != nil {
				release()
			}
		}()
	}
	if ev.BodyKey != "" {
		// The same verified body under another delivery id (a replay, or a redelivery that got
		// a new id) is claimed once across replicas. The receiver sets BodyKey only when
		// signature dedupe is on.
		key := ev.BodyKey
		if host != a.defaultHost() {
			key = host + "/" + key
		}
		fresh, release, err := a.claimDelivery(ctx, key)
		if err != nil {
			return err
		}
		if !fresh {
			a.log().Info("webhook body already handled; dropped", "host", host)
			return nil
		}
		defer func() {
			if retErr != nil {
				release()
			}
		}()
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
		repoID := ev.RepoID
		if ev.Kind == platform.ReposAdded && i < len(ev.RepoIDs) {
			repoID = ev.RepoIDs[i]
		}
		me := model.Event{ID: fmt.Sprintf("%s/%d", prefix, i), InstallationID: ev.Scope,
			Kind: string(ev.Kind), Repo: a.repoName(host, repo), Ref: ref, Files: ev.Files, DefaultBranch: ev.DefaultBranch, RepoID: repoID}
		if ev.Kind == platform.RepoRenamed {
			me.NewRepo = ref
		}
		evs = append(evs, me)
	}
	return a.Queue.Add(ctx, evs...)
}

// Handler serves POST /webhook, POST /webhook/<host>, GET /healthz and GET /replica, and
// GET /metrics after EnableMetrics.
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
	mux.HandleFunc("/replica", a.replicaStatus)
	if a.metrics != nil {
		mux.Handle("/metrics", a.metrics.Handler())
		return metrics.Instrument(a.httpLatency, metricsRoute, mux)
	}
	return mux
}

// newServer is the HTTP server with the deadlines that bound a slow or stalled client: the
// header read, the whole request read (the backstop under each receiver's own per-body
// deadline, which needs a ResponseWriter that supports deadlines) and the response write.
func newServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
}

// Close releases what New started outside the process (the confined container network of
// RIPPLE_CONTAINER_CONFINE). It is safe to call more than once and after Run returns.
func (a *App) Close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
}

// Run serves HTTP on ln and runs the background loops until ctx ends.
func (a *App) Run(ctx context.Context, ln net.Listener) error {
	a.init()
	srv := newServer(a.Handler())
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
	if _, ok := a.replicaTable(); ok {
		wg.Add(1)
		go func() { defer wg.Done(); a.heartbeatLoop(ctx) }()
	}
	if a.PollEvery > 0 {
		wg.Add(2)
		go func() { defer wg.Done(); a.pollLoop(ctx) }()
		go func() { defer wg.Done(); a.jobs.Run(ctx, a.PollEvery) }()
	}
	if a.Advisories != nil && a.AdvisoryEvery > 0 {
		wg.Add(1)
		go func() { defer wg.Done(); a.advisoryLoop(ctx) }()
	}
	if d, ok := a.Engine.Store.(*store.Dynamo); ok && a.DriftCheckEvery > 0 {
		wg.Add(1)
		go func() { defer wg.Done(); a.driftLoop(ctx, d) }()
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
	a.pollActions(ctx)
	a.pollDocker(ctx)
	a.pollNpm(ctx)
	a.pollCargo(ctx)
	a.pollPip(ctx)
	a.pollHelm(ctx)
	a.pollRubygems(ctx)
	a.pollPackagist(ctx)
	a.pollAdvisories(ctx)
	a.housekeep(ctx)
	if _, err := a.Engine.RunDue(ctx); err != nil && ctx.Err() == nil {
		a.Log.Error("run due jobs", "err", err)
	}
}

// pollFeed polls the release feed if this replica is the elected poller (see
// engine.Watcher.PollElected) and returns the jobs it enqueued. The claim outlives a few
// poll periods, so a replica that dies is replaced within about two of them.
func (a *App) pollFeed(ctx context.Context) int {
	n, polled, err := a.Watcher.PollElected(ctx, 2*a.PollEvery)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			a.Log.Error("feed poll", "err", err)
		}
	case polled && n > 0:
		a.Log.Info("feed poll enqueued jobs", "jobs", n)
	}
	return n
}

// electedSource is a poller that one replica at a time runs: *engine.Watcher and the
// toolchain, Actions, Docker, npm, Cargo and pip watchers.
type electedSource interface {
	Poll(context.Context) (int, error)
	PollElected(context.Context, time.Duration) (int, bool, error)
	Elected() bool
	StepDown(context.Context)
}

// pollSource polls src once through its election (lease feed:<name>), like the release
// feed: of the replicas sharing a store one polls and the others skip. The claim lasts twice
// the poll interval, so a replica that dies is replaced within about two of them.
func (a *App) pollSource(ctx context.Context, src electedSource) (int, error) {
	n, _, err := src.PollElected(ctx, 2*a.PollEvery)
	return n, err
}

// sources lists the configured elected pollers by name.
func (a *App) sources() map[string]electedSource {
	m := map[string]electedSource{}
	if a.Watcher != nil {
		m["feed"] = a.Watcher
	}
	if a.Toolchain != nil {
		m["go-toolchain"] = a.Toolchain
	}
	if a.Actions != nil {
		m["github-actions"] = a.Actions
	}
	if a.Docker != nil {
		m["docker"] = a.Docker
	}
	if a.Npm != nil {
		m["npm"] = a.Npm
	}
	if a.Cargo != nil {
		m["cargo"] = a.Cargo
	}
	if a.Pip != nil {
		m["pip"] = a.Pip
	}
	if a.Helm != nil {
		m["helm"] = a.Helm
	}
	if a.Rubygems != nil {
		m["rubygems"] = a.Rubygems
	}
	if a.Packagist != nil {
		m["packagist"] = a.Packagist
	}
	if a.Advisories != nil {
		m["osv"] = a.Advisories
	}
	return m
}

// electedNames returns the sources this replica polls now, sorted.
func (a *App) electedNames() []string {
	var out []string
	for n, s := range a.sources() {
		if s.Elected() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// sourceNames returns every configured elected source, sorted.
func (a *App) sourceNames() []string {
	var out []string
	for n := range a.sources() {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (a *App) stepDownFeed(ctx context.Context) {
	for _, s := range a.sources() {
		s.StepDown(ctx)
	}
}

// pollAdvisories runs one OSV poll when enabled. A failure is logged and nothing else:
// normal updates never wait on OSV.
func (a *App) pollAdvisories(ctx context.Context) {
	if a.Advisories == nil {
		return
	}
	// The claim outlives two OSV intervals (or feed intervals when only Tick drives it).
	ttl := 2 * max(a.AdvisoryEvery, a.PollEvery)
	if n, _, err := a.Advisories.PollElected(ctx, ttl); err != nil {
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

// driftLoop checks the DynamoDB dependents counters against their index every
// DriftCheckEvery, after the first period, so a start is not also a whole-table scan.
func (a *App) driftLoop(ctx context.Context, d *store.Dynamo) {
	t := time.NewTicker(a.DriftCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.checkDrift(ctx, d)
	}
}

// checkDrift logs how many packages' counters disagree with the index (a warning naming
// the first few) or that none do. A failed check is logged and tried again next period.
func (a *App) checkDrift(ctx context.Context, d *store.Dynamo) {
	drift, err := d.CheckDependentCounts(ctx)
	switch {
	case ctx.Err() != nil:
	case err != nil:
		a.Log.Error("dependent counter check", "err", err)
	case len(drift) == 0:
		a.Log.Info("dependent counters match the index")
	default:
		first := drift[:min(len(drift), 3)]
		a.Log.Warn("dependent counters drifted from the index; run `ripple dynamodb recount`",
			"packages", len(drift), "first", first)
	}
}

// pollLoop polls the feed every PollEvery. Jobs run on their own loop (see
// engine.JobRunner), so a slow job never delays the next poll.
func (a *App) pollLoop(ctx context.Context) {
	t := time.NewTicker(a.PollEvery)
	defer t.Stop()
	defer a.stepDownFeed(context.WithoutCancel(ctx)) // hand the feed to another replica at once
	for {
		a.Discover(ctx)
		if a.pollFeed(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollToolchain(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollActions(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollDocker(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollNpm(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollCargo(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollPip(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollHelm(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollRubygems(ctx) > 0 {
			a.jobs.Wake()
		}
		if a.pollPackagist(ctx) > 0 {
			a.jobs.Wake()
		}
		a.housekeep(ctx)
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
		return a.Engine.Scan(ctx, model.Repo{FullName: ev.Repo, InstallationID: ev.InstallationID, RepoID: ev.RepoID})
	case "repos_removed":
		return a.Engine.Remove(ctx, ev.Repo)
	case string(platform.RepoArchived):
		return a.Engine.Pause(ctx, ev.Repo)
	case string(platform.RepoUnarchived):
		err := a.Engine.Resume(ctx, ev.Repo, ev.InstallationID)
		if err == nil {
			a.jobs.Wake()
		}
		return err
	case "push":
		return a.push(ctx, ev.Repo, ev)
	case string(platform.RepoRenamed):
		return a.Engine.Rename(ctx, ev.Repo, a.typedOrLegacy(ev, ev.NewRepo, ev.Ref), ev.InstallationID)
	case string(platform.DefaultBranchChanged):
		return a.Engine.SetDefaultBranch(ctx, ev.Repo, a.typedOrLegacy(ev, ev.DefaultBranch, strings.TrimPrefix(ev.Ref, "refs/heads/")), ev.InstallationID)
	}
	a.Log.Warn("unknown queued event kind; dropped", "kind", ev.Kind, "event", ev.ID)
	return nil
}

// typedOrLegacy picks the typed event field, falling back to the deprecated overloaded
// Ref for events queued before the typed fields existed (ripple:deprecated event-ref-overload).
// Each fallback is logged, so that "no queue holds old rows" (the condition for removing
// the overload, ROADMAP 76) can be read off the log of a deployment.
func (a *App) typedOrLegacy(ev model.Event, typed, legacy string) string {
	if typed != "" {
		return typed
	}
	if legacy != "" && a.Log != nil {
		a.Log.Warn("queued event has no typed field; using the deprecated Ref (ripple:deprecated event-ref-overload)", "kind", ev.Kind, "event", ev.ID, "repo", ev.Repo)
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
	if ev.RepoID != 0 {
		switch {
		case repo.RepoID == 0:
			if err := a.Engine.AdoptRepoID(ctx, name, ev.RepoID); err != nil {
				return err
			}
		case repo.RepoID != ev.RepoID:
			// Another repository than the one registered under this name, or the same
			// name recreated (a new id). The push cannot say which, so the host is asked
			// (once per id per hour); only a host that ties the id to this name replaces
			// the stored one. Otherwise the push is not acted on.
			adopted, err := a.Engine.ResolveRepoID(ctx, repo, ev.RepoID)
			if err != nil {
				return err
			}
			if !adopted {
				a.Log.Warn("push ignored: repository id mismatch", "repo", name, "stored", repo.RepoID, "push", ev.RepoID)
				return nil
			}
			repo.RepoID = ev.RepoID
		}
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
	// The record held under another name with this id is this repo before a rename the
	// rename event has not delivered yet.
	if old, ok := a.Engine.RepoIDOwner(ctx, name, ev.RepoID); ok && old != name {
		if err := a.Engine.Rename(ctx, old, name, ev.InstallationID); err != nil {
			return err
		}
		return a.push(ctx, name, ev)
	}
	if a.Engine.RemovedRecently(ctx, name, ev.RepoID) {
		a.Log.Info("push after removal ignored", "repo", name, "id", ev.RepoID)
		return nil
	}
	return a.Engine.RegisterFromPush(ctx, name, ev.InstallationID, ev.RepoID)
}

// pollActions polls the tags of the GitHub Actions in use and returns how many jobs it enqueued.
func (a *App) pollActions(ctx context.Context) int {
	if a.Actions == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Actions)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("github actions poll", "err", err)
	case n > 0:
		a.Log.Info("github actions poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollDocker polls the tags of the container images in use and returns how many jobs it enqueued.
func (a *App) pollDocker(ctx context.Context) int {
	if a.Docker == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Docker)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("container image poll", "err", err)
	case n > 0:
		a.Log.Info("container image poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollNpm polls the versions of the npm packages in use and returns how many jobs it enqueued.
func (a *App) pollNpm(ctx context.Context) int {
	if a.Npm == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Npm)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("npm poll", "err", err)
	case n > 0:
		a.Log.Info("npm poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollCargo polls the versions of the crates in use and returns how many jobs it enqueued.
func (a *App) pollCargo(ctx context.Context) int {
	if a.Cargo == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Cargo)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("cargo poll", "err", err)
	case n > 0:
		a.Log.Info("cargo poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollPip polls the versions of the Python projects in use and returns how many jobs it enqueued.
func (a *App) pollPip(ctx context.Context) int {
	if a.Pip == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Pip)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("pip poll", "err", err)
	case n > 0:
		a.Log.Info("pip poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollHelm polls the versions of the Helm charts in use and returns how many jobs it enqueued.
func (a *App) pollHelm(ctx context.Context) int {
	if a.Helm == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Helm)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("helm poll", "err", err)
	case n > 0:
		a.Log.Info("helm poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollRubygems polls the versions of the gems in use and returns how many jobs it enqueued.
func (a *App) pollRubygems(ctx context.Context) int {
	if a.Rubygems == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Rubygems)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("rubygems poll", "err", err)
	case n > 0:
		a.Log.Info("rubygems poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollPackagist polls the versions of the gems in use and returns how many jobs it enqueued.
func (a *App) pollPackagist(ctx context.Context) int {
	if a.Packagist == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Packagist)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("packagist poll", "err", err)
	case n > 0:
		a.Log.Info("packagist poll enqueued jobs", "jobs", n)
	}
	return n
}

// pollToolchain polls the Go release list and returns how many jobs it enqueued.
func (a *App) pollToolchain(ctx context.Context) int {
	if a.Toolchain == nil {
		return 0
	}
	n, err := a.pollSource(ctx, a.Toolchain)
	switch {
	case err != nil && ctx.Err() == nil:
		a.Log.Error("go release poll", "err", err)
	case n > 0:
		a.Log.Info("go release poll enqueued jobs", "jobs", n)
	}
	return n
}

// DefaultDeliveryTTL is DeliveryTTL's default.
const DefaultDeliveryTTL = time.Hour

// claimDelivery reserves the delivery in the Store's table. fresh is false when it was
// already handled (by this process, an earlier run or another replica); release forgets
// the claim again when queueing failed, so the host's retry is handled. A store without
// the table, or a negative DeliveryTTL, claims nothing and reports fresh. A store error
// fails the delivery (the host retries): dropping it would lose an event.
func (a *App) claimDelivery(ctx context.Context, key string) (fresh bool, release func(), err error) {
	t, ok := a.Queue.Store.(store.DeliveryTable)
	ttl := a.DeliveryTTL
	if !ok || ttl < 0 {
		return true, func() {}, nil
	}
	if ttl == 0 {
		ttl = DefaultDeliveryTTL
	}
	fresh, err = t.ClaimDelivery(ctx, key, a.nowTime(), ttl)
	if err != nil || !fresh {
		return false, nil, err
	}
	return true, func() {
		// The request context may be the reason queueing failed; the release must still run.
		if err := t.ReleaseDelivery(context.WithoutCancel(ctx), key); err != nil {
			a.log().Warn("release delivery", "delivery", key, "err", err)
		}
	}, nil
}

// nowTime reads the queue's clock. Queue.init (idempotent, under the queue's mutex) fills in a
// nil Now, so calling it first orders this read after that write even when a delivery arrives
// while Queue.Run is still starting.
func (a *App) nowTime() time.Time {
	a.Queue.init()
	if a.Queue.Now != nil {
		return a.Queue.Now()
	}
	return time.Now()
}

const (
	deliveryPurgeEvery  = time.Hour
	tombstoneSweepEvery = 24 * time.Hour
)

// housekeep deletes what has expired and nothing reads again: handled delivery ids past
// their ttl (hourly) and removal tombstones past theirs (daily; on DynamoDB that is a
// Scan). Failures are logged; the next period tries again.
func (a *App) housekeep(ctx context.Context) {
	now, engNow := a.nowTime(), time.Now()
	if a.Engine.Now != nil {
		engNow = a.Engine.Now() // the clock tombstones are written and read on
	}
	a.keepMu.Lock()
	doDeliveries := now.Sub(a.lastDeliveryPurge) >= deliveryPurgeEvery
	doSweep := engNow.Sub(a.lastSweep) >= tombstoneSweepEvery
	if doDeliveries {
		a.lastDeliveryPurge = now
	}
	if doSweep {
		a.lastSweep = engNow
	}
	a.keepMu.Unlock()
	if t, ok := a.Engine.Store.(store.DeliveryTable); ok && doDeliveries {
		n, err := t.PurgeDeliveries(ctx, now)
		switch {
		case err != nil && ctx.Err() == nil:
			a.Log.Error("purge deliveries", "err", err)
		case n > 0:
			a.Log.Info("purged expired delivery ids", "ids", n)
		}
	}
	if doSweep {
		n, err := a.Engine.SweepTombstones(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			a.Log.Error("sweep removal tombstones", "err", err)
		case n > 0:
			a.Log.Info("swept removal tombstones", "tombstones", n)
		}
	}
}
