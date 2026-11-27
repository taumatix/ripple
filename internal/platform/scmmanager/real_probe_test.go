//go:build scmreal

package scmmanager_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// scm is a real SCM-Manager reached with the first administrator's basic auth, used to bootstrap it
// (plugins, API key) the way a user would through the REST API.
type scm struct {
	base, user, password string
}

func realSCM(t *testing.T) *scm {
	t.Helper()
	s := &scm{base: os.Getenv("RIPPLE_REAL_SCM_URL"), user: os.Getenv("RIPPLE_REAL_SCM_ADMIN_USER"), password: os.Getenv("RIPPLE_REAL_SCM_ADMIN_PASSWORD")}
	if s.base == "" || s.user == "" || s.password == "" {
		t.Fatal("RIPPLE_REAL_SCM_URL, RIPPLE_REAL_SCM_ADMIN_USER and RIPPLE_REAL_SCM_ADMIN_PASSWORD must name a real SCM-Manager (hosts-scmmanager workflow)")
	}
	return s
}

// call sends one request and returns status, headers and body.
func (s *scm) call(t *testing.T, method, url, contentType string, body any) (int, http.Header, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	if strings.HasPrefix(url, "/") {
		url = strings.TrimSuffix(s.base, "/scm") + url
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(s.user, s.password)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, []byte(err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func clip(b []byte) string {
	if len(b) > 1500 {
		return string(b[:1500]) + "...(clipped)"
	}
	return string(b)
}

// TestRealProbe records what a fresh SCM-Manager answers to the bootstrap calls (ROADMAP 18).
func TestRealProbe(t *testing.T) {
	s := realSCM(t)
	code, h, body := s.call(t, http.MethodGet, s.base+"/api/v2/", "", nil)
	t.Logf("FACT index: %d %v %s", code, h.Get("Content-Type"), clip(body))
	var idx struct {
		Links map[string]json.RawMessage `json:"_links"`
	}
	_ = json.Unmarshal(body, &idx)
	names := make([]string, 0, len(idx.Links))
	for k := range idx.Links {
		names = append(names, k)
	}
	t.Logf("FACT index link names: %v", names)
	for _, k := range []string{"availablePlugins", "installedPlugins", "pendingPlugins", "me"} {
		if raw, ok := idx.Links[k]; ok {
			t.Logf("FACT link %s: %s", k, raw)
		}
	}
}
