package codecommit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
	"github.com/taumatix/ripple/internal/testutil"
)

var t0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func newClient(t *testing.T) (*codecommit.Client, *testutil.CodeCommit) {
	t.Helper()
	cc := testutil.NewCodeCommit(t, region)
	return &codecommit.Client{Regions: []string{region}, Creds: creds(cc), Endpoint: cc.APIURL, GitBase: cc.GitURL,
		Now: func() time.Time { return t0 }}, cc
}

// The expected passwords were produced by running botocore's SigV4Auth through
// git-remote-codecommit's sign() (docs/vendor/codecommit/grc-init.py, "def sign"), with a
// fixed timestamp, so they come from AWS's own code and not from this package.
func TestGitPasswordMatchesGitRemoteCodecommit(t *testing.T) {
	const secret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	for _, c := range []struct {
		host, path, region string
		at                 time.Time
		want               string
	}{
		{"git-codecommit.us-east-2.amazonaws.com", "/v1/repos/MyDemoRepo", "us-east-2",
			time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			"20260102T030405Zb14944c28a48d6ea965c8a176110f827bb1bebcfffe8238b18c8584b29cf0963"},
		{"git-codecommit.cn-north-1.amazonaws.com.cn", "/v1/repos/a.b_c-d", "cn-north-1", t0.Add(0),
			"20261123T100000Za776715210415d53b36bde14e6f3c3544a7b08aecb560fd65c4f591665463407"},
	} {
		if got := codecommit.GitPassword(c.host, c.path, c.region, secret, c.at); got != c.want {
			t.Errorf("%s: password %s, want %s", c.path, got, c.want)
		}
	}
	// A time zone other than UTC must not leak into the timestamp.
	zone := time.FixedZone("x", 5*3600)
	if got := codecommit.GitPassword("git-codecommit.cn-north-1.amazonaws.com.cn", "/v1/repos/a.b_c-d", "cn-north-1", secret, t0.In(zone)); !strings.HasPrefix(got, "20261123T100000Z") {
		t.Errorf("password for a non-UTC clock: %s", got)
	}
}

// grc-init.py git_url: the user name is the access key, plus "%" and the session token
// for temporary credentials.
func TestGitUsername(t *testing.T) {
	if got := codecommit.GitUsername(aws.Credentials{AccessKeyID: "AKID"}); got != "AKID" {
		t.Errorf("long-lived: %q", got)
	}
	if got := codecommit.GitUsername(aws.Credentials{AccessKeyID: "AKID", SessionToken: "tok"}); got != "AKID%tok" {
		t.Errorf("temporary: %q", got)
	}
}

func TestCheckPath(t *testing.T) {
	for p, ok := range map[string]bool{
		"us-east-2/app": true, "eu-west-1/my.repo_1-x": true, "us-gov-west-1/a": true, "cn-north-1/a": true,
		"": false, "app": false, "us-east-2/": false, "us-east-2/a/b": false, "US-EAST-2/a": false,
		"us-east-2/..": false, "us-east-2/.": false, "us-east-2/a.git": false, "us-east-2/a b": false,
		"us-east-2/" + strings.Repeat("a", 101): false, "evil.com#/a": false, "us-east-2@evil/a": false,
	} {
		_, _, err := codecommit.CheckPath(p)
		if (err == nil) != ok {
			t.Errorf("CheckPath(%q) error=%v, want ok=%v", p, err, ok)
		}
	}
}

func TestListReposPagesAndRegion(t *testing.T) {
	c, cc := newClient(t)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		cc.AddRepo(region+"/"+n, map[string]string{"README.md": "x"})
		cc.Member(region + "/" + n)
	}
	got, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range got {
		names = append(names, r.FullName)
	}
	if strings.Join(names, ",") != "us-east-2/a,us-east-2/b,us-east-2/c,us-east-2/d,us-east-2/e" {
		t.Errorf("listing %v", names)
	}
	if n := len(cc.Requests()); n != 3 {
		t.Errorf("%d requests for 5 repositories at 2 per page, want 3", n)
	}
}

func TestListOpenPRsFiltersByHeadPrefix(t *testing.T) {
	c, cc := newClient(t)
	path := region + "/app"
	cc.AddRepo(path, map[string]string{"README.md": "x"})
	repo := model.Repo{FullName: path}
	for _, head := range []string{"ripple/a", "ripple/b", "human/c"} {
		cc.Branch(path, head)
		if _, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Title: head, Head: head, Base: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	prs, err := c.ListOpenPRs(context.Background(), repo, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].Head != "ripple/a" || prs[1].Head != "ripple/b" || prs[0].Number != 1 {
		t.Errorf("open PRs %+v", prs)
	}
	if !strings.Contains(prs[0].URL, "/repositories/app/pull-requests/1") {
		t.Errorf("URL %q", prs[0].URL)
	}
}

func TestCreatePRShapesTheRequest(t *testing.T) {
	c, cc := newClient(t)
	path := region + "/app"
	cc.AddRepo(path, map[string]string{"README.md": "x"})
	cc.Branch(path, "ripple/x")
	repo := model.Repo{FullName: path}
	long := strings.Repeat("é", 300)
	pr, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Title: long, Body: strings.Repeat("b", 20000),
		Head: "ripple/x", Base: "main", Draft: true, Labels: []string{"dependencies"}})
	if err != nil {
		t.Fatal(err)
	}
	got := cc.PRs(path)
	if len(got) != 1 || got[0].Head != "ripple/x" || got[0].Base != "main" || !got[0].Draft {
		t.Fatalf("recorded %+v", got)
	}
	// api-API_CreatePullRequest.md: description at most 10240; the title limit is 100 or
	// 150 by the page, and the lower is kept. Lengths are in characters, not bytes.
	if n := len([]rune(got[0].Title)); n != 100 || !strings.HasPrefix(got[0].Title, "Draft: ") {
		t.Errorf("title is %d characters: %.20q", n, got[0].Title)
	}
	if n := len([]rune(got[0].Description)); n != 10240 {
		t.Errorf("description is %d characters", n)
	}
	// The same branch pair again, as after a lost answer, must not open a second one.
	again, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Title: long, Head: "ripple/x", Base: "main", Draft: true})
	if err != nil || again.Number != pr.Number || len(cc.PRs(path)) != 1 {
		t.Errorf("retry: %+v %v, PRs %d", again, err, len(cc.PRs(path)))
	}
}

// api-CommonErrors.md: ThrottlingException has no Retry-After, so the wait is the
// client's own guess, counted from its clock and kept per region.
func TestThrottlingIsARateLimitError(t *testing.T) {
	c, cc := newClient(t)
	path := region + "/app"
	cc.AddRepo(path, map[string]string{"README.md": "x"})
	cc.Branch(path, "ripple/x")
	repo := model.Repo{FullName: path}
	cc.RateLimitNext()
	_, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Title: "t", Head: "ripple/x", Base: "main"})
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(t0.Add(time.Minute)) {
		t.Fatalf("error %v", err)
	}
	if got := c.RateLimitedUntil(repo); !got.Equal(t0.Add(time.Minute)) {
		t.Errorf("limited until %v", got)
	}
	if got := c.RateLimitedUntil(model.Repo{FullName: "eu-west-1/app"}); !got.IsZero() {
		t.Errorf("another region is limited until %v", got)
	}
}

func TestBadSecretIsRefusedByTheSignatureCheck(t *testing.T) {
	c, cc := newClient(t)
	c.Creds = credentials.NewStaticCredentialsProvider(cc.AccessKey, "not the secret", cc.SessionToken)
	_, err := c.ListRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("error %v", err)
	}
}

func TestChangedFilesPagesAndNamesBothSidesOfADelete(t *testing.T) {
	c, cc := newClient(t)
	path := region + "/app"
	cc.AddRepo(path, map[string]string{"old.txt": "x", "keep.txt": "x"})
	before := cc.Head(path)
	cc.Commit(path, map[string]string{"a.txt": "1", "b.txt": "1", "keep.txt": "2"})
	got, err := c.ChangedFiles(context.Background(), model.Repo{FullName: path}, before, cc.Head(path))
	if err != nil {
		t.Fatal(err)
	}
	// Three changed files at 2 per page: both pages are read.
	if strings.Join(got, ",") != "a.txt,b.txt,keep.txt,keep.txt" { // modified paths appear on both sides
		t.Errorf("files %v", got)
	}
}
