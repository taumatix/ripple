package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// logged is one commit of a branch with the identities it carries.
type logged struct{ AuthorName, AuthorEmail, CommitterName, CommitterEmail string }

// branchLog lists the commits head has beyond base in the bare repository, oldest first.
func branchLog(bare, base, head string) []logged {
	cmd := exec.Command("git", "log", "--reverse", "--format=%an%x09%ae%x09%cn%x09%ce", base+".."+head)
	cmd.Dir = bare
	cmd.Env = gitEnv()
	out, _ := cmd.Output()
	var cs []logged
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Split(l, "\t"); len(f) == 4 {
			cs = append(cs, logged{f[0], f[1], f[2], f[3]})
		}
	}
	return cs
}

// humanCommit pushes one more commit, authored by a person, onto branch.
func humanCommit(t *testing.T, bare, branch string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "clone", "-q", "--branch", branch, bare, "w")
	w := filepath.Join(work, "w")
	if err := os.WriteFile(filepath.Join(w, "HUMAN.txt"), []byte("hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w, "add", "-A")
	git(t, w, "commit", "-q", "-m", "hand edit")
	git(t, w, "push", "-q", "origin", branch)
}

// forcePushAmended rewrites branch's tip keeping author and committer.
func forcePushAmended(t *testing.T, bare, branch string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "clone", "-q", "--branch", branch, bare, "w")
	w := filepath.Join(work, "w")
	f := strings.Split(strings.TrimSpace(git(t, w, "log", "-1", "--format=%an\t%ae\t%cn\t%ce")), "\t")
	cmd := exec.Command("git", "commit", "-q", "--amend", "--no-edit", "--allow-empty", "-m", "amended")
	cmd.Dir = w
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME="+f[0], "GIT_AUTHOR_EMAIL="+f[1], "GIT_COMMITTER_NAME="+f[2], "GIT_COMMITTER_EMAIL="+f[3])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("amend: %v\n%s", err, out)
	}
	git(t, w, "push", "-q", "--force", "origin", branch)
}

// pushBranchCommit creates branch from main with one commit authored and committed by email.
func pushBranchCommit(t *testing.T, bare, branch, email string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "clone", "-q", "--branch", "main", bare, "w")
	w := filepath.Join(work, "w")
	git(t, w, "checkout", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(w, "BUMP.txt"), []byte(branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w, "add", "-A")
	cmd := exec.Command("git", "commit", "-q", "-m", "bump")
	cmd.Dir = w
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME=ripple", "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_NAME=ripple", "GIT_COMMITTER_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	git(t, w, "push", "-q", "origin", branch)
}
