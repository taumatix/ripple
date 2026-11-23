package azuredevops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
)

type sink struct {
	events []platform.Event
	err    error
}

func (s *sink) HandleEvent(_ context.Context, e platform.Event) error {
	s.events = append(s.events, e)
	return s.err
}

// A trimmed git.push payload from service-hooks-events.md ("git.push", sample payload).
const pushBody = `{"id":"a0a0a0a0-bbbb-cccc-dddd-e1e1e1e1e1e1","eventType":"git.push","publisherId":"tfs",
"resource":{"refUpdates":[{"name":"refs/heads/main","oldObjectId":"d3d3","newObjectId":"e4e4"},{"name":"refs/tags/v1","oldObjectId":"0","newObjectId":"e4e4"}],
"repository":{"id":"f5f5","name":"Fabrikam-Fiber-Git","defaultBranch":"refs/heads/main","project":{"id":"a6a6","name":"Fabrikam"}}}}`

func post(h http.Handler, user, pass, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/webhook/azuredevops", strings.NewReader(body))
	if user != "" || pass != "" {
		r.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func newReceiver(s *sink) *Receiver {
	return &Receiver{Org: "fabrikam-fiber-inc", Auth: BasicAuth{Username: "ripple", Password: "s3cret"}, Sink: s}
}

func TestPushBecomesOneEventPerBranchUpdate(t *testing.T) {
	s := &sink{}
	if w := post(newReceiver(s), "ripple", "s3cret", pushBody); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	if len(s.events) != 1 {
		t.Fatalf("events = %+v (tag update must be ignored)", s.events)
	}
	e := s.events[0]
	if e.Kind != platform.Push || e.Ref != "refs/heads/main" || e.DefaultBranch != "main" || len(e.Files) != 0 ||
		len(e.Repos) != 1 || e.Repos[0] != "fabrikam-fiber-inc/Fabrikam/Fabrikam-Fiber-Git" ||
		e.Delivery != "a0a0a0a0-bbbb-cccc-dddd-e1e1e1e1e1e1#refs/heads/main" {
		t.Errorf("event = %+v", e)
	}
}

func TestUnauthenticatedDeliveriesAreRefusedBeforeTheBodyIsRead(t *testing.T) {
	for name, c := range map[string][2]string{"none": {"", ""}, "wrong password": {"ripple", "nope"}, "wrong user": {"mallory", "s3cret"}} {
		s := &sink{}
		if w := post(newReceiver(s), c[0], c[1], pushBody); w.Code != http.StatusUnauthorized || len(s.events) != 0 {
			t.Errorf("%s: status %d, events %d", name, w.Code, len(s.events))
		}
	}
	// No configured password means no delivery is ever accepted, even an empty one.
	r := &Receiver{Org: "o", Sink: &sink{}}
	if w := post(r, "", "", pushBody); w.Code != http.StatusUnauthorized {
		t.Errorf("empty config accepted: %d", w.Code)
	}
}

func TestOtherEventsAndUnsupportedNamesAreAcknowledgedAndIgnored(t *testing.T) {
	s := &sink{}
	h := newReceiver(s)
	if w := post(h, "ripple", "s3cret", `{"eventType":"git.pullrequest.created"}`); w.Code != http.StatusAccepted {
		t.Errorf("other event: %d", w.Code)
	}
	space := strings.Replace(pushBody, `"name":"Fabrikam"`, `"name":"Has Space"`, 1)
	if w := post(h, "ripple", "s3cret", space); w.Code != http.StatusAccepted {
		t.Errorf("unsupported name: %d", w.Code)
	}
	if len(s.events) != 0 {
		t.Errorf("events = %+v", s.events)
	}
}

func TestMalformedAndOversizedBodies(t *testing.T) {
	h := newReceiver(&sink{})
	if w := post(h, "ripple", "s3cret", "{nope"); w.Code != http.StatusBadRequest {
		t.Errorf("malformed: %d", w.Code)
	}
	big := `{"x":"` + strings.Repeat("a", maxBody) + `"}`
	if w := post(h, "ripple", "s3cret", big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}

func TestSinkFailureAsksForRedelivery(t *testing.T) {
	s := &sink{err: context.Canceled}
	if w := post(newReceiver(s), "ripple", "s3cret", pushBody); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d", w.Code)
	}
}
