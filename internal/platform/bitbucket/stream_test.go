package bitbucket_test

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucket"
	"github.com/taumatix/ripple/internal/testutil"
)

// A signed push whose bulk is a field ripple does not read is parsed as it streams: the
// body is not held in memory nor read back from a spool file.
func TestReceiverParsesALargeSignedPushWithoutHoldingIt(t *testing.T) {
	secret := []byte("s3cret")
	sink, files := &sinkRec{}, &filesRec{files: []string{"go.mod"}}
	srv := httptest.NewServer(&bitbucket.Receiver{Secret: secret, Sink: sink, Files: files})
	defer srv.Close()
	const size = 4 << 20
	m := pushBody("acme/app", "main", "aaaaaaa", "bbbbbbb")
	m["actor"] = map[string]any{"note": strings.Repeat("x", size)}
	b := body(t, m)
	hdr := map[string]string{"X-Event-Key": "repo:push", "X-Request-UUID": "uuid-1", "X-Hub-Signature": bitbucket.Sign(secret, b)}
	code, alloc := testutil.PostMeasured(t, srv.URL, hdr, string(b))
	want := []platform.Event{{Kind: platform.Push, Delivery: "uuid-1-0", Repos: []string{"acme/app"}, Ref: "refs/heads/main", Files: []string{"go.mod"}}}
	if code != 202 || !reflect.DeepEqual(sink.got, want) {
		t.Fatalf("%d %+v", code, sink.got)
	}
	if wantCalls := [][]string{{"acme/app", "main", "bbbbbbb"}}; !reflect.DeepEqual(files.calls, wantCalls) {
		t.Errorf("file lookups = %v", files.calls)
	}
	if alloc > size/3 {
		t.Fatalf("allocated %d KiB handling a %d KiB body", alloc>>10, size>>10)
	}
}

func TestReceiverAnswers400ToAMalformedSignedPushAndIgnoresOtherEventsUnparsed(t *testing.T) {
	secret := []byte("s3cret")
	sink := &sinkRec{}
	srv := httptest.NewServer(&bitbucket.Receiver{Secret: secret, Sink: sink})
	defer srv.Close()
	post := func(key, b string) int {
		hdr := map[string]string{"X-Event-Key": key, "X-Request-UUID": "u", "X-Hub-Signature": bitbucket.Sign(secret, []byte(b))}
		c, _ := testutil.PostMeasured(t, srv.URL, hdr, b)
		return c
	}
	if c := post("repo:push", `{"push":{"changes":[`); c != 400 {
		t.Errorf("malformed: %d", c)
	}
	if c := post("pullrequest:created", `not json`); c != 202 {
		t.Errorf("other event: %d", c)
	}
	if len(sink.got) != 0 {
		t.Errorf("events %+v", sink.got)
	}
}
