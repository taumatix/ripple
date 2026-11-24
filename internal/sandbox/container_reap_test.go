package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Reap names the container of a Wrap'd argv to the runtime, kills it, then removes it with -f,
// in that order, and does nothing for an argv without a name. A fake runtime script records
// what it is asked, so no container is needed.
func TestReapKillsThenForceRemovesTheNamedContainer(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "rt")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := Container(FlavourDocker, bin, ContainerOptions{Image: "img:1"})
	if err != nil {
		t.Fatal(err)
	}
	argv, err := b.Wrap(jobPolicy, []string{"sleep", "300"})
	if err != nil {
		t.Fatal(err)
	}
	name := argv[slices.Index(argv, "--name")+1]
	b.(Reaper).Reap(argv) // both calls fail in the fake: a runtime that has already removed it is not an error
	got, _ := os.ReadFile(log)
	if want := "kill " + name + "\nrm -f " + name + "\n"; string(got) != want {
		t.Fatalf("runtime was asked %q, want %q", got, want)
	}
	os.Remove(log)
	b.(Reaper).Reap([]string{"docker", "run", "img"})
	if got, _ := os.ReadFile(log); strings.TrimSpace(string(got)) != "" {
		t.Errorf("an argv without a name must not reach the runtime: %q", got)
	}
}
