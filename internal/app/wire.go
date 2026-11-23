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
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/sandbox"
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
			store.WithCounterStripes(s.DynamoCounterStripes))
		if err := d.EnsureTable(ctx); err != nil {
			return nil, err
		}
		return d, nil
	}
	return store.NewMemory(), nil
}

// New wires the production components from the server configuration: every
// registered host that is configured (see RegisterHost), behind one platform.Mux.
func New(ctx context.Context, s *config.Server, log *slog.Logger) (*App, store.Store, error) {
	configureWebhookSpool(s)
	httpc := &http.Client{Timeout: 30 * time.Second}
	sb, err := sandbox.DetectWith(s.Sandbox, nil, sandbox.ContainerOptions{Image: s.ContainerImage,
		CPUs: s.ContainerCPUs, Memory: s.ContainerMemory, Pull: s.ContainerPull, Network: s.ContainerNetwork})
	if err != nil {
		return nil, nil, err
	}
	hosts, err := BuildHosts(ctx, HostDeps{Server: s, Getenv: os.Getenv, HTTP: httpc, Log: log,
		Git: gitws.Options{WorkDir: s.WorkDir, GoProxy: s.GoProxy, Sandbox: sb}})
	if err != nil {
		return nil, nil, err
	}
	mux, err := platform.NewMux(github.HostName, hosts...)
	if err != nil {
		return nil, nil, err
	}
	st, err := OpenStore(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	eng := &engine.Engine{Store: st, Platform: mux, Log: log, Workers: s.Workers,
		MaxFiles: s.MaxFiles, MaxDependencies: s.MaxDependencies,
		GoCC: s.GoCC, GoToolchainDir: s.GoToolchainDir, GoProxyUser: s.GoProxyUser, GoProxyToken: s.GoProxyToken,
		Limits: rules.Limits{MinimumReleaseAge: s.MinimumReleaseAge, OpenPRLimit: s.OpenPRLimit, AllowAutomerge: s.AllowAutomerge}}
	a := &App{
		Engine: eng, Log: log, Secret: []byte(s.WebhookSecret), PollEvery: s.FeedInterval,
		Hosts: hosts, DefaultHost: github.HostName,
		Queue: &Queue{Workers: s.EventWorkers, PerInstallation: s.EventWorkersPerInstallation,
			Backlog: s.EventBacklog, InstallationBacklog: s.EventInstallationBacklog, DeadRetention: s.EventDeadRetention},
		Toolchain: &engine.ToolchainWatcher{Engine: eng, Source: &feed.GoDL{BaseURL: s.GoReleasesURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
		Actions:   &engine.ActionsWatcher{Engine: eng},
		Watcher:   &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: s.FeedURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
	}
	wireReplica(eng, a.Queue, s, log)
	if s.OSV {
		eng.Advisories = true
		osvClient := &advisory.Client{APIURL: s.OSVURL, DumpURL: s.OSVDumpURL, HTTP: &http.Client{Timeout: 20 * time.Second}}
		eng.AdvisoryHistory = osvClient
		a.Advisories = &engine.AdvisoryWatcher{Engine: eng, MaxFetch: s.OSVMaxFetch, Source: osvClient}
		a.AdvisoryEvery = s.OSVInterval
	}
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

// configureWebhookSpool applies RIPPLE_WEBHOOK_SPOOL to every webhook receiver, which all
// read their signed bodies through a webhook.Guard that follows webhook.DefaultSpool.
func configureWebhookSpool(s *config.Server) {
	webhook.DefaultSpool = webhook.SpoolDisk
	if s.WebhookSpool == "memory" {
		webhook.DefaultSpool = webhook.SpoolMemory
	}
}
