package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
)

func init() { RegisterHost(codecommit.HostName, buildCodeCommit) }

// buildCodeCommit is the AWS CodeCommit host. RIPPLE_CODECOMMIT_REGIONS (comma-separated,
// e.g. us-east-2,eu-west-1) turns it on; without it the host is off. Events arrive from
// SNS, so RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS (comma-separated topic ARNs the receiver
// accepts) is required. Credentials are RIPPLE_CODECOMMIT_ACCESS_KEY_ID and
// RIPPLE_CODECOMMIT_SECRET_ACCESS_KEY (plus RIPPLE_CODECOMMIT_SESSION_TOKEN for temporary
// ones); when the key pair is absent the AWS default chain is used (environment, shared
// config, instance or task role). RIPPLE_CODECOMMIT_ENDPOINT and
// RIPPLE_CODECOMMIT_GIT_ENDPOINT override the API and git endpoints (VPC endpoints).
func buildCodeCommit(ctx context.Context, d HostDeps) (*platform.Host, error) {
	regions := ccList(d.Getenv("RIPPLE_CODECOMMIT_REGIONS"))
	if len(regions) == 0 {
		return nil, nil
	}
	for _, r := range regions {
		if err := codecommit.CheckRegion(r); err != nil {
			return nil, fmt.Errorf("RIPPLE_CODECOMMIT_REGIONS: %w", err)
		}
	}
	topics := ccList(d.Getenv("RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS"))
	if len(topics) == 0 {
		return nil, fmt.Errorf("RIPPLE_CODECOMMIT_REGIONS is set, so RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS is required: without it the webhook would accept nothing")
	}
	var creds aws.CredentialsProvider
	id, secret := d.Getenv("RIPPLE_CODECOMMIT_ACCESS_KEY_ID"), d.Getenv("RIPPLE_CODECOMMIT_SECRET_ACCESS_KEY")
	switch {
	case id != "" && secret != "":
		creds = credentials.NewStaticCredentialsProvider(id, secret, d.Getenv("RIPPLE_CODECOMMIT_SESSION_TOKEN"))
	case id != "" || secret != "":
		return nil, fmt.Errorf("RIPPLE_CODECOMMIT_ACCESS_KEY_ID and RIPPLE_CODECOMMIT_SECRET_ACCESS_KEY go together")
	default:
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(regions[0]))
		if err != nil {
			return nil, fmt.Errorf("codecommit: AWS default credential chain: %w", err)
		}
		creds = cfg.Credentials
	}
	g := d.Git
	c := &codecommit.Client{
		Regions: regions, Creds: creds, HTTP: d.HTTP, Log: d.Log,
		Endpoint: strings.TrimRight(d.Getenv("RIPPLE_CODECOMMIT_ENDPOINT"), "/"),
		GitBase:  strings.TrimRight(d.Getenv("RIPPLE_CODECOMMIT_GIT_ENDPOINT"), "/"),
		WorkDir:  g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		AuthorName: "ripple[bot]", AuthorEmail: "ripple@noreply.invalid",
	}
	return codecommit.NewHost(c, codecommit.Receiver{Topics: topics, HTTP: d.HTTP}, d.Log), nil
}

func ccList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
