package webhook

import (
	"bytes"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// junk is a push body whose bulk is a field ripple does not read.
func junk(size int, tail string) string {
	return `{"ref":"refs/heads/main","installation":{"id":7},"repository":{"id":9,"full_name":"acme/app","default_branch":"main"},` +
		`"head_commit":{"message":"` + strings.Repeat("x", size) + `"},"commits":[{"added":["a.go"],"modified":["go.mod"],"removed":[]}]` + tail + `}`
}

type streamRig struct {
	srv    *httptest.Server
	secret []byte
	events chan Event
}

func newStreamRig(t *testing.T) *streamRig {
	t.Helper()
	rig := &streamRig{secret: []byte("s3cret"), events: make(chan Event, 8)}
	h := &Handler{Secret: rig.secret, Log: slog.New(slog.DiscardHandler),
		Sink: sinkFn(func(e Event) error { rig.events <- e; return nil })}
	rig.srv = httptest.NewServer(h)
	t.Cleanup(rig.srv.Close)
	return rig
}

func (g *streamRig) post(t *testing.T, event, body, sig string) int {
	t.Helper()
	if sig == "" {
		sig = Sign(g.secret, []byte(body))
	}
	req, _ := http.NewRequest("POST", g.srv.URL, strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestLargeSignedBodyIsParsedWithoutBeingHeld(t *testing.T) {
	rig := newStreamRig(t)
	const size = 24 << 20
	body := junk(size, "")
	sig := Sign(rig.secret, []byte(body)) // signed outside the measured window
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if code := rig.post(t, "push", body, sig); code != 202 {
		t.Fatalf("status %d", code)
	}
	runtime.ReadMemStats(&after)
	want := Event{Kind: "push", InstallationID: 7, Repos: []string{"acme/app"}, Ref: "refs/heads/main",
		DefaultBranch: "main", Files: []string{"a.go", "go.mod"}, RepoID: 9}
	if got := <-rig.events; !reflect.DeepEqual(got, want) {
		t.Fatalf("event %+v, want %+v", got, want)
	}
	// Holding the body whole costs at least its size (a file read back, or a buffer), and
	// the test's own body and the HTTP client's copy are made before and outside this
	// window only in part, so compare against a third of it.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > size/3 {
		t.Fatalf("allocated %d MiB handling a %d MiB body", alloc>>20, size>>20)
	}
}

func TestForgedBodyIsRejectedAfterItsParse(t *testing.T) {
	rig := newStreamRig(t)
	body := junk(1000, "")
	wrong := Sign([]byte("other"), []byte(body))
	if code := rig.post(t, "push", body, wrong); code != 401 {
		t.Fatalf("forged body: status %d, want 401", code)
	}
	// A forgery that does not parse is still a 401, never a 400 that reveals the parse.
	if code := rig.post(t, "push", "{not json", Sign([]byte("other"), []byte("{not json"))); code != 401 {
		t.Fatalf("forged garbage: status %d, want 401", code)
	}
	if len(rig.events) != 0 {
		t.Fatal("a forged delivery reached the sink")
	}
}

func TestMalformedSignedBodyIs400AndUnwatchedEventIs202(t *testing.T) {
	rig := newStreamRig(t)
	for name, body := range map[string]string{
		"truncated":        junk(10, "")[:80],
		"trailing garbage": junk(10, "") + "x",
		"wrong type":       `{"ref":5}`,
		"bad number":       `{"installation":{"id":01}}`,
		"too deep":         strings.Repeat(`{"a":`, 100) + "1" + strings.Repeat("}", 100),
	} {
		if code := rig.post(t, "push", body, ""); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	if code := rig.post(t, "ping", "not even json", ""); code != 202 {
		t.Errorf("ping: status %d, want 202", code)
	}
	if len(rig.events) != 0 {
		t.Fatal("a rejected delivery reached the sink")
	}
}

func TestExtractDecodesWhatItKeeps(t *testing.T) {
	ev, err := Parse("push", []byte(`{"ref":"refs\/heads\/é😀","extra":[1,2.5e3,-0,true,null,{"k":"v\"}"}],`+
		`"repository":{"full_name":"a\\b","id":12345678901234},"commits":[null,{"added":["x"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Ref != "refs/heads/é😀" || ev.Repos[0] != `a\b` || ev.RepoID != 12345678901234 || !reflect.DeepEqual(ev.Files, []string{"x"}) {
		t.Fatalf("%+v", ev)
	}
}

func TestExtractRefusesToKeepTooMuch(t *testing.T) {
	var files strings.Builder
	files.WriteString(`{"commits":[{"added":[`)
	for i := 0; i < 5; i++ {
		files.WriteString(`"` + strings.Repeat("f", 4<<20) + `",`)
	}
	files.WriteString(`"z"]}]}`)
	if _, err := Parse("push", []byte(files.String())); err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("err %v", err)
	}
}

func TestReadVerifiedStreamHashesWhatTheConsumerLeavesUnread(t *testing.T) {
	var g Guard
	body := bytes.Repeat([]byte("a"), 3*readChunk)
	for name, tc := range map[string]struct {
		key  []byte
		want ReadResult
	}{"authentic": {[]byte("k"), ReadOK}, "forged": {[]byte("other"), ReadForged}} {
		r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		w := httptest.NewRecorder()
		want := Sign([]byte("k"), body)
		_, res := g.ReadVerifiedStream(w, r, int64(len(body)), tc.key, nil,
			func(sum []byte) bool { return "sha256="+hexOf(sum) == want },
			func(rd io.Reader) error { _, err := rd.Read(make([]byte, 1)); return err }) // stops after one byte
		if res != tc.want {
			t.Errorf("%s: result %v, want %v", name, res, tc.want)
		}
	}
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }
