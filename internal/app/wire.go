package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/cratesindex"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/gotool"
	"github.com/taumatix/ripple/internal/npmreg"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/pypi"
	"github.com/taumatix/ripple/internal/registry"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/webhook"
)

// OpenStore opens the backend named by the server configuration.
func OpenStore(ctx context.Context, s *config.Server) (store.Store, error) {
	switch s.Store {
	case "postgres":
		return store.NewPostgres(ctx, s.PostgresDSN, "")
	case "dynamodb":
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("aws config: %w", err)
		}
		d := store.NewDynamo(dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
			if s.DynamoEndpoint != "" {
				o.BaseEndpoint = &s.DynamoEndpoint
			}
		}), s.DynamoTable, store.WithJobShards(s.DynamoJobShards), store.WithDependentShards(s.DynamoDependentShards),
			store.WithQueryConcurrency(s.DynamoQueryConcurrency), store.WithAdaptiveDependents(s.DynamoAdaptiveDependents),
			store.WithCounterStripes(s.DynamoCounterStripes), store.WithScanPaging(s.DynamoScanPage, s.DynamoScanPause))
		if err := d.EnsureTable(ctx); err != nil {
			return nil, err
		}
		return d, nil
	}
	return store.NewMemory(), nil
}

// New wires the production components from the server configuration: every
// registered host that is configured (see RegisterHost), behind one platform.Mux.
//
// With RIPPLE_CONTAINER_CONFINE it also starts the confined container network; the caller
// must call App.Close when it is done with the server (also when it never runs it).
func New(ctx context.Context, s *config.Server, log *slog.Logger) (a *App, st store.Store, err error) {
	configureWebhookSpool(s)
	httpc := &http.Client{Timeout: 30 * time.Second}
	sb, closeSandbox, err := newSandbox(ctx, s, log, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			closeSandbox()
		}
	}()
	hosts, err := BuildHosts(ctx, HostDeps{Server: s, Getenv: os.Getenv, HTTP: httpc, Log: log,
		Git: gitws.Options{WorkDir: s.WorkDir, GoProxy: s.GoProxy, GoSumDB: s.GoSumDB, Sandbox: sb}})
	if err != nil {
		return nil, nil, err
	}
	mux, err := platform.NewMux(github.HostName, hosts...)
	if err != nil {
		return nil, nil, err
	}
	st, err = OpenStore(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	eng := &engine.Engine{Store: st, Platform: mux, Log: log, Workers: s.Workers,
		MaxFiles: s.MaxFiles, MaxDependencies: s.MaxDependencies, MaxBackfill: s.MaxBackfill, BackfillMajors: s.BackfillMajors, PollPackagesPerPoll: s.PollPackagesPerPoll,
		GoCC: s.GoCC, GoToolchainDir: s.GoToolchainDir, GoImages: s.ContainerGoImages, GoProxyUser: s.GoProxyUser, GoProxyToken: s.GoProxyToken,
		Limits: rules.Limits{MinimumReleaseAge: s.MinimumReleaseAge, OpenPRLimit: s.OpenPRLimit, AllowAutomerge: s.AllowAutomerge}}
	if s.GoToolchainDownload {
		// A token is only accepted alongside an explicit RIPPLE_GOPROXY (config checks),
		// so it never reaches the default public proxy.
		eng.GoInstaller = &gotool.Installer{Dir: s.GoToolchainDir, Proxy: s.GoProxy, SumDB: s.GoSumDB, User: s.GoProxyUser, Token: s.GoProxyToken}
	}
	eng.Lookup = &feed.GoProxyLookup{BaseURL: s.GoProxy, User: s.GoProxyUser, Token: s.GoProxyToken,
		UserAgent: "ripple (+https://github.com/taumatix/ripple)"}
	rc := &registry.Client{UserAgent: "ripple (+https://github.com/taumatix/ripple)", HubNewest: s.RegistryHubNewest}
	if s.RegistryAuthFile != "" {
		if rc.Auth, err = registry.LoadDockerConfig(s.RegistryAuthFile); err != nil {
			return nil, nil, fmt.Errorf("RIPPLE_REGISTRY_AUTH_FILE: %w", err)
		}
		for _, w := range rc.Auth.Warnings {
			log.Warn("registry auth file", "note", w)
		}
		log.Info("registry logins loaded", "registries", rc.Auth.Hosts())
	}
	eng.Registry = rc
	eng.Crates = &cratesindex.Client{UserAgent: "ripple (+https://github.com/taumatix/ripple)"}
	eng.PyPI = &pypi.Client{UserAgent: "ripple (+https://github.com/taumatix/ripple)"}
	eng.Npm = &npmreg.Client{UserAgent: "ripple (+https://github.com/taumatix/ripple)"}
	a = &App{
		Engine: eng, Log: log, Secret: []byte(s.WebhookSecret), PollEvery: s.FeedInterval,
		Hosts: hosts, DefaultHost: github.HostName,
		Queue: &Queue{Workers: s.EventWorkers, PerInstallation: s.EventWorkersPerInstallation,
			Backlog: s.EventBacklog, InstallationBacklog: s.EventInstallationBacklog, DeadRetention: s.EventDeadRetention},
		Toolchain: &engine.ToolchainWatcher{Engine: eng, Source: &feed.GoDL{BaseURL: s.GoReleasesURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
		Actions:   &engine.ActionsWatcher{Engine: eng},
		Docker:    &engine.DockerWatcher{Engine: eng},
		Npm:       &engine.NpmWatcher{Engine: eng},
		Cargo:     &engine.CargoWatcher{Engine: eng},
		Pip:       &engine.PipWatcher{Engine: eng},
		Watcher:   &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: s.FeedURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
	}
	a.closers = append(a.closers, closeSandbox)
	wireReplica(eng, a.Queue, s, log)
	if s.OSV {
		eng.Advisories = true
		osvClient := &advisory.Client{APIURL: s.OSVURL, DumpURL: s.OSVDumpURL, HTTP: &http.Client{Timeout: 20 * time.Second}}
		eng.AdvisoryHistory = osvClient
		a.Advisories = &engine.AdvisoryWatcher{Engine: eng, MaxFetch: s.OSVMaxFetch, SweepBatch: s.OSVSweepBatch, BackfillOpenPRs: s.OSVBackfillOpenPRs, Source: osvClient}
		a.AdvisoryEvery = s.OSVInterval
	}
	a.DriftCheckEvery = s.DynamoDriftCheck
	return a, st, nil
}

// wireReplica gives the engine and the event queue one identity and lease TTL, so the
// leases they take on repos and on events name the same replica, and logs it once so the
// name in other lines (a lease taken over, a fenced-out write) can be matched to a process.
func wireReplica(eng *engine.Engine, q *Queue, s *config.Server, log *slog.Logger) {
	id := s.Replica
	if id == "" {
		id = engine.NewReplicaID()
	}
	eng.Replica, eng.LeaseTTL = id, s.LeaseTTL
	q.Owner, q.LeaseTTL = id, s.LeaseTTL
	log.Info("replica", "replica", id, "lease_ttl", s.LeaseTTL.String(), "store", s.Store)
}

// configureWebhookSpool applies RIPPLE_WEBHOOK_DEDUPE_SIGNATURE and RIPPLE_WEBHOOK_SPOOL to every webhook receiver, which all
// read their signed bodies through a webhook.Guard that follows webhook.DefaultSpool.
func configureWebhookSpool(s *config.Server) {
	webhook.DefaultDedupeBySignature = s.WebhookDedupeSignature
	webhook.DefaultSpool = webhook.SpoolDisk
	if s.WebhookSpool == "memory" {
		webhook.DefaultSpool = webhook.SpoolMemory
	}
}
