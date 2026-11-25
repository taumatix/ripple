package gitlab_test

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/testutil"
)

// A signed push whose bulk is a field ripple does not read is parsed as it streams: the
// body is not held in memory nor read back from a spool file.
func TestSignedPushIsParsedWithoutBeingHeld(t *testing.T) {
	key, _ := gitlab.ParseSigningToken(vecToken)
	now := time.Unix(1700000000, 0)
	sink := &sinkRec{}
	srv := httptest.NewServer(gitlab.NewHost(&gitlab.Client{}, gitlab.Receiver{SigningKey: key, Now: func() time.Time { return now }},
		slog.New(slog.DiscardHandler)).Receiver(sink))
	defer srv.Close()
	const size = 4 << 20
	body := strings.Replace(pushBody, `"event_name":"push",`, `"event_name":"push","note":"`+strings.Repeat("x", size)+`",`, 1)
	hdr := map[string]string{"X-Gitlab-Event": "Push Hook", "webhook-id": vecID, "webhook-timestamp": vecTS,
		"webhook-signature": gitlab.Signature(key, vecID, vecTS, []byte(body))}
	code, alloc := testutil.PostMeasured(t, srv.URL, hdr, body)
	evs := sink.events()
	if code != 202 || len(evs) != 1 || strings.Join(evs[0].Files, ",") != "CHANGELOG,app/controller/application.rb,go.mod,old.txt" ||
		evs[0].Repos[0] != "mike/sub/diaspora" || evs[0].DefaultBranch != "master" {
		t.Fatalf("%d %+v", code, evs)
	}
	if alloc > size/3 {
		t.Fatalf("allocated %d KiB handling a %d KiB body", alloc>>10, size>>10)
	}
}

func TestSignedMalformedPushIs400AndOtherEventsAreNotParsed(t *testing.T) {
	key, _ := gitlab.ParseSigningToken(vecToken)
	now := time.Unix(1700000000, 0)
	sink := &sinkRec{}
	srv := httptest.NewServer(gitlab.NewHost(&gitlab.Client{}, gitlab.Receiver{SigningKey: key, Now: func() time.Time { return now }},
		slog.New(slog.DiscardHandler)).Receiver(sink))
	defer srv.Close()
	post := func(event, body string) int {
		hdr := map[string]string{"X-Gitlab-Event": event, "webhook-id": vecID, "webhook-timestamp": vecTS,
			"webhook-signature": gitlab.Signature(key, vecID, vecTS, []byte(body))}
		code, _ := testutil.PostMeasured(t, srv.URL, hdr, body)
		return code
	}
	if c := post("Push Hook", `{"object_kind":"push",`); c != 400 {
		t.Errorf("malformed push: %d", c)
	}
	if c := post("Issue Hook", `not json`); c != 202 {
		t.Errorf("other event: %d", c)
	}
	if len(sink.events()) != 0 {
		t.Errorf("events %+v", sink.events())
	}
}

// A push authenticated by the plain secret token is parsed as it streams too, not read
// whole first: a large field ripple does not read is never held.
func TestSecretTokenPushIsParsedWithoutBeingHeld(t *testing.T) {
	sink := &sinkRec{}
	srv := httptest.NewServer(gitlab.NewHost(&gitlab.Client{}, gitlab.Receiver{SecretToken: "s3cret"},
		slog.New(slog.DiscardHandler)).Receiver(sink))
	defer srv.Close()
	const size = 4 << 20
	body := strings.Replace(pushBody, `"event_name":"push",`, `"event_name":"push","note":"`+strings.Repeat("x", size)+`",`, 1)
	hdr := map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "s3cret", "webhook-id": "id-1"}
	code, alloc := testutil.PostMeasured(t, srv.URL, hdr, body)
	evs := sink.events()
	if code != 202 || len(evs) != 1 || evs[0].Repos[0] != "mike/sub/diaspora" || len(evs[0].Files) != 4 {
		t.Fatalf("%d %+v", code, evs)
	}
	if alloc > size/3 {
		t.Fatalf("allocated %d KiB handling a %d KiB body", alloc>>10, size>>10)
	}
}

func TestSecretTokenMalformedPushIs400(t *testing.T) {
	sink := &sinkRec{}
	srv := httptest.NewServer(gitlab.NewHost(&gitlab.Client{}, gitlab.Receiver{SecretToken: "s3cret"},
		slog.New(slog.DiscardHandler)).Receiver(sink))
	defer srv.Close()
	hdr := map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "s3cret"}
	if code, _ := testutil.PostMeasured(t, srv.URL, hdr, `{"object_kind":"push",`); code != 400 {
		t.Errorf("malformed: %d", code)
	}
}
