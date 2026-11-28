#!/usr/bin/env bash
# Registers a gitlab-runner (shell executor, in a public image) with the throwaway GitLab CE the
# bootstrap script prepared, so merge requests there get real pipelines. No secret leaves the job:
# the runner token is masked and lives only in the runner's volume.
#
#   GITLAB_URL        instance root, default http://127.0.0.1:8929
#   RIPPLE_TEST_GITLAB_TOKEN  the root token ci-gitlab-bootstrap.sh exported
set -euo pipefail

base=${GITLAB_URL:-http://127.0.0.1:8929}
root=${RIPPLE_TEST_GITLAB_TOKEN:?run ci-gitlab-bootstrap.sh first}
image=gitlab/gitlab-runner:latest

api() { # api METHOD PATH [JSON]: prints the body, fails showing it on a non-2xx
  local m=$1 p=$2 d=${3:-} code out
  out=$(mktemp)
  if [ -n "$d" ]; then
    code=$(curl -sS -H "Authorization: Bearer $root" -H 'Content-Type: application/json' -X "$m" -d "$d" -o "$out" -w '%{http_code}' "$base/api/v4$p")
  else
    code=$(curl -sS -H "Authorization: Bearer $root" -X "$m" -o "$out" -w '%{http_code}' "$base/api/v4$p")
  fi
  cat "$out"; echo
  case $code in 2??) rm -f "$out" ;; *) echo "$m $p: HTTP $code" >&2; rm -f "$out"; return 1 ;; esac
}

echo "--- create an instance runner (POST /user/runners)"
resp=$(api POST /user/runners '{"runner_type":"instance_type","description":"ripple-ci","run_untagged":true}')
tok=$(printf '%s' "$resp" | jq -r .token)
echo "::add-mask::$tok"
case $tok in glrt-*) ;; *) echo "no glrt- token in the answer" >&2; exit 1 ;; esac

echo "--- register it with the shell executor and start it"
docker volume create ripple-runner >/dev/null
docker run --rm --network host -v ripple-runner:/etc/gitlab-runner "$image" register --non-interactive \
  --url "$base" --token "$tok" --executor shell 2>&1 | grep -v "$tok" | tail -n 8
docker run -d --name runner --network host -v ripple-runner:/etc/gitlab-runner "$image" >/dev/null

echo "--- wait for the runner to be online"
for i in $(seq 30); do
  st=$(curl -sS -H "Authorization: Bearer $root" "$base/api/v4/runners/all?type=instance_type" | jq -r '[.[]|select(.description=="ripple-ci")|.status][0] // "none"')
  echo "runner status: $st"
  if [ "$st" = online ]; then
    echo "RIPPLE_TEST_GITLAB_RUNNER=1" >> "${GITHUB_ENV:-/dev/null}"
    exit 0
  fi
  sleep 5
done
docker logs runner 2>&1 | tail -n 40
exit 1
