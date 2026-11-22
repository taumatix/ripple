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
