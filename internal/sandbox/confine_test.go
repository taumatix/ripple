package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type recorded struct {
	calls [][]string
	fail  func(args []string) error
}

func (r *recorded) run(_ context.Context, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	if r.fail != nil {
		return "", r.fail(args)
	}
	return "", nil
}

func (r *recorded) has(words ...string) bool {
	for _, c := range r.calls {
		if has(c, words...) {
			return true
		}
	}
	return false
}

func confineOpts() ConfineOptions {
	return ConfineOptions{Name: "rn", Upstream: "https://proxy.golang.org"}
}

func TestConfineCreatesInternalNetworkAndSidecar(t *testing.T) {
	r := &recorded{}
	c, err := confine(context.Background(), FlavourDocker, confineOpts(), r.run)
	if err != nil {
		t.Fatal(err)
	}
	if c.Network != "rn" || c.ProxyURL != "http://rn-proxy:3128" {
		t.Errorf("handle = %+v", c)
	}
	for _, want := range [][]string{
		{"network", "create", "--internal", "--label", "ripple.job=1", "rn"},
		{"run", "-d", "--name", "rn-proxy", "--label", "ripple.job=1"},
		{"network", "connect", "rn", "rn-proxy"},
		{"exec", "rn-proxy", "wget"},
	} {
		if !r.has(want...) {
			t.Errorf("no call with %v in %v", want, r.calls)
		}
	}
	// the sidecar must start on the default network (so it can reach upstream) and only
	// then be joined to the internal one, not the other way round
	if idx := indexOfCall(r, "network", "connect"); idx < indexOfCall(r, "run", "-d") {
		t.Error("network connect before the sidecar exists")
	}
}

func indexOfCall(r *recorded, words ...string) int {
	for i, c := range r.calls {
		if has(c, words...) {
			return i
		}
	}
	return -1
}

func TestConfineConfigCarriesUpstreamHost(t *testing.T) {
	r := &recorded{}
	if _, err := confine(context.Background(), FlavourPodman, confineOpts(), r.run); err != nil {
		t.Fatal(err)
	}
	var all string
	for _, c := range r.calls {
		all += strings.Join(c, " ") + "\n"
	}
	for _, want := range []string{"proxy_pass https://proxy.golang.org;", "proxy_set_header Host proxy.golang.org;", "listen 3128;"} {
		if !strings.Contains(all, want) {
			t.Errorf("config lacks %q:\n%s", want, all)
		}
	}
}

func TestConfineRejectsBadInput(t *testing.T) {
	for name, o := range map[string]ConfineOptions{
		"no upstream":      {Name: "rn"},
		"path in upstream": {Name: "rn", Upstream: "https://p.example/x"},
		"config injection": {Name: "rn", Upstream: "https://p.example;}evil{"},
		"bad scheme":       {Name: "rn", Upstream: "ftp://p.example"},
		"bad name":         {Name: "r n", Upstream: "https://p.example"},
	} {
		if _, err := confine(context.Background(), FlavourDocker, o, (&recorded{}).run); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := confine(context.Background(), FlavourApple, confineOpts(), (&recorded{}).run); err == nil {
		t.Error("Apple container accepted: it has no internal networks")
	}
}

func TestConfineCleansUpOnFailure(t *testing.T) {
	r := &recorded{fail: func(a []string) error {
		if has(a, "network", "connect") {
			return errors.New("boom")
		}
		return nil
	}}
	if _, err := confine(context.Background(), FlavourDocker, confineOpts(), r.run); err == nil {
		t.Fatal("no error")
	}
	if !r.has("rm", "-f", "rn-proxy") || !r.has("network", "rm", "rn") {
		t.Errorf("not cleaned up: %v", r.calls)
	}
}

func TestConfineClose(t *testing.T) {
	r := &recorded{}
	c, err := confine(context.Background(), FlavourDocker, confineOpts(), r.run)
	if err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !r.has("rm", "-f", "rn-proxy") || !r.has("network", "rm", "rn") {
		t.Errorf("close calls: %v", r.calls)
	}
}
