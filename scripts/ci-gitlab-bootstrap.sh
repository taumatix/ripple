#!/usr/bin/env bash
# One-shot bootstrap of a throwaway GitLab CE container for the hosts-gitlab-gerrit workflow.
#
# GitLab CE has no first-run API for a token, so the root personal access token is created the way GitLab's
# own documentation does it for automation: a Rails runner inside the container (docs.gitlab.com, "Create a
# personal access token programmatically"). The token belongs to a container that is gone when the job
# ends; it is still masked in the log and written only to $GITHUB_ENV.
#
#   GITLAB_URL     instance root, default http://127.0.0.1:8929
#   CONTAINER      container name, default gitlab
set -euo pipefail

base=${GITLAB_URL:-http://127.0.0.1:8929}
ctr=${CONTAINER:-gitlab}
token=${ROOT_TOKEN:-$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')}
echo "::add-mask::$token"

echo "--- version (unauthenticated endpoints only)"
curl -sS -o /dev/null -w 'GET /users/sign_in: %{http_code}\n' "$base/users/sign_in"
curl -sS -o /dev/null -w 'GET /-/readiness: %{http_code}\n' "$base/-/readiness"

echo "--- create the root personal access token"
docker exec -e RIPPLE_TOKEN="$token" "$ctr" gitlab-rails runner "
user = User.find_by_username('root')
t = user.personal_access_tokens.create!(scopes: [:api, :read_repository, :write_repository, :sudo, :create_runner], name: 'ripple-ci', expires_at: 30.days.from_now)
t.set_token(ENV['RIPPLE_TOKEN'])
t.save!
puts 'token created'
" 2>&1 | tail -n 5

api() { # api METHOD PATH [JSON]: prints the body, fails showing it on a non-2xx
  local m=$1 p=$2 d=${3:-} code out
  out=$(mktemp)
  if [ -n "$d" ]; then
    code=$(curl -sS -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -X "$m" -d "$d" -o "$out" -w '%{http_code}' "$base/api/v4$p")
  else
    code=$(curl -sS -H "Authorization: Bearer $token" -X "$m" -o "$out" -w '%{http_code}' "$base/api/v4$p")
  fi
  cat "$out"; echo
  case $code in 2??) rm -f "$out" ;; *) echo "$m $p: HTTP $code"; rm -f "$out"; return 1 ;; esac
}

echo "--- instance version"
api GET /version
echo "--- webhooks may reach the runner's own loopback (the relay on 127.0.0.1:18999)"
api PUT /application/settings '{"allow_local_requests_from_web_hooks_and_services":true}' > /dev/null
echo "--- the project-level throttle starts off; TestRealRateLimit turns it on and back off"

{
  echo "RIPPLE_TEST_GITLAB_URL=$base"
  echo "RIPPLE_TEST_GITLAB_TOKEN=$token"
} >> "${GITHUB_ENV:-/dev/null}"
echo "bootstrap done"
