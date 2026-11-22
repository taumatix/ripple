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

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/rules"
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
		}), s.DynamoTable)
		if err := d.EnsureTable(ctx); err != nil {
			return nil, err
		}
		return d, nil
	}
	return store.NewMemory(), nil
}

// New wires the production components from the server configuration.
func New(ctx context.Context, s *config.Server, log *slog.Logger) (*App, store.Store, error) {
	pem, err := os.ReadFile(s.GitHubKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read github key: %w", err)
	}
	key, err := github.ParsePrivateKey(pem)
	if err != nil {
		return nil, nil, err
	}
	st, err := OpenStore(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	httpc := &http.Client{Timeout: 30 * time.Second}
	gh := &github.Client{
		APIBase: s.GitHubAPIURL, GitBase: s.GitHubGitURL, HTTP: httpc, Log: log, WorkDir: s.WorkDir, GoProxy: s.GoProxy,
		Tokens:     &github.AppAuth{AppID: s.GitHubAppID, Key: key, APIBase: s.GitHubAPIURL, HTTP: httpc},
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@users.noreply.github.com",
	}
	eng := &engine.Engine{Store: st, Platform: gh, Log: log, Workers: s.Workers,
		MaxFiles: s.MaxFiles, MaxDependencies: s.MaxDependencies,
		Limits: rules.Limits{MinimumReleaseAge: s.MinimumReleaseAge, OpenPRLimit: s.OpenPRLimit, AllowAutomerge: s.AllowAutomerge}}
	return &App{
		Engine: eng, Log: log, Secret: []byte(s.WebhookSecret), PollEvery: s.FeedInterval,
		Queue: &Queue{Workers: s.EventWorkers, PerInstallation: s.EventWorkersPerInstallation,
			Backlog: s.EventBacklog, InstallationBacklog: s.EventInstallationBacklog},
		Watcher: &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: s.FeedURL, UserAgent: "ripple (+https://github.com/taumatix/ripple)"}},
	}, st, nil
}
