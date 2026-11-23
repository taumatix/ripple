package codecommit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/taumatix/ripple/internal/model"
)

// The git half over a real HTTP server: the fake verifies the SigV4 git password with its
// own implementation and serves the repository through git-http-backend, so a checkout,
// a push of a branch and the credential going stale all go over real git.
func TestCheckoutAndPushWithSignedGitPassword(t *testing.T) {
	c, cc := newClient(t)
	c.WorkDir = t.TempDir()
	path := region + "/app"
	cc.AddRepo(path, map[string]string{"README.md": "hello\n"})
	ctx := context.Background()
	ws, err := c.Checkout(ctx, model.Repo{FullName: path, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if b, err := ws.ReadFile("README.md"); err != nil || string(b) != "hello\n" {
		t.Fatalf("README: %q %v", b, err)
	}
	if err := ws.WriteFile("NEW.txt", []byte("n")); err != nil {
		t.Fatal(err)
	}
	if ok, err := ws.CommitAndPush(ctx, "ripple/e2e", "add"); err != nil || !ok {
		t.Fatalf("push: %v %v", ok, err)
	}
	if got, ok := cc.FileAt(path, "ripple/e2e", "NEW.txt"); !ok || got != "n" {
		t.Errorf("pushed file: %q %v", got, ok)
	}
	if cc.GitDenied() != 0 {
		t.Errorf("%d git requests were denied", cc.GitDenied())
	}
}

func TestCheckoutWithWrongSecretIsDenied(t *testing.T) {
	c, cc := newClient(t)
	c.WorkDir = t.TempDir()
	c.Creds = credentials.NewStaticCredentialsProvider(cc.AccessKey, "wrong", cc.SessionToken)
	path := region + "/app"
	cc.AddRepo(path, map[string]string{"README.md": "x"})
	_, err := c.Checkout(context.Background(), model.Repo{FullName: path, DefaultBranch: "main"})
	if err == nil || cc.GitDenied() == 0 {
		t.Errorf("error %v, denied %d", err, cc.GitDenied())
	}
	if err != nil && strings.Contains(err.Error(), "wrong") {
		t.Errorf("error leaks the secret: %v", err)
	}
}

func TestCheckoutRefusesBadPaths(t *testing.T) {
	c, _ := newClient(t)
	for _, p := range []string{"app", "us-east-2/../x", "evil.com#/a"} {
		if _, err := c.Checkout(context.Background(), model.Repo{FullName: p}); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}
