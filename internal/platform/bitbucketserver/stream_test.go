package bitbucketserver_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/platform/bitbucketserver"
	"github.com/taumatix/ripple/internal/testutil"
)

// A signed push whose bulk is a field ripple does not read is parsed as it streams: the
// body is not held in memory nor read back from a spool file.
func TestReceiverParsesALargeSignedPushWithoutHoldingIt(t *testing.T) {
	l := &fakeLookup{def: "main", files: []string{"go.mod"}}
	s := &sink{}
	srv := newReceiver(l, s)
	defer srv.Close()
	const size = 4 << 20
	b := refsChanged("ACME/p", "refs/heads/main", from, to, "UPDATE")
	body := strings.Replace(string(b), `{"changes"`, `{"actor":{"note":"`+strings.Repeat("x", size)+`"},"changes"`, 1)
	if body == string(b) {
		body = strings.Replace(string(b), `{`, `{"actor":{"note":"`+strings.Repeat("x", size)+`"},`, 1)
	}
	hdr := map[string]string{"X-Event-Key": "repo:refs_changed", "X-Request-Id": "r1", "X-Hub-Signature": signature(testSecret, body)}
	code, alloc := testutil.PostMeasured(t, srv.URL, hdr, body)
	if code != http.StatusAccepted || len(s.evs) != 1 || !reflect.DeepEqual(s.evs[0].Files, []string{"go.mod"}) ||
		!reflect.DeepEqual(s.evs[0].Repos, []string{"ACME/p"}) || s.evs[0].Ref != "refs/heads/main" {
		t.Fatalf("%d %+v", code, s.evs)
	}
	if alloc > size/3 {
		t.Fatalf("allocated %d KiB handling a %d KiB body", alloc>>10, size>>10)
	}
}

func TestMalformedSignedPushIsDroppedAndOtherEventsAreNotParsed(t *testing.T) {
	s := &sink{}
	srv := newReceiver(&fakeLookup{def: "main"}, s)
	defer srv.Close()
	post := func(key, b string) int {
		hdr := map[string]string{"X-Event-Key": key, "X-Request-Id": "m", "X-Hub-Signature": signature(testSecret, b)}
		c, _ := testutil.PostMeasured(t, srv.URL, hdr, b)
		return c
	}
	if c := post("repo:refs_changed", `{"changes":[`); c != http.StatusAccepted {
		t.Errorf("malformed: %d", c)
	}
	if c := post("pr:opened", `not json`); c != http.StatusAccepted {
		t.Errorf("other event: %d", c)
	}
	if len(s.evs) != 0 {
		t.Errorf("events %+v", s.evs)
	}
}

func signature(secret []byte, body string) string { return bitbucketserver.Sign(secret, []byte(body)) }
