package sandbox

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGateForwardsOnlyToUpstreamUnderItsSecretPrefix(t *testing.T) {
	var gotPath, gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.RequestURI(), r.Header.Get("Authorization")
		io.WriteString(w, "from upstream")
	}))
	defer up.Close()
	g, err := NewGate(up.URL + "/base")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if !strings.HasPrefix(g.URL, "http://127.0.0.1:") {
		t.Fatalf("gate URL %q is not loopback", g.URL)
	}

	req, _ := http.NewRequest("GET", g.URL+"/mod/@v/list?x=1", nil)
	req.Header.Set("Authorization", "Basic abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "from upstream" || gotPath != "/base/mod/@v/list?x=1" || gotAuth != "Basic abc" {
		t.Errorf("body %q path %q auth %q", b, gotPath, gotAuth)
	}

	// Without the secret prefix another local process gets nothing.
	gotPath = ""
	resp, err = http.Get("http://127.0.0.1:" + strings.Split(g.URL, ":")[2][:strings.Index(strings.Split(g.URL, ":")[2], "/")] + "/mod")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 || gotPath != "" {
		t.Errorf("unprefixed request: status %d, upstream saw %q", resp.StatusCode, gotPath)
	}
}

func TestGateDoesNotFollowRedirectsToOtherHosts(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/x", http.StatusFound)
	}))
	defer up.Close()
	g, _ := NewGate(up.URL)
	defer g.Close()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(g.URL + "/a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status %d, want the 302 handed to the client (which the sandbox then blocks)", resp.StatusCode)
	}
}

func TestGatePort(t *testing.T) {
	g, _ := NewGate("http://example.invalid")
	defer g.Close()
	if g.Port() == 0 || !strings.Contains(g.URL, ":"+strconv.Itoa(g.Port())+"/") {
		t.Errorf("Port %d vs URL %s", g.Port(), g.URL)
	}
}
