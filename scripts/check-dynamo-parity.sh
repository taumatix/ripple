#!/usr/bin/env bash
# Compares the `fake` and `dynamodb-local` subtests of every Dynamo store test in a
# `go test -v` log. Both run the same test body, so a test that passes on the in-process fake
# and fails (or is missing) on DynamoDB Local is a gap in internal/store/dynamofake: the
# fake accepts something the service rejects, or behaves differently from it.
#
#   scripts/check-dynamo-parity.sh test.log     compare, exit 1 on any difference
#   scripts/check-dynamo-parity.sh --self-test  check the comparison against sample logs
set -euo pipefail

compare() {
  awk '
    match($0, /--- (PASS|FAIL|SKIP): [^ ]+/) {
      split(substr($0, RSTART+4, RLENGTH-4), f, ": ")
      status = f[1]; name = f[2]
      n = split(name, parts, "/")
      side = ""; norm = ""
      for (i = 1; i <= n; i++) {
        if (side == "" && (parts[i] == "fake" || parts[i] == "dynamodb-local")) { side = parts[i]; parts[i] = "@" }
        norm = norm (i > 1 ? "/" : "") parts[i]
      }
      if (side == "") next
      seen[norm] = 1; st[side SUBSEP norm] = status
    }
    END {
      for (t in seen) {
        a = st["fake" SUBSEP t]; b = st["dynamodb-local" SUBSEP t]
        if (a == "" || b == "") { printf "%s: only run on %s\n", t, (a == "" ? "dynamodb-local" : "fake"); bad = 1 }
        else if (a != b) { printf "%s: fake %s, dynamodb-local %s\n", t, a, b; bad = 1 }
        else checked++
      }
      if (!checked && !bad) { print "no fake/dynamodb-local subtests found in the log"; bad = 1 }
      if (!bad) printf "dynamo parity ok: %d test(s) agree between the fake and DynamoDB Local\n", checked
      exit bad
    }' "$1" | sort
  return "${PIPESTATUS[0]}"
}

if [ "${1:-}" = "--self-test" ]; then
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
  cat >"$tmp/ok.log" <<'LOG'
    --- PASS: TestDynamo/fake (0.10s)
        --- PASS: TestDynamo/fake/Suite (0.01s)
    --- PASS: TestDynamo/dynamodb-local (0.40s)
        --- PASS: TestDynamo/dynamodb-local/Suite (0.02s)
--- PASS: TestDynamo (0.50s)
LOG
  compare "$tmp/ok.log" >/dev/null || { echo "self-test: matching log rejected" >&2; exit 1; }
  sed 's#PASS: TestDynamo/dynamodb-local/Suite#FAIL: TestDynamo/dynamodb-local/Suite#' "$tmp/ok.log" >"$tmp/diff.log"
  out=$(compare "$tmp/diff.log") && { echo "self-test: differing status accepted" >&2; exit 1; }
  grep -q 'TestDynamo/@/Suite: fake PASS, dynamodb-local FAIL' <<<"$out" || { echo "self-test: wrong report: $out" >&2; exit 1; }
  grep -v 'dynamodb-local' "$tmp/ok.log" >"$tmp/missing.log"
  out=$(compare "$tmp/missing.log") && { echo "self-test: missing side accepted" >&2; exit 1; }
  grep -q 'only run on fake' <<<"$out" || { echo "self-test: wrong report: $out" >&2; exit 1; }
  echo "SKIP only" | compare /dev/stdin >/dev/null && { echo "self-test: empty log accepted" >&2; exit 1; }
  echo "parity self-test ok"
  exit 0
fi

[ $# -eq 1 ] || { echo "usage: $0 test.log | --self-test" >&2; exit 2; }
compare "$1"
