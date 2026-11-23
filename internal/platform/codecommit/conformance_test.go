package codecommit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &ccFixture{})
}

const (
	region = "us-east-2"
	topic  = "arn:aws:sns:us-east-2:123456789012:ripple"
)

// ccFixture drives the fake CodeCommit. CodeCommit has no install event: Register makes
// the repositories visible in the listing and the suite's discovery lists them. A push
// is an EventBridge event delivered through a signed SNS notification, as in production.
type ccFixture struct{ cc *testutil.CodeCommit }

func creds(cc *testutil.CodeCommit) aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider(cc.AccessKey, cc.SecretKey, cc.SessionToken)
}

func (f *ccFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.cc = testutil.NewCodeCommit(t, region)
	c := &codecommit.Client{Regions: []string{region}, Creds: creds(f.cc), Endpoint: f.cc.APIURL, GitBase: f.cc.GitURL, Now: now,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return codecommit.NewHost(c, loopback(f.cc, topic), slog.New(slog.DiscardHandler))
}

// loopback is a Receiver that fetches the fake's certificate over plain HTTP on loopback.
func loopback(cc *testutil.CodeCommit, topics ...string) codecommit.Receiver {
	u, _ := url.Parse(cc.APIURL)
	return codecommit.Receiver{Topics: topics, AllowURL: func(x *url.URL) bool { return x.Host == u.Host }}
}

func (f *ccFixture) Path(short string) string { return region + "/" + short }

func (f *ccFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.cc.AddRepo(path, files)
}

func (f *ccFixture) Register(_ *testing.T, _ string, paths ...string) {
	for _, p := range paths {
		f.cc.Member(p)
	}
}

// refUpdated is the EventBridge event for a branch update
// (docs/vendor/codecommit/ug-monitoring-events.md, "referenceUpdated event").
func refUpdated(path, branch, old, new string) string {
	r, name, _ := strings.Cut(path, "/")
	b, _ := json.Marshal(map[string]any{
		"version": "0", "id": "01234567-EXAMPLE", "detail-type": "CodeCommit Repository State Change",
		"source": "aws.codecommit", "account": "123456789012", "time": "2019-06-12T10:23:43Z", "region": r,
		"resources": []string{"arn:aws:codecommit:" + r + ":123456789012:" + name},
		"detail": map[string]any{"event": "referenceUpdated", "repositoryName": name, "referenceType": "branch",
			"referenceName": branch, "referenceFullName": "refs/heads/" + branch, "oldCommitId": old, "commitId": new},
	})
	return string(b)
}

func (f *ccFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	old := f.cc.Head(path)
	f.cc.Commit(path, files)
	hdr, body := f.cc.SNSMessage("Notification", "2", topic, refUpdated(path, "main", old, f.cc.Head(path)))
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, hook, bytes.NewReader(body))
	req.Header = hdr
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("push notification: %d", resp.StatusCode)
	}
}

func (f *ccFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.cc.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft})
	}
	return out
}

func (f *ccFixture) FileAt(path, branch, file string) (string, bool) {
	return f.cc.FileAt(path, branch, file)
}
func (f *ccFixture) Calls(path string) int { return f.cc.Calls(path) }
func (f *ccFixture) RateLimitNextPR() bool { f.cc.RateLimitNext(); return true }

// PushLookupCalls: the event names no files, so each push costs one GetDifferences.
func (f *ccFixture) FilelessPushes() {}
