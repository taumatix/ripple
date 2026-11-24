//go:build emulator

package codecommit_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
)

// The CodeCommit host against the public AWS Emulator (docs/aws-emulator.md), not the in-process
// fake: the .github/workflows/emulator.yml `codecommit` job starts it and trusts its CA.
//
//	RIPPLE_TEST_CODECOMMIT_ENDPOINT=https://127.0.0.1:9298 go test -tags emulator ./internal/platform/codecommit -run Emulator -v
//
// The emulator is in memory and models neither throttling nor IAM, so this checks that ripple's
// requests and its reading of the answers are shaped as the service's own API does, not what AWS
// does under load or with a denied policy.
const emuRegion = "us-west-2"

type emuAPI struct {
	t     *testing.T
	url   string
	creds aws.Credentials
}

// do sends one CodeCommit JSON 1.1 call with its own signer, so the setup the host cannot
// do (creating a repository, a commit, a branch) does not depend on the code under test.
func (a emuAPI) do(op string, in any) map[string]any {
	a.t.Helper()
	body, _ := json.Marshal(in)
	req, _ := http.NewRequest(http.MethodPost, a.url+"/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "CodeCommit_20150413."+op)
	sum := sha256.Sum256(body)
	if err := v4.NewSigner().SignHTTP(context.Background(), a.creds, req, hex.EncodeToString(sum[:]), "codecommit", emuRegion, time.Now()); err != nil {
		a.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s: %v", op, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		a.t.Fatalf("%s: HTTP %d: %s", op, resp.StatusCode, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// mintGitCredentials creates an IAM user allowed codecommit:* and an access key for it. The emulator's
// git transport answers 401 to any user name no store of its own minted, even with --auth off
// (services/.../git_authorization.rs), so ripple's git username has to be a real emulator key.
func (a emuAPI) mintGitCredentials() aws.Credentials {
	a.t.Helper()
	call := func(form url.Values) string {
		form.Set("Version", "2010-05-08")
		body := form.Encode()
		req, _ := http.NewRequest(http.MethodPost, a.url+"/", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		sum := sha256.Sum256([]byte(body))
		if err := v4.NewSigner().SignHTTP(context.Background(), a.creds, req, hex.EncodeToString(sum[:]), "iam", "us-east-1", time.Now()); err != nil {
			a.t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			a.t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			a.t.Fatalf("IAM %s: HTTP %d: %s", form.Get("Action"), resp.StatusCode, raw)
		}
		return string(raw)
	}
	call(url.Values{"Action": {"CreateUser"}, "UserName": {"ripple-git"}})
	call(url.Values{"Action": {"PutUserPolicy"}, "UserName": {"ripple-git"}, "PolicyName": {"cc"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"codecommit:*","Resource":"*"}]}`}})
	out := call(url.Values{"Action": {"CreateAccessKey"}, "UserName": {"ripple-git"}})
	id := regexp.MustCompile(`<AccessKeyId>([^<]+)</AccessKeyId>`).FindStringSubmatch(out)
	sec := regexp.MustCompile(`<SecretAccessKey>([^<]+)</SecretAccessKey>`).FindStringSubmatch(out)
	if id == nil || sec == nil {
		a.t.Fatalf("CreateAccessKey answered %s", out)
	}
	return aws.Credentials{AccessKeyID: id[1], SecretAccessKey: sec[1]}
}

func (a emuAPI) putFile(repo, branch, parent, path, content string) string {
	a.t.Helper()
	in := map[string]any{"repositoryName": repo, "branchName": branch, "filePath": path,
		"fileContent": base64.StdEncoding.EncodeToString([]byte(content)), "commitMessage": "add " + path,
		"name": "ripple", "email": "ripple@example.invalid"}
	if parent != "" {
		in["parentCommitId"] = parent
	}
	return a.do("PutFile", in)["commitId"].(string)
}

func TestEmulatorCodeCommitHost(t *testing.T) {
	endpoint := os.Getenv("RIPPLE_TEST_CODECOMMIT_ENDPOINT")
	if endpoint == "" {
		t.Fatal("RIPPLE_TEST_CODECOMMIT_ENDPOINT is not set; this file only builds with -tags emulator")
	}
	cr := aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}
	a := emuAPI{t: t, url: endpoint, creds: cr}
	cr = a.mintGitCredentials()
	a.creds = cr
	const repoName = "ripple-emu"
	path := emuRegion + "/" + repoName
	a.do("CreateRepository", map[string]any{"repositoryName": repoName})
	base := a.putFile(repoName, "main", "", "README.md", "hello\n")
	a.do("CreateBranch", map[string]any{"repositoryName": repoName, "branchName": "ripple/dep", "commitId": base})
	head := a.putFile(repoName, "ripple/dep", base, "NEW.txt", "n")

	c := &codecommit.Client{Regions: []string{emuRegion}, Endpoint: endpoint, GitBase: endpoint,
		Creds: credentials.NewStaticCredentialsProvider(cr.AccessKeyID, cr.SecretAccessKey, ""), WorkDir: t.TempDir(),
		AuthorEmail: "ripple@example.invalid"}
	ctx := context.Background()
	repo := model.Repo{FullName: path, DefaultBranch: "main"}

	t.Run("list repositories", func(t *testing.T) {
		repos, err := c.ListRepos(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range repos {
			if r.FullName == path {
				return
			}
		}
		t.Errorf("%s not listed in %v", path, repos)
	})
	var pr platform.PR
	t.Run("create and list a pull request", func(t *testing.T) {
		var err error
		pr, err = c.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/dep", Base: "main", Title: "bump", Body: "b"})
		if err != nil {
			t.Fatal(err)
		}
		open, err := c.ListOpenPRs(ctx, repo, "ripple/")
		if err != nil {
			t.Fatal(err)
		}
		if len(open) != 1 || open[0].Number != pr.Number || open[0].Head != "ripple/dep" {
			t.Errorf("open %+v, want %d on ripple/dep", open, pr.Number)
		}
	})
	t.Run("changed files between two commits", func(t *testing.T) {
		got, err := c.ChangedFiles(ctx, repo, base, head)
		if err != nil || len(got) != 1 || got[0] != "NEW.txt" {
			t.Errorf("%v %v", got, err)
		}
	})
	t.Run("close an untouched pull request", func(t *testing.T) {
		closed, err := c.CloseUntouchedPR(ctx, repo, pr, "superseded")
		if err != nil || !closed {
			t.Fatalf("closed %v: %v", closed, err)
		}
		if open, err := c.ListOpenPRs(ctx, repo, "ripple/"); err != nil || len(open) != 0 {
			t.Errorf("still open: %+v %v", open, err)
		}
	})
	t.Run("git accepts ripple's SigV4 git password", func(t *testing.T) {
		// What Checkout hands git, sent the way gitws sends it (a preemptive Basic header), to
		// a command that needs no shallow support: the transport must accept the credential.
		u, _ := url.Parse(endpoint)
		creds := aws.Credentials{AccessKeyID: cr.AccessKeyID, SecretAccessKey: cr.SecretAccessKey}
		pw := codecommit.GitPassword(u.Host, "/v1/repos/"+repoName, emuRegion, creds.SecretAccessKey, time.Now())
		basic := base64.StdEncoding.EncodeToString([]byte(codecommit.GitUsername(creds) + ":" + pw))
		cmd := exec.Command("git", "ls-remote", endpoint+"/v1/repos/"+repoName)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+endpoint+"/.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+basic)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "refs/heads/ripple/dep") {
			t.Fatalf("ls-remote: %v\n%s", err, out)
		}
		// And a wrong password is refused, so the pass above is not an open door.
		bad := base64.StdEncoding.EncodeToString([]byte("nobody:x"))
		cmd = exec.Command("git", "ls-remote", endpoint+"/v1/repos/"+repoName)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+endpoint+"/.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+bad)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("an unknown user was accepted:\n%s", out)
		}
	})
	t.Run("checkout falls back to a full clone when the emulator refuses shallow", func(t *testing.T) {
		// Checkout clones with --depth 1, which real CodeCommit serves and the emulator's
		// upload-pack refuses ("Server does not support shallow clients", emulator 1.0.598);
		// gitws then retries with a whole clone. If the emulator serves that too, the
		// checkout must hold the default branch's files. If it does not, say why in the log
		// rather than pinning an emulator gap (docs/aws-emulator.md, ROADMAP 16).
		ws, err := c.Checkout(ctx, repo)
		if err != nil {
			t.Logf("emulator refuses the whole clone as well: %v", err)
			return
		}
		defer ws.Close()
		got, err := ws.ReadFile("README.md")
		if err != nil || string(got) != "hello\n" {
			t.Fatalf("README.md = %q, %v", got, err)
		}
	})
}
