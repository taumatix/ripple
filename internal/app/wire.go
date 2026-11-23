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
		}), s.DynamoTable, store.WithJobShards(s.DynamoJobShards), store.WithDependentShards(s.DynamoDependentShards))
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
		Limits: rules.Limits{MinimumReleaseAge: s.MinimumReleaseAge, OpenPRLimit: s.OpenPRLimit, AllowAutomerge: s.AllowAutomerge}}
	a := &App{
		Engine: eng, Log: log, Secret: []byte(s.WebhookSecret), PollEvery: s.FeedInterval,
		Hosts: hosts, DefaultHost: github.HostName,
		Queue: &Queue{Workers: s.EventWorkers, PerInstallation: s.EventWorkersPerInstallation,
			Backlog: s.EventBacklog, InstallationBacklog: s.EventInstallationBacklog},
		Toolchain: &engine.ToolchainWatcher{Engine: eng, Source: &feed.GoDL{BaseURL: s.GoReleasesURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
		Watcher:   &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: s.FeedURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
	}
	if s.OSV {
		eng.Advisories = true
		a.Advisories = &engine.AdvisoryWatcher{Engine: eng, MaxFetch: s.OSVMaxFetch,
			Source: &advisory.Client{APIURL: s.OSVURL, DumpURL: s.OSVDumpURL, HTTP: &http.Client{Timeout: 20 * time.Second}}}
		a.AdvisoryEvery = s.OSVInterval
	}
	return a, st, nil
}
