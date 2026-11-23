package gerrit_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
)

type sinkRec struct {
	mu  sync.Mutex
	got []platform.Event
	err error
}

func (s *sinkRec) HandleEvent(_ context.Context, e platform.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, e)
	return s.err
}

type filesRec struct {
	files []string
	err   error
	calls [][]string
}

func (f *filesRec) PushFiles(_ context.Context, project, oldRev, newRev string) ([]string, error) {
	f.calls = append(f.calls, []string{project, oldRev, newRev})
	return f.files, f.err
}

func hook(target string, body string, setup func(*http.Request)) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	if setup != nil {
		setup(req)
	}
	return httptest.NewRecorder(), req
}

const (
	oldRev = "1111111111111111111111111111111111111111"
	newRev = "2222222222222222222222222222222222222222"
	zero   = "0000000000000000000000000000000000000000"
)

// refUpdated: the event shape of cmd-stream-events.md ("Ref Updated") with the
// refUpdate attribute of json.md; refName is the full ref name, and a zero newRev
// means the ref was deleted.
func refUpdated(ref, old, head string) string {
	return `{"type":"ref-updated","refUpdate":{"oldRev":"` + old + `","newRev":"` + head + `","refName":"` + ref + `","project":"acme/app"},"eventCreatedOn":1700000000}`
}

func serve(h http.Handler, target, body string, setup func(*http.Request)) *httptest.ResponseRecorder {
	w, req := hook(target, body, setup)
	h.ServeHTTP(w, req)
	return w
}

func TestReceiverRefUpdatedBecomesPushWithResolvedFiles(t *testing.T) {
	sink, files := &sinkRec{}, &filesRec{files: []string{"go.mod"}}
	h := &gerrit.Receiver{Secret: []byte("s3cret"), Sink: sink, Files: files}
	w := serve(h, "/webhook/gerrit?token=s3cret", refUpdated("refs/heads/main", oldRev, newRev), nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	want := []platform.Event{{Kind: platform.Push, Repos: []string{"acme/app"}, Ref: "refs/heads/main", Files: []string{"go.mod"}}}
	if !reflect.DeepEqual(sink.got, want) {
		t.Errorf("events %+v, want %+v", sink.got, want)
	}
	if !reflect.DeepEqual(files.calls, [][]string{{"acme/app", oldRev, newRev}}) {
		t.Errorf("PushFiles calls %v", files.calls)
	}
}

func TestReceiverFileLookupFailureForcesRescan(t *testing.T) {
	sink := &sinkRec{}
	h := &gerrit.Receiver{Secret: []byte("s"), Sink: sink, Files: &filesRec{files: []string{"x"}, err: errors.New("boom")}}
	if w := serve(h, "/?token=s", refUpdated("refs/heads/main", oldRev, newRev), nil); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	if len(sink.got) != 1 || sink.got[0].Files != nil {
		t.Errorf("events %+v, want one push with no files (a re-scan)", sink.got)
	}
}

func TestReceiverAuthenticatesBeforeReadingTheBody(t *testing.T) {
	for name, setup := range map[string]struct {
		target string
		auth   func(*http.Request)
		want   int
	}{
		"token in query":    {"/?token=s3cret", nil, http.StatusAccepted},
		"basic password":    {"/", func(r *http.Request) { r.SetBasicAuth("ripple", "s3cret") }, http.StatusAccepted},
		"wrong token":       {"/?token=nope", nil, http.StatusUnauthorized},
		"no credential":     {"/", nil, http.StatusUnauthorized},
		"wrong basic":       {"/", func(r *http.Request) { r.SetBasicAuth("ripple", "nope") }, http.StatusUnauthorized},
		"empty token":       {"/?token=", nil, http.StatusUnauthorized},
		"prefix of secret":  {"/?token=s3cre", nil, http.StatusUnauthorized},
		"secret and suffix": {"/?token=s3cretX", nil, http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			sink := &sinkRec{}
			h := &gerrit.Receiver{Secret: []byte("s3cret"), Sink: sink}
			// An unreadable body must not matter to a request that fails authentication.
			w, req := hook(setup.target, refUpdated("refs/heads/main", oldRev, newRev), setup.auth)
			req.Body = errReader{}
			h.ServeHTTP(w, req)
			if setup.want == http.StatusUnauthorized && w.Code != setup.want {
				t.Errorf("status %d, want %d", w.Code, setup.want)
			}
			if setup.want == http.StatusUnauthorized && len(sink.got) != 0 {
				t.Errorf("an unauthenticated delivery reached the sink: %+v", sink.got)
			}
			if setup.want == http.StatusAccepted {
				w2 := serve(h, setup.target, refUpdated("refs/heads/main", oldRev, newRev), setup.auth)
				if w2.Code != setup.want {
					t.Errorf("status %d, want %d", w2.Code, setup.want)
				}
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("body must not be read") }
func (errReader) Close() error             { return nil }

func TestReceiverWithoutASecretRefusesEverything(t *testing.T) {
	h := &gerrit.Receiver{Sink: &sinkRec{}}
	if w := serve(h, "/?token=", refUpdated("refs/heads/main", oldRev, newRev), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

func TestReceiverIgnoresRefsThatCannotMoveTheDefaultBranch(t *testing.T) {
	for name, body := range map[string]string{
		"deleted branch": refUpdated("refs/heads/old", oldRev, zero),
		"change ref":     refUpdated("refs/changes/01/1/1", zero, newRev),
		"meta config":    refUpdated("refs/meta/config", oldRev, newRev),
		"tag":            refUpdated("refs/tags/v1", zero, newRev),
		"other event":    `{"type":"comment-added","project":"acme/app"}`,
	} {
		t.Run(name, func(t *testing.T) {
			sink, files := &sinkRec{}, &filesRec{}
			h := &gerrit.Receiver{Secret: []byte("s"), Sink: sink, Files: files}
			if w := serve(h, "/?token=s", body, nil); w.Code != http.StatusAccepted {
				t.Fatalf("status %d", w.Code)
			}
			if len(sink.got) != 0 || len(files.calls) != 0 {
				t.Errorf("events %+v, file lookups %v; want none", sink.got, files.calls)
			}
		})
	}
}

func TestReceiverBatchRefUpdatedYieldsOnePushPerBranch(t *testing.T) {
	sink := &sinkRec{}
	h := &gerrit.Receiver{Secret: []byte("s"), Sink: sink}
	body := `{"type":"batch-ref-updated","refUpdates":[
	  {"oldRev":"` + oldRev + `","newRev":"` + newRev + `","refName":"refs/heads/main","project":"acme/app"},
	  {"oldRev":"` + zero + `","newRev":"` + newRev + `","refName":"refs/changes/02/2/1","project":"acme/app"},
	  {"oldRev":"` + oldRev + `","newRev":"` + newRev + `","refName":"refs/heads/dev","project":"acme/lib"}]}`
	if w := serve(h, "/?token=s", body, nil); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	var got [][2]string
	for _, e := range sink.got {
		got = append(got, [2]string{e.Repos[0], e.Ref})
	}
	if want := [][2]string{{"acme/app", "refs/heads/main"}, {"acme/lib", "refs/heads/dev"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("pushes %v, want %v", got, want)
	}
}

// stream-events.md ("Project Created"): projectName and projectHead.
func TestReceiverProjectCreatedAddsTheRepo(t *testing.T) {
	sink := &sinkRec{}
	h := &gerrit.Receiver{Secret: []byte("s"), Sink: sink}
	body := `{"type":"project-created","projectName":"acme/new","projectHead":"refs/heads/master","eventCreatedOn":1700000000}`
	if w := serve(h, "/?token=s", body, nil); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	if want := []platform.Event{{Kind: platform.ReposAdded, Repos: []string{"acme/new"}}}; !reflect.DeepEqual(sink.got, want) {
		t.Errorf("events %+v, want %+v", sink.got, want)
	}
}

func TestReceiverRefusesBadInput(t *testing.T) {
	h := &gerrit.Receiver{Secret: []byte("s"), Sink: &sinkRec{}}
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"not json":      {"{", http.StatusBadRequest},
		"traversal":     {`{"type":"project-created","projectName":"../etc"}`, http.StatusBadRequest},
		"dot segment":   {`{"type":"ref-updated","refUpdate":{"project":"a/../b","refName":"refs/heads/main","newRev":"` + newRev + `"}}`, http.StatusBadRequest},
		"empty project": {`{"type":"ref-updated","refUpdate":{"project":"","refName":"refs/heads/main","newRev":"` + newRev + `"}}`, http.StatusBadRequest},
		"leading slash": {`{"type":"project-created","projectName":"/abs"}`, http.StatusBadRequest},
		"unknown type":  {`{"type":"x"}`, http.StatusAccepted},
	} {
		t.Run(name, func(t *testing.T) {
			if w := serve(h, "/?token=s", c.body, nil); w.Code != c.want {
				t.Errorf("status %d, want %d", w.Code, c.want)
			}
		})
	}
	w, req := hook("/?token=s", "", nil)
	req.Method = http.MethodGet
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d, want 405", w.Code)
	}
}

func TestReceiverSinkFailureAsksForRedelivery(t *testing.T) {
	h := &gerrit.Receiver{Secret: []byte("s"), Sink: &sinkRec{err: errors.New("queue full")}}
	if w := serve(h, "/?token=s", refUpdated("refs/heads/main", oldRev, newRev), nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
}

func TestReceiverOversizedBodyRefused(t *testing.T) {
	h := &gerrit.Receiver{Secret: []byte("s"), Sink: &sinkRec{}}
	w, req := hook("/?token=s", "", nil)
	req.ContentLength = 2 << 20
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", w.Code)
	}
}
