package codecommit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
	"github.com/taumatix/ripple/internal/testutil"
)

type sink struct {
	mu  sync.Mutex
	got []platform.Event
	err error
}

func (s *sink) HandleEvent(_ context.Context, e platform.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, e)
	return s.err
}

type rx struct {
	cc  *testutil.CodeCommit
	s   *sink
	h   http.Handler
	got []string
}

// newRx is a receiver on the fake's certificate, with a recorded Files lookup.
func newRx(t *testing.T, files func(context.Context, model.Repo, string, string) ([]string, error)) *rx {
	t.Helper()
	cc := testutil.NewCodeCommit(t, region)
	r := &rx{cc: cc, s: &sink{}}
	rc := loopback(cc, topic)
	rc.Files = func(ctx context.Context, repo model.Repo, a, b string) ([]string, error) {
		r.got = append(r.got, repo.FullName+" "+a+".."+b)
		return files(ctx, repo, a, b)
	}
	c := &codecommit.Client{Regions: []string{region}}
	r.h = codecommit.NewHost(c, rc, nil).Receiver(r.s)
	return r
}

func (r *rx) post(hdr http.Header, body []byte) int {
	req := httptest.NewRequest(http.MethodPost, "/webhook/codecommit", bytes.NewReader(body))
	for k, v := range hdr {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w.Code
}

func okFiles(...string) func(context.Context, model.Repo, string, string) ([]string, error) {
	return func(context.Context, model.Repo, string, string) ([]string, error) { return []string{"go.mod"}, nil }
}

func TestNotificationBecomesAPushForBothSignatureVersions(t *testing.T) {
	for _, v := range []string{"1", "2"} {
		r := newRx(t, okFiles())
		hdr, body := r.cc.SNSMessage("Notification", v, topic, refUpdated(region+"/app", "main", "aaa", "bbb"))
		if code := r.post(hdr, body); code != http.StatusAccepted {
			t.Fatalf("version %s: status %d", v, code)
		}
		// ug-monitoring-events.md: referenceFullName is the ref; the lookup gets both commits.
		want := platform.Event{Kind: platform.Push, Delivery: "165545c9-2a5c-472c-8df2-7ff2be2b3b1b",
			Repos: []string{region + "/app"}, Ref: "refs/heads/main", Files: []string{"go.mod"}}
		if len(r.s.got) != 1 || !eventEq(r.s.got[0], want) || r.got[0] != region+"/app aaa..bbb" {
			t.Errorf("version %s: %+v lookups %v", v, r.s.got, r.got)
		}
	}
}

func eventEq(a, b platform.Event) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestLookupFailureFallsBackToAFullScan(t *testing.T) {
	r := newRx(t, func(context.Context, model.Repo, string, string) ([]string, error) { return nil, errors.New("boom") })
	hdr, body := r.cc.SNSMessage("Notification", "2", topic, refUpdated(region+"/app", "main", "a", "b"))
	if code := r.post(hdr, body); code != http.StatusAccepted || len(r.s.got) != 1 || len(r.s.got[0].Files) != 0 {
		t.Errorf("status %d events %+v", code, r.s.got)
	}
}

func TestReferenceCreatedHasNoLookup(t *testing.T) {
	r := newRx(t, okFiles())
	msg := strings.Replace(refUpdated(region+"/app", "main", "", "bbb"), "referenceUpdated", "referenceCreated", 1)
	hdr, body := r.cc.SNSMessage("Notification", "2", topic, msg)
	if code := r.post(hdr, body); code != http.StatusAccepted || len(r.s.got) != 1 || len(r.got) != 0 {
		t.Errorf("status %d events %+v lookups %v", code, r.s.got, r.got)
	}
}

func TestOtherEventsAreAcknowledgedAndIgnored(t *testing.T) {
	for name, msg := range map[string]string{
		"tag":    strings.Replace(refUpdated(region+"/app", "main", "a", "b"), `"branch"`, `"tag"`, 1),
		"delete": strings.Replace(refUpdated(region+"/app", "main", "a", "b"), "referenceUpdated", "referenceDeleted", 1),
		"source": strings.Replace(refUpdated(region+"/app", "main", "a", "b"), "aws.codecommit", "aws.other", 1),
	} {
		r := newRx(t, okFiles())
		hdr, body := r.cc.SNSMessage("Notification", "2", topic, msg)
		if code := r.post(hdr, body); code != http.StatusAccepted || len(r.s.got) != 0 {
			t.Errorf("%s: status %d events %+v", name, code, r.s.got)
		}
	}
}

func TestSinkFailureAsksSNSToRetry(t *testing.T) {
	r := newRx(t, okFiles())
	r.s.err = errors.New("db down")
	hdr, body := r.cc.SNSMessage("Notification", "2", topic, refUpdated(region+"/app", "main", "a", "b"))
	if code := r.post(hdr, body); code != http.StatusServiceUnavailable {
		t.Errorf("status %d", code)
	}
}

func TestRefusals(t *testing.T) {
	r := newRx(t, okFiles())
	msg := refUpdated(region+"/app", "main", "a", "b")
	hdr, body := r.cc.SNSMessage("Notification", "2", topic, msg)

	wrongHdr := hdr.Clone()
	wrongHdr.Set("x-amz-sns-topic-arn", "arn:aws:sns:us-east-2:999999999999:evil")
	_, evilBody := r.cc.SNSMessage("Notification", "2", "arn:aws:sns:us-east-2:999999999999:evil", msg)
	typeHdr := hdr.Clone()
	typeHdr.Set("x-amz-sns-message-type", "SubscriptionConfirmation")
	tampered := bytes.Replace(body, []byte("referenceUpdated"), []byte("referenceCreated"), 1)
	badSig := bytes.Replace(body, []byte(`"SignatureVersion":"2"`), []byte(`"SignatureVersion":"3"`), 1)
	foreign := bytes.Replace(body, []byte(r.cc.CertURL), []byte("https://evil.example.com/cert.pem"), 1)

	for name, c := range map[string]struct {
		hdr  http.Header
		body []byte
	}{
		"topic header not allowed":           {wrongHdr, body},
		"no headers":                         {http.Header{}, body},
		"signed topic differs from header":   {hdr, evilBody},
		"type header differs from body":      {typeHdr, body},
		"body altered after signing":         {hdr, tampered},
		"unsupported SignatureVersion":       {hdr, badSig},
		"certificate URL is not an SNS host": {hdr, foreign},
		"not JSON":                           {hdr, []byte("hello")},
	} {
		if code := r.post(c.hdr, c.body); code != http.StatusUnauthorized && code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, code)
		}
	}
	if len(r.s.got) != 0 {
		t.Errorf("refused deliveries reached the sink: %+v", r.s.got)
	}
	// GET is not a delivery.
	req := httptest.NewRequest(http.MethodGet, "/webhook/codecommit", nil)
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}

func TestNoTopicsRefusesEverything(t *testing.T) {
	cc := testutil.NewCodeCommit(t, region)
	h := codecommit.NewHost(&codecommit.Client{}, codecommit.Receiver{}, nil).Receiver(&sink{})
	hdr, body := cc.SNSMessage("Notification", "2", topic, refUpdated(region+"/app", "main", "a", "b"))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header = hdr
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d", w.Code)
	}
}

// sns-SendMessageToHttp.confirm.md: visiting SubscribeURL completes the subscription.
func TestSubscriptionConfirmationVisitsSubscribeURL(t *testing.T) {
	r := newRx(t, okFiles())
	hdr, body := r.cc.SNSMessage("SubscriptionConfirmation", "2", topic, "You have chosen to subscribe")
	if code := r.post(hdr, body); code != http.StatusOK || r.cc.Confirmed() != 1 {
		t.Errorf("status %d, confirmations %d", code, r.cc.Confirmed())
	}
}

func TestAWSURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://sns.us-east-2.amazonaws.com/SimpleNotificationService-abc.pem": true,
		"https://sns.cn-north-1.amazonaws.com.cn/x.pem":                         true,
		"http://sns.us-east-2.amazonaws.com/x.pem":                              false,
		"https://sns.us-east-2.amazonaws.com.evil.com/x.pem":                    false,
		"https://evilsns.us-east-2.amazonaws.com/x.pem":                         false,
		"https://sns.us-east-2.amazonaws.com:8443/x.pem":                        false,
		"https://sns.us-east-2.amazonaws.com@evil.com/x.pem":                    false,
		"https://user@sns.us-east-2.amazonaws.com/x.pem":                        false,
		"https://amazonaws.com/x.pem":                                           false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := codecommit.AWSURL(u); got != ok {
			t.Errorf("AWSURL(%s) = %v", raw, got)
		}
	}
}
