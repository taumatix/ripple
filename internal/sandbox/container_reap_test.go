package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	fastReap(t)
	b.(Reaper).Reap(argv) // both calls fail in the fake: a runtime that has already removed it is not an error
	got, _ := os.ReadFile(log)
	// Nothing found the container, so Reap looked again reapLateAttempts more times.
	if want := strings.Repeat("kill "+name+"\nrm -f "+name+"\n", reapLateAttempts+1); string(got) != want {
		t.Fatalf("runtime was asked %q, want %q", got, want)
	}
	os.Remove(log)
	b.(Reaper).Reap([]string{"docker", "run", "img"})
	if got, _ := os.ReadFile(log); strings.TrimSpace(string(got)) != "" {
		t.Errorf("an argv without a name must not reach the runtime: %q", got)
	}
}

// fastReap shortens the wait between Reap's looks for the test and restores it after.
func fastReap(t *testing.T) {
	t.Helper()
	old := reapLateDelay
	reapLateDelay = time.Millisecond
	t.Cleanup(func() { reapLateDelay = old })
}

// The timeout killed the client before the runtime had created the container (the former
// ROADMAP 197, cause b): the first kill and remove find nothing, and the container appears a moment later.
// Reap must look again and stop it, and must stop looking once it has.
func TestReapFindsAContainerTheRuntimeCreatesLate(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	seen := filepath.Join(dir, "created")
	bin := filepath.Join(dir, "rt")
	// The container "exists" from the third call on: kill and rm answer "no such container"
	// (exit 1) before that, and kill succeeds once it exists.
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"n=$(wc -l < " + log + ")\n" +
		"if [ \"$n\" -ge 5 ]; then touch " + seen + "; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
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
	fastReap(t)
	b.(Reaper).Reap(argv)
	got, _ := os.ReadFile(log)
	// Calls 1-4 find nothing (two looks), call 5 (the third kill) succeeds, its rm follows, and Reap stops.
	want := strings.Repeat("kill "+name+"\nrm -f "+name+"\n", 3)
	if string(got) != want {
		t.Fatalf("runtime was asked %q, want %q", got, want)
	}
	if _, err := os.Stat(seen); err != nil {
		t.Error("the late container was never reached")
	}
}
