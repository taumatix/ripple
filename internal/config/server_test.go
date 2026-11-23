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
	if _, err := LoadServer(env(with("RIPPLE_GOPROXY_TOKEN", "tok"))); err != nil {
		t.Errorf("a token with the default https proxy: %v", err)
	}
}
