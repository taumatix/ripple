package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FakeRuntime is a stand-in for a container CLI (docker, podman, Apple's container).
// It records every invocation's argv, parses `run` the way the real CLIs do (options
// with values, boolean options, then image, then the command), and then runs the command
// directly on the host: in the -w directory, with the -e keys taken from its own
// environment. `kill` and `rm` are recorded. It confines nothing, so it proves the argv
// ripple builds and the engine path around it, not that a real runtime accepts it.
//
// A file named "hang" in the fake's directory makes `run` sleep instead of running the
// command, to exercise timeouts.
type FakeRuntime struct {
	Bin string // path of the executable
	dir string
}

const fakeRuntimeScript = `#!/bin/sh
dir="$(dirname "$0")"
{ printf '%s\n' "$@"; printf '%s\n' '--end-of-call--'; } >> "$dir/calls.log"
case "$1" in
run) shift;;
*) exit 0;;
esac
wd=.
detached=
while [ $# -gt 0 ]; do
  case "$1" in
    --rm|--read-only|--init) shift;;
    -d) detached=1; shift;;
    -w) wd="$2"; shift 2;;
    --name|--label|--progress|--pull|--pids-limit|--security-opt|--network|--entrypoint|--tmpfs|--cap-drop|--user|--userns|-c|--cpus|-m|--memory|-v) shift 2;;
    -e) case "$2" in *=*) export "$2";; esac; shift 2;;
    -*) echo "fake runtime: unknown option $1" >&2; exit 125;;
    *) break;;
  esac
done
image="$1"; shift
[ -n "$detached" ] && exit 0
[ -f "$dir/hang" ] && exec sleep 30
cd "$wd" || exit 126
exec "$@"
`

// NewFakeRuntime writes the fake into a temp directory.
func NewFakeRuntime(t *testing.T) *FakeRuntime {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecontainer")
	if err := os.WriteFile(bin, []byte(fakeRuntimeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return &FakeRuntime{Bin: bin, dir: dir}
}

// Hang makes later `run` calls block instead of running their command.
func (f *FakeRuntime) Hang(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "hang"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Calls returns the recorded argv (after the binary) of every invocation.
func (f *FakeRuntime) Calls() [][]string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	var out [][]string
	var cur []string
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if l == "--end-of-call--" {
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, l)
	}
	return out
}

// Runs returns the recorded invocations whose first argument is "run".
func (f *FakeRuntime) Runs() [][]string {
	var out [][]string
	for _, c := range f.Calls() {
		if len(c) > 0 && c[0] == "run" {
			out = append(out, c)
		}
	}
	return out
}
