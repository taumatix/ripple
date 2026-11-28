//go:build scmreal

package scmmanager_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	mtIndex   = "application/vnd.scmm-index+json;v=2"
	mtPlugins = "application/vnd.scmm-pluginCollection+json;v=2"
	mtAPIKey  = "application/vnd.scmm-apiKey+json;v=2"
	mtMe      = "application/vnd.scmm-me+json;v=2"
)

var (
	bootOnce sync.Once
	bootKey  string
)

// apiKey bootstraps the instance once (the Review and Webhook plugins, installed through the plugin
// API with the restart they need, and an API key for the first administrator) and returns the key.
// It is the headless path ROADMAP 18 asked about; every step is recorded as a FACT.
func (s *scm) apiKey(t *testing.T) string {
	t.Helper()
	bootOnce.Do(func() { bootKey = s.bootstrap(t) })
	if bootKey == "" {
		t.Fatal("bootstrap failed earlier")
	}
	return bootKey
}

func (s *scm) installed(t *testing.T) []string {
	t.Helper()
	code, _, body := s.call(t, http.MethodGet, "/scm/api/v2/plugins/installed", mtPlugins, "", nil)
	var l struct {
		Embedded struct {
			Plugins []struct {
				Name string `json:"name"`
			} `json:"plugins"`
		} `json:"_embedded"`
	}
	if code != http.StatusOK {
		return nil
	}
	_ = json.Unmarshal(body, &l)
	var names []string
	for _, p := range l.Embedded.Plugins {
		names = append(names, p.Name)
	}
	return names
}

func (s *scm) bootstrap(t *testing.T) string {
	t.Helper()
	plugins := []string{"scm-review-plugin", "scm-webhook-plugin"}
	// installWithRestart restarts at once, so a second install in the same breath meets a dead server
	// (recorded: EOF). Queue both with plain install, then execute the pending set once.
	for _, p := range plugins {
		code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/plugins/available/"+p+"/install", "", "", nil)
		t.Logf("FACT install %s without restart: %d location=%q %s", p, code, h.Get("Location"), clip(body))
		if code != http.StatusOK && code != http.StatusAccepted && code != http.StatusNoContent && code != http.StatusCreated {
			t.Fatalf("installing %s: %d %s", p, code, body)
		}
	}
	code, _, body := s.call(t, http.MethodGet, s.base+"/api/v2/plugins/pending", mtPlugins, "", nil)
	t.Logf("FACT pending plugins before the restart: %d %s", code, clip(body))
	var pend struct {
		Links links `json:"_links"`
	}
	_ = json.Unmarshal(body, &pend)
	t.Logf("FACT pending link names: %v", pend.Links.names())
	exec := pend.Links.href("execute")
	if exec == "" {
		t.Fatalf("no execute link on the pending plugins: %s", clip(body))
	}
	code, _, body = s.call(t, http.MethodPost, exec, "", "", nil)
	t.Logf("FACT execute pending plugins: %d %s", code, clip(body))
	// The restart is in-process: wait on the REST index and the installed list, not on a sleep.
	start := time.Now()
	down := 0
	deadline := time.Now().Add(10 * time.Minute)
	for {
		code, _, _ := s.call(t, http.MethodGet, s.base+"/api/v2/", mtIndex, "", nil)
		if code != http.StatusOK {
			down++
		} else {
			have := s.installed(t)
			if slices.Contains(have, plugins[0]) && slices.Contains(have, plugins[1]) {
				t.Logf("FACT after the install the REST index answered non-200 %d times; both plugins installed after %v", down, time.Since(start).Round(time.Second))
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugins not installed after 10 minutes (%d non-200 index answers); installed: %v", down, s.installed(t))
		}
		time.Sleep(2 * time.Second)
	}
	code, _, body = s.call(t, http.MethodGet, s.base+"/api/v2/plugins/pending", mtPlugins, "", nil)
	t.Logf("FACT pending plugins after the restart: %d %s", code, clip(body))

	return s.newKey(t)
}

// newKey creates an API key for the user s authenticates as.
func (s *scm) newKey(t *testing.T) string {
	t.Helper()
	code, h, body := s.call(t, http.MethodPost, s.base+"/api/v2/users/"+s.user+"/api_keys", "", mtAPIKey, map[string]string{"displayName": "ripple", "permissionRole": "WRITE"})
	// Recorded: the 201 body is the key itself, as text/plain, not JSON. Never log it.
	tok := strings.TrimSpace(string(body))
	t.Logf("FACT create api key: %d location=%q content-type=%q body is the token as plain text, %d bytes", code, h.Get("Location"), h.Get("Content-Type"), len(tok))
	if code != http.StatusCreated || tok == "" {
		t.Fatalf("creating the API key: %d (body withheld, %d bytes)", code, len(tok))
	}
	maskSecret(tok)
	return tok
}

// maskSecret asks the Actions runner to redact v from the rest of the job's log. The command carries the
// raw value, so it must never reach stdout (the workflow tees stdout into an uploaded log): it goes to the
// descriptor named by RIPPLE_REAL_MASK_FD, which the workflow opens as the step's real stdout. Unset, as on
// a developer machine, it does nothing.
func maskSecret(v string) {
	if f := maskFile(); f != nil {
		_, _ = fmt.Fprintf(f, "::add-mask::%s\n", v)
	}
}

var (
	maskOnce sync.Once
	maskOut  *os.File
)

// maskFile wraps the descriptor once and keeps it for the life of the process: an *os.File closes its
// descriptor when it is garbage collected, and a closed number is reused by the next socket the tests open.
func maskFile() *os.File {
	maskOnce.Do(func() {
		n, err := strconv.Atoi(os.Getenv("RIPPLE_REAL_MASK_FD"))
		if err != nil || n < 3 {
			return
		}
		maskOut = os.NewFile(uintptr(n), "mask")
	})
	return maskOut
}

// TestRealBootstrapAndKeyUse records how an API key is presented: Bearer header, the cookie the docs
// name, and as a git/basic password.
func TestRealBootstrapAndKeyUse(t *testing.T) {
	s := realSCM(t)
	key := s.apiKey(t)
	try := func(label string, set func(*http.Request)) {
		req, _ := http.NewRequest(http.MethodGet, s.base+"/api/v2/me/", nil)
		req.Header.Set("Accept", mtMe)
		set(req)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			t.Logf("FACT api key as %s: %v", label, err)
			return
		}
		defer resp.Body.Close()
		t.Logf("FACT api key as %s: %d", label, resp.StatusCode)
	}
	try("Authorization: Bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) })
	try("cookie X-Bearer-Token", func(r *http.Request) { r.Header.Set("Cookie", "X-Bearer-Token="+key) })
	try("basic auth, user + key as password", func(r *http.Request) { r.SetBasicAuth(s.user, key) })
	try("basic auth, user ripple + key as password", func(r *http.Request) { r.SetBasicAuth("ripple", key) })
	try("no credentials", func(r *http.Request) {})
}
