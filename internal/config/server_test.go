package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var base = map[string]string{"RIPPLE_WEBHOOK_SECRET": "s", "RIPPLE_GITHUB_APP_ID": "1", "RIPPLE_GITHUB_KEY_FILE": "/k"}

func with(kv ...string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestServerDefaults(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if s.MinimumReleaseAge != 72*time.Hour || s.OpenPRLimit != 5 || !s.AllowAutomerge || s.FeedURL != "https://index.golang.org" || s.Store != "memory" || s.Workers != 4 {
		t.Fatalf("%+v", s)
	}
}

func TestServerMaxBackfill(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.MaxBackfill != 0 {
		t.Fatalf("default must be zero (the engine's own default): %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_MAX_BACKFILL", "25")))
	if err != nil || s.MaxBackfill != 25 {
		t.Fatalf("set: %+v, %v", s, err)
	}
	for _, v := range []string{"0", "-1", "lots"} {
		if _, err := LoadServer(env(with("RIPPLE_MAX_BACKFILL", v))); err == nil {
			t.Errorf("RIPPLE_MAX_BACKFILL=%s accepted", v)
		}
	}
}

func TestServerBackfillMajors(t *testing.T) {
	s, err := LoadServer(env(with("RIPPLE_BACKFILL_MAJORS", "8")))
	if err != nil || s.BackfillMajors != 8 {
		t.Fatalf("set: %+v, %v", s, err)
	}
	for _, v := range []string{"0", "51", "lots"} {
		if _, err := LoadServer(env(with("RIPPLE_BACKFILL_MAJORS", v))); err == nil {
			t.Errorf("RIPPLE_BACKFILL_MAJORS=%s accepted", v)
		}
	}
}

func TestServerPollPackagesPerPoll(t *testing.T) {
	if s, err := LoadServer(env(with("RIPPLE_WORKERS", "4"))); err != nil || s.PollPackagesPerPoll != 0 {
		t.Fatalf("default: %+v, %v", s, err)
	}
	if s, err := LoadServer(env(with("RIPPLE_POLL_PACKAGES_PER_POLL", "200"))); err != nil || s.PollPackagesPerPoll != 200 {
		t.Fatalf("set: %+v, %v", s, err)
	}
	for _, v := range []string{"-1", "lots"} {
		if _, err := LoadServer(env(with("RIPPLE_POLL_PACKAGES_PER_POLL", v))); err == nil {
			t.Errorf("RIPPLE_POLL_PACKAGES_PER_POLL=%s accepted", v)
		}
	}
}

func TestServerScanCaps(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.MaxFiles != DefaultMaxFiles || s.MaxDependencies != DefaultMaxDependencies {
		t.Fatalf("defaults: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_MAX_FILES", "10", "RIPPLE_MAX_DEPENDENCIES", "3")))
	if err != nil || s.MaxFiles != 10 || s.MaxDependencies != 3 {
		t.Fatalf("set: %+v, %v", s, err)
	}
	for _, k := range []string{"RIPPLE_MAX_FILES", "RIPPLE_MAX_DEPENDENCIES"} {
		for _, v := range []string{"0", "-1", "lots"} {
			if _, err := LoadServer(env(with(k, v))); err == nil {
				t.Errorf("%s=%s accepted", k, v)
			}
		}
	}
}

func TestServerRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"missing":          {},
		"http feed":        with("RIPPLE_FEED_URL", "http://index.example.com"),
		"http api":         with("RIPPLE_GITHUB_API_URL", "http://example.com"),
		"url creds":        with("RIPPLE_GITHUB_GIT_URL", "https://u:p@example.com"),
		"bad store":        with("RIPPLE_STORE", "mysql"),
		"pg without dsn":   with("RIPPLE_STORE", "postgres"),
		"bad limit":        with("RIPPLE_OPEN_PR_LIMIT", "0"),
		"bad age":          with("RIPPLE_MINIMUM_RELEASE_AGE", "soon"),
		"bad bool":         with("RIPPLE_ALLOW_AUTOMERGE", "maybe"),
		"zero feed period": with("RIPPLE_FEED_INTERVAL", "0"),
	}
	for name, m := range cases {
		if _, err := LoadServer(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	_, err := LoadServer(env(map[string]string{}))
	for _, k := range []string{"RIPPLE_WEBHOOK_SECRET", "RIPPLE_GITHUB_APP_ID", "RIPPLE_GITHUB_KEY_FILE"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error does not name %s: %v", k, err)
		}
	}
}

func TestServerLoopbackHTTPAllowed(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:9", "http://localhost:80", "http://[::1]:5"} {
		if _, err := LoadServer(env(with("RIPPLE_FEED_URL", u))); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestServerEventQueue(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.EventWorkers != 4 || s.EventWorkersPerInstallation != 2 || s.EventBacklog != 10_000 || s.EventInstallationBacklog != 1_000 {
		t.Fatalf("defaults: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_EVENT_WORKERS", "8", "RIPPLE_EVENT_WORKERS_PER_INSTALLATION", "3",
		"RIPPLE_EVENT_BACKLOG", "50", "RIPPLE_EVENT_INSTALLATION_BACKLOG", "20")))
	if err != nil || s.EventWorkers != 8 || s.EventWorkersPerInstallation != 3 || s.EventBacklog != 50 || s.EventInstallationBacklog != 20 {
		t.Fatalf("set: %+v, %v", s, err)
	}
	for _, k := range []string{"RIPPLE_EVENT_WORKERS", "RIPPLE_EVENT_WORKERS_PER_INSTALLATION", "RIPPLE_EVENT_BACKLOG", "RIPPLE_EVENT_INSTALLATION_BACKLOG"} {
		if _, err := LoadServer(env(with(k, "0"))); err == nil {
			t.Errorf("%s=0 accepted", k)
		}
	}
}

func TestServerEventDeadRetention(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.EventDeadRetention != 30*24*time.Hour {
		t.Fatalf("default: %v, %v", s.EventDeadRetention, err)
	}
	s, err = LoadServer(env(with("RIPPLE_EVENT_DEAD_RETENTION", "7d")))
	if err != nil || s.EventDeadRetention != 7*24*time.Hour {
		t.Fatalf("set: %v, %v", s.EventDeadRetention, err)
	}
	for _, v := range []string{"0", "soon"} {
		if _, err := LoadServer(env(with("RIPPLE_EVENT_DEAD_RETENTION", v))); err == nil {
			t.Errorf("RIPPLE_EVENT_DEAD_RETENTION=%s accepted", v)
		}
	}
}

func TestServerSandbox(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.Sandbox != "none" {
		t.Fatalf("default: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_SANDBOX", "auto")))
	if err != nil || s.Sandbox != "auto" {
		t.Fatalf("set: %+v, %v", s, err)
	}
}

func TestServerContainerSettings(t *testing.T) {
	s, err := LoadServer(env(with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "golang:1.27", "RIPPLE_CONTAINER_CPUS", "1",
		"RIPPLE_CONTAINER_MEMORY", "1G", "RIPPLE_CONTAINER_PULL", "always", "RIPPLE_CONTAINER_NETWORK", "ripple-net")))
	if err != nil || s.ContainerImage != "golang:1.27" || s.ContainerCPUs != "1" || s.ContainerMemory != "1G" ||
		s.ContainerPull != "always" || s.ContainerNetwork != "ripple-net" {
		t.Fatalf("%+v, %v", s, err)
	}
}

func TestServerDynamoShards(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.DynamoJobShards != 8 || s.DynamoDependentShards != 1 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_DYNAMODB_JOB_SHARDS", "16", "RIPPLE_DYNAMODB_DEPENDENT_SHARDS", "4")))
	if err != nil || s.DynamoJobShards != 16 || s.DynamoDependentShards != 4 {
		t.Fatalf("set: %+v %v", s, err)
	}
	for _, v := range []string{"0", "65", "x"} {
		if _, err := LoadServer(env(with("RIPPLE_DYNAMODB_JOB_SHARDS", v))); err == nil {
			t.Errorf("JOB_SHARDS=%s accepted", v)
		}
	}
}

func TestServerDynamoConcurrencyAndAdaptive(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.DynamoQueryConcurrency != 4 || s.DynamoAdaptiveDependents != 0 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_DYNAMODB_QUERY_CONCURRENCY", "1", "RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS", "5000")))
	if err != nil || s.DynamoQueryConcurrency != 1 || s.DynamoAdaptiveDependents != 5000 {
		t.Fatalf("set: %+v %v", s, err)
	}
	for _, kv := range [][2]string{{"RIPPLE_DYNAMODB_QUERY_CONCURRENCY", "0"}, {"RIPPLE_DYNAMODB_QUERY_CONCURRENCY", "65"}, {"RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS", "-1"}, {"RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS", "x"}} {
		if _, err := LoadServer(env(with(kv[0], kv[1]))); err == nil {
			t.Errorf("%s=%s accepted", kv[0], kv[1])
		}
	}
}

func TestServerDynamoCounterStripes(t *testing.T) {
	if s, err := LoadServer(env(base)); err != nil || s.DynamoCounterStripes != 1 {
		t.Fatalf("default: %+v %v", s, err)
	}
	if s, err := LoadServer(env(with("RIPPLE_DYNAMODB_COUNTER_STRIPES", "8"))); err != nil || s.DynamoCounterStripes != 8 {
		t.Fatalf("set: %+v %v", s, err)
	}
	for _, v := range []string{"0", "65", "x"} {
		if _, err := LoadServer(env(with("RIPPLE_DYNAMODB_COUNTER_STRIPES", v))); err == nil {
			t.Errorf("RIPPLE_DYNAMODB_COUNTER_STRIPES=%s accepted", v)
		}
	}
}

func TestServerOSVSweepBatch(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.OSVSweepBatch != 10 {
		t.Fatalf("default: %d %v", s.OSVSweepBatch, err)
	}
	if s, err = LoadServer(env(with("RIPPLE_OSV_SWEEP_BATCH", "3"))); err != nil || s.OSVSweepBatch != 3 {
		t.Fatalf("set: %d %v", s.OSVSweepBatch, err)
	}
	if _, err = LoadServer(env(with("RIPPLE_OSV_SWEEP_BATCH", "0"))); err == nil {
		t.Fatal("0 accepted")
	}
}

func TestServerOSV(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.OSV || s.OSVInterval != 10*time.Minute || s.OSVMaxFetch != 100 || s.OSVURL != "https://api.osv.dev" {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	if s, err = LoadServer(env(with("RIPPLE_OSV", "true"))); err != nil || !s.OSV {
		t.Fatalf("enable: %+v %v", s, err)
	}
	for name, m := range map[string]map[string]string{
		"bad bool":      with("RIPPLE_OSV", "yes please"),
		"http api":      with("RIPPLE_OSV", "true", "RIPPLE_OSV_URL", "http://osv.example.com"),
		"http dump":     with("RIPPLE_OSV", "true", "RIPPLE_OSV_DUMP_URL", "http://osv.example.com"),
		"zero interval": with("RIPPLE_OSV", "true", "RIPPLE_OSV_INTERVAL", "0"),
		"bad max":       with("RIPPLE_OSV_MAX_FETCH", "0"),
	} {
		if _, err := LoadServer(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestServerGoVerifySettings(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.GoProxyUser != "" || s.GoProxyToken != "" || s.GoCC != "" {
		t.Fatalf("defaults: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_GOPROXY", "https://proxy.corp.example.com", "RIPPLE_GOPROXY_USER", "ripple", "RIPPLE_GOPROXY_TOKEN", "tok", "RIPPLE_GO_CC", "gcc")))
	if err != nil || s.GoProxyUser != "ripple" || s.GoProxyToken != "tok" || s.GoCC != "gcc" {
		t.Fatalf("%+v, %v", s, err)
	}
	// A credential is only ever sent to an https proxy (or a loopback one in tests): go
	// itself refuses to pass credentials over plain http, so fail at start-up instead.
	if _, err := LoadServer(env(with("RIPPLE_GOPROXY", "http://proxy.corp.example.com", "RIPPLE_GOPROXY_TOKEN", "tok"))); err == nil {
		t.Error("a token with a plain-http proxy was accepted")
	}
	if _, err := LoadServer(env(with("RIPPLE_GOPROXY_TOKEN", "tok"))); err == nil {
		t.Error("a token with the default public proxy was accepted: it would be sent to proxy.golang.org")
	}
}

func TestGitHubDefaultsAreGitHubDotCom(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.GitHubAPIURL != "https://api.github.com" || s.GitHubGitURL != "https://github.com" || s.GitHubGraphQLURL != "" || s.GitHubAPIVersion != "" {
		t.Fatalf("%+v, %v", s, err)
	}
}

func TestGitHubEnterpriseURLDerivesTheBases(t *testing.T) {
	s, err := LoadServer(env(with("RIPPLE_GITHUB_ENTERPRISE_URL", "https://ghes.example.com/")))
	if err != nil || s.GitHubAPIURL != "https://ghes.example.com/api/v3" || s.GitHubGitURL != "https://ghes.example.com" || s.GitHubGraphQLURL != "https://ghes.example.com/api/graphql" {
		t.Fatalf("%+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_GITHUB_ENTERPRISE_URL", "https://ghes.example.com", "RIPPLE_GITHUB_GIT_URL", "https://git.example.com", "RIPPLE_GITHUB_API_VERSION", "2022-11-28")))
	if err != nil || s.GitHubGitURL != "https://git.example.com" || s.GitHubAPIURL != "https://ghes.example.com/api/v3" || s.GitHubAPIVersion != "2022-11-28" {
		t.Fatalf("explicit variables must win over the derived ones: %+v, %v", s, err)
	}
}

func TestGitHubAPIURLAloneKeepsGraphQLDerivedByTheClient(t *testing.T) {
	s, err := LoadServer(env(with("RIPPLE_GITHUB_API_URL", "https://ghes.example.com/api/v3/", "RIPPLE_GITHUB_GRAPHQL_URL", "https://gql.example.com/graphql")))
	if err != nil || s.GitHubAPIURL != "https://ghes.example.com/api/v3" || s.GitHubGraphQLURL != "https://gql.example.com/graphql" {
		t.Fatalf("%+v, %v", s, err)
	}
}

func TestGitHubURLsAreValidated(t *testing.T) {
	for _, kv := range [][]string{
		{"RIPPLE_GITHUB_ENTERPRISE_URL", "http://ghes.example.com"},
		{"RIPPLE_GITHUB_GRAPHQL_URL", "ftp://x"},
		{"RIPPLE_GITHUB_API_VERSION", "latest"},
	} {
		if _, err := LoadServer(env(with(kv...))); err == nil || !strings.Contains(err.Error(), kv[0]) {
			t.Errorf("%v: err = %v, want one naming %s", kv, err, kv[0])
		}
	}
}

func TestServerReplicaAndLeaseTTL(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.Replica != "" || s.LeaseTTL != 30*time.Second {
		t.Fatalf("defaults: %q %v, %v", s.Replica, s.LeaseTTL, err)
	}
	s, err = LoadServer(env(with("RIPPLE_REPLICA", "ripple-1", "RIPPLE_LEASE_TTL", "45s")))
	if err != nil || s.Replica != "ripple-1" || s.LeaseTTL != 45*time.Second {
		t.Fatalf("set: %q %v, %v", s.Replica, s.LeaseTTL, err)
	}
	for _, v := range []string{"0", "-5s", "soon", "2s"} {
		if _, err := LoadServer(env(with("RIPPLE_LEASE_TTL", v))); err == nil {
			t.Errorf("RIPPLE_LEASE_TTL=%s accepted", v)
		}
	}
	if _, err := LoadServer(env(with("RIPPLE_REPLICA", strings.Repeat("x", 65)))); err == nil {
		t.Error("an over-long RIPPLE_REPLICA was accepted")
	}
}

func TestServerWebhookDedupeSignature(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.WebhookDedupeSignature {
		t.Fatalf("default: %v, %v", s.WebhookDedupeSignature, err)
	}
	if s, err = LoadServer(env(with("RIPPLE_WEBHOOK_DEDUPE_SIGNATURE", "true"))); err != nil || !s.WebhookDedupeSignature {
		t.Fatalf("true: %v, %v", s.WebhookDedupeSignature, err)
	}
	if _, err = LoadServer(env(with("RIPPLE_WEBHOOK_DEDUPE_SIGNATURE", "maybe"))); err == nil {
		t.Fatal("maybe accepted")
	}
}

func TestServerWebhookSpool(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.WebhookSpool != "disk" {
		t.Fatalf("default: %q, %v", s.WebhookSpool, err)
	}
	if s, err = LoadServer(env(with("RIPPLE_WEBHOOK_SPOOL", "memory"))); err != nil || s.WebhookSpool != "memory" {
		t.Fatalf("memory: %q, %v", s.WebhookSpool, err)
	}
	if _, err = LoadServer(env(with("RIPPLE_WEBHOOK_SPOOL", "ram"))); err == nil {
		t.Fatal("RIPPLE_WEBHOOK_SPOOL=ram accepted")
	}
}

func TestServerGoToolchainDir(t *testing.T) {
	if s, err := LoadServer(env(base)); err != nil || s.GoToolchainDir != "" {
		t.Fatalf("default: %+v, %v", s, err)
	}
	dir := t.TempDir()
	if s, err := LoadServer(env(with("RIPPLE_GO_TOOLCHAIN_DIR", dir))); err != nil || s.GoToolchainDir != dir {
		t.Fatalf("%+v, %v", s, err)
	}
	if _, err := LoadServer(env(with("RIPPLE_GO_TOOLCHAIN_DIR", dir, "RIPPLE_SANDBOX", "container", "RIPPLE_CONTAINER_IMAGE", "img"))); err == nil {
		t.Error("accepted beside a container sandbox, which would ignore it")
	}
	for name, v := range map[string]string{"relative": "toolchains", "missing": dir + "/nope"} {
		if _, err := LoadServer(env(with("RIPPLE_GO_TOOLCHAIN_DIR", v))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestServerContainerConfine(t *testing.T) {
	ok := with("RIPPLE_SANDBOX", "podman", "RIPPLE_CONTAINER_IMAGE", "img", "RIPPLE_CONTAINER_CONFINE", "true")
	s, err := LoadServer(env(ok))
	if err != nil || !s.ContainerConfine {
		t.Fatalf("%+v, %v", s, err)
	}
	if s, err = LoadServer(env(base)); err != nil || s.ContainerConfine {
		t.Fatalf("default must be off: %+v, %v", s, err)
	}
	bad := map[string]map[string]string{
		"not a bool":      with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "i", "RIPPLE_CONTAINER_CONFINE", "yes please"),
		"no sandbox":      with("RIPPLE_CONTAINER_CONFINE", "true"),
		"apple container": with("RIPPLE_SANDBOX", "container", "RIPPLE_CONTAINER_IMAGE", "i", "RIPPLE_CONTAINER_CONFINE", "true"),
		"bwrap":           with("RIPPLE_SANDBOX", "bwrap", "RIPPLE_CONTAINER_CONFINE", "true"),
		"own network":     with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "i", "RIPPLE_CONTAINER_CONFINE", "true", "RIPPLE_CONTAINER_NETWORK", "n"),
		"proxy path":      with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "i", "RIPPLE_CONTAINER_CONFINE", "true", "RIPPLE_GOPROXY", "https://p.example/go"),
		"credential":      with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "i", "RIPPLE_CONTAINER_CONFINE", "true", "RIPPLE_GOPROXY", "https://p.example", "RIPPLE_GOPROXY_TOKEN", "t"),
	}
	for name, m := range bad {
		if _, err := LoadServer(env(m)); err == nil || !strings.Contains(err.Error(), "RIPPLE_CONTAINER_CONFINE") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestActionsUpstream(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.ActionsUpstreamURL != "" || s.ActionsUpstreamToken != "" {
		t.Fatalf("off by default: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_ACTIONS_UPSTREAM_URL", "https://api.github.com/", "RIPPLE_ACTIONS_UPSTREAM_TOKEN", "t")))
	if err != nil || s.ActionsUpstreamURL != "https://api.github.com" || s.ActionsUpstreamToken != "t" {
		t.Fatalf("%+v, %v", s, err)
	}
	for name, m := range map[string]map[string]string{
		"plain http":    with("RIPPLE_ACTIONS_UPSTREAM_URL", "http://api.example.com"),
		"token no url":  with("RIPPLE_ACTIONS_UPSTREAM_TOKEN", "t"),
		"url with user": with("RIPPLE_ACTIONS_UPSTREAM_URL", "https://u:p@api.github.com"),
	} {
		if _, err := LoadServer(env(m)); err == nil || !strings.Contains(err.Error(), "RIPPLE_ACTIONS_UPSTREAM") {
			t.Errorf("%s: want an error naming the variable, got %v", name, err)
		}
	}
}

func TestLoadServerRegistryAuthFile(t *testing.T) {
	s, err := LoadServer(env(with("RIPPLE_REGISTRY_AUTH_FILE", "/etc/ripple/registry.json")))
	if err != nil || s.RegistryAuthFile != "/etc/ripple/registry.json" {
		t.Fatalf("%q %v", s.RegistryAuthFile, err)
	}
	if s, err = LoadServer(env(with())); err != nil || s.RegistryAuthFile != "" {
		t.Errorf("default is anonymous: %q %v", s.RegistryAuthFile, err)
	}
}

func TestServerContainerGoImages(t *testing.T) {
	ok := with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "img", "RIPPLE_CONTAINER_GO_IMAGES", "1.25=r:1.25, 1.26=r:1.26")
	s, err := LoadServer(env(ok))
	if err != nil || s.ContainerGoImages["1.25"] != "r:1.25" || s.ContainerGoImages["1.26"] != "r:1.26" || len(s.ContainerGoImages) != 2 {
		t.Fatalf("%+v, %v", s.ContainerGoImages, err)
	}
	for name, v := range map[string]string{"no minor": "r:1", "patch": "1.26.1=r", "empty image": "1.26=", "twice": "1.26=a,1.26=b", "option": "1.26=--privileged"} {
		if _, err := LoadServer(env(with("RIPPLE_SANDBOX", "docker", "RIPPLE_CONTAINER_IMAGE", "img", "RIPPLE_CONTAINER_GO_IMAGES", v))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := LoadServer(env(with("RIPPLE_CONTAINER_GO_IMAGES", "1.26=r"))); err == nil {
		t.Error("accepted without a container sandbox")
	}
}

func TestServerDynamoScanPacingAndDriftCheck(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.DynamoScanPage != 0 || s.DynamoScanPause != 0 || s.DynamoDriftCheck != 0 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_STORE", "dynamodb", "RIPPLE_DYNAMODB_SCAN_PAGE", "100", "RIPPLE_DYNAMODB_SCAN_PAUSE_MS", "250", "RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL", "6h")))
	if err != nil || s.DynamoScanPage != 100 || s.DynamoScanPause != 250*time.Millisecond || s.DynamoDriftCheck != 6*time.Hour {
		t.Fatalf("set: %+v %v", s, err)
	}
	for _, kv := range [][2]string{{"RIPPLE_DYNAMODB_SCAN_PAGE", "-1"}, {"RIPPLE_DYNAMODB_SCAN_PAUSE_MS", "x"}, {"RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL", "soon"}} {
		if _, err := LoadServer(env(with("RIPPLE_STORE", "dynamodb", kv[0], kv[1]))); err == nil {
			t.Errorf("%s=%s accepted", kv[0], kv[1])
		}
	}
	if _, err := LoadServer(env(with("RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL", "1h"))); err == nil {
		t.Error("a drift check on the memory store was accepted")
	}
}

func TestServerGoToolchainDownload(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadServer(env(base))
	if err != nil || s.GoToolchainDownload {
		t.Fatalf("default: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_GO_TOOLCHAIN_DIR", dir, "RIPPLE_GO_TOOLCHAIN_DOWNLOAD", "true")))
	if err != nil || !s.GoToolchainDownload {
		t.Fatalf("set: %+v, %v", s, err)
	}
	if _, err = LoadServer(env(with("RIPPLE_GO_TOOLCHAIN_DOWNLOAD", "true"))); err == nil {
		t.Fatal("download without a directory must be rejected")
	}
	if _, err = LoadServer(env(with("RIPPLE_GO_TOOLCHAIN_DIR", dir, "RIPPLE_GO_TOOLCHAIN_DOWNLOAD", "maybe"))); err == nil {
		t.Fatal("a non-boolean must be rejected")
	}
}

func TestServerRegistryHubNewest(t *testing.T) {
	s, err := LoadServer(env(base))
	if err != nil || s.RegistryHubNewest {
		t.Fatalf("default: %+v, %v", s, err)
	}
	s, err = LoadServer(env(with("RIPPLE_REGISTRY_HUB_NEWEST", "true")))
	if err != nil || !s.RegistryHubNewest {
		t.Fatalf("set: %+v, %v", s, err)
	}
	if _, err = LoadServer(env(with("RIPPLE_REGISTRY_HUB_NEWEST", "maybe"))); err == nil {
		t.Fatal("a non-boolean must be rejected")
	}
}
