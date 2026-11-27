//go:build scmreal

package scmmanager_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
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

// call sends one request. accept and contentType are SCM-Manager's versioned media types: it
// answers 406 to a plain application/json (recorded in docs/vendor/scm-manager/real-answers.md).
func (s *scm) call(t *testing.T, method, url, accept, contentType string, body any) (int, http.Header, []byte) {
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
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
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

type links map[string]json.RawMessage

func (l links) names() []string {
	var n []string
	for k := range l {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// href is the href of a named link, whether it is an object or the first of a list.
func (l links) href(name string) string {
	raw, ok := l[name]
	if !ok {
		return ""
	}
	var one struct {
		Href string `json:"href"`
	}
	if json.Unmarshal(raw, &one) == nil && one.Href != "" {
		return one.Href
	}
	var many []struct {
		Href string `json:"href"`
	}
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return many[0].Href
	}
	return ""
}

// TestRealProbe records what a fresh SCM-Manager answers to the bootstrap calls (ROADMAP 18):
// the index, the plugin catalogue and the links that install a plugin and create an API key.
func TestRealProbe(t *testing.T) {
	s := realSCM(t)
	code, h, body := s.call(t, http.MethodGet, s.base+"/api/v2/", "application/vnd.scmm-index+json;v=2", "", nil)
	t.Logf("FACT index: %d %v %s", code, h.Get("Content-Type"), clip(body))
	var idx struct {
		Links links `json:"_links"`
	}
	_ = json.Unmarshal(body, &idx)
	t.Logf("FACT index link names: %v", idx.Links.names())
	for _, k := range []string{"availablePlugins", "installedPlugins", "pendingPlugins", "me", "repositories", "users"} {
		if raw, ok := idx.Links[k]; ok {
			t.Logf("FACT link %s: %s", k, raw)
		}
	}
	if href := idx.Links.href("availablePlugins"); href != "" {
		code, h, body = s.call(t, http.MethodGet, href, "application/vnd.scmm-pluginCollection+json;v=2", "", nil)
		t.Logf("FACT availablePlugins: %d %v %s", code, h.Get("Content-Type"), clip(body))
		var cat struct {
			Embedded struct {
				Plugins []struct {
					Name  string `json:"name"`
					Links links  `json:"_links"`
				} `json:"plugins"`
			} `json:"_embedded"`
		}
		_ = json.Unmarshal(body, &cat)
		for _, p := range cat.Embedded.Plugins {
			if p.Name == "scm-review-plugin" || p.Name == "scm-webhook-plugin" {
				t.Logf("FACT plugin %s links: %v install=%s", p.Name, p.Links.names(), p.Links.href("install"))
			}
		}
	}
	if href := idx.Links.href("me"); href != "" {
		code, h, body = s.call(t, http.MethodGet, href, "application/vnd.scmm-me+json;v=2", "", nil)
		t.Logf("FACT me: %d %v %s", code, h.Get("Content-Type"), clip(body))
	}
}
