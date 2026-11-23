package github

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func logTo(b *bytes.Buffer) *slog.Logger { return slog.New(slog.NewTextHandler(b, nil)) }

func ghesMeta(t *testing.T, status int, body string) (*Client, *bytes.Buffer, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v3/meta" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("meta is unauthenticated; no credential may be sent")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	var logs bytes.Buffer
	return &Client{APIBase: srv.URL + "/api/v3", Log: logTo(&logs)}, &logs, &hits
}

func TestLogReleaseInfoForSupportedRelease(t *testing.T) {
	c, logs, _ := ghesMeta(t, 200, `{"installed_version":"3.14.2","verifiable_password_authentication":false}`)
	if got := c.LogRelease(context.Background()); got != "3.14.2" {
		t.Fatalf("release %q", got)
	}
	if !strings.Contains(logs.String(), "level=INFO") || !strings.Contains(logs.String(), "3.14.2") || strings.Contains(logs.String(), "WARN") {
		t.Fatalf("log: %s", logs.String())
	}
}

func TestLogReleaseWarnsBelowMinimum(t *testing.T) {
	c, logs, _ := ghesMeta(t, 200, `{"installed_version":"3.8.12"}`)
	c.LogRelease(context.Background())
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), MinGHESRelease) {
		t.Fatalf("want a minimum-version warning, got: %s", logs)
	}
}

func TestLogReleaseFailureIsLoggedNotFatal(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{"404": {404, `nope`}, "bad json": {200, `<html>`}, "no field": {200, `{}`}, "unparsable": {200, `{"installed_version":"nightly"}`}} {
		t.Run(name, func(t *testing.T) {
			c, logs, _ := ghesMeta(t, tc.status, tc.body)
			if got := c.LogRelease(context.Background()); got != "" && name != "unparsable" {
				t.Fatalf("release %q", got)
			}
			if !strings.Contains(logs.String(), "level=WARN") {
				t.Fatalf("want a warning, got: %s", logs)
			}
		})
	}
}

func TestLogReleaseSkipsGitHubDotCom(t *testing.T) {
	c := &Client{APIBase: "https://api.github.com", Log: logTo(&bytes.Buffer{}), HTTP: &http.Client{Transport: failTransport{t}}}
	c.LogRelease(context.Background())
}

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("github.com must not be asked for a GHES release")
	return nil, http.ErrNotSupported
}

func TestLogReleaseUnreachable(t *testing.T) {
	var logs bytes.Buffer
	c := &Client{APIBase: "http://127.0.0.1:1/api/v3", Log: logTo(&logs)}
	c.LogRelease(context.Background())
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("log: %s", logs.String())
	}
}
