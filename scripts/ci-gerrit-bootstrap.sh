#!/usr/bin/env bash
# One-shot bootstrap of a throwaway Gerrit (gerritcodereview/gerrit) for the hosts-gitlab-gerrit workflow.
#
# The image's default auth is DEVELOPMENT_BECOME_ANY_ACCOUNT: the first account created is an
# administrator and can mint its own HTTP password, so nothing here is a secret of ours. The password it
# writes to $GITHUB_ENV belongs to a container that is gone when the job ends.
#
#   GERRIT_URL      server root, default http://127.0.0.1:8080
#   GERRIT_USER     username of account 1000000, which the image ships (the dev login page lists it as
#                   "admin"); it is in Administrators
#   RELAY_URL       URL the webhooks plugin posts every project's events to (docs/platform-gerrit.md)
#   HOOK_TOKEN      the ?token= the plugin appends, the secret ripple's receiver checks
#   AUTHOR_EMAIL    ripple's commit email, registered on the account so Gerrit accepts its commits
set -euo pipefail

base=${GERRIT_URL:-http://127.0.0.1:8080}
user=${GERRIT_USER:-admin}
relay=${RELAY_URL:-http://127.0.0.1:18999/hook}
token=${HOOK_TOKEN:-ci-hook-secret}
email=${AUTHOR_EMAIL:-ripple@users.noreply.invalid}
work=$(mktemp -d)
jar=$work/jar

# body of a Gerrit JSON answer without the )]}' prefix
strip() { sed -e "1{/^)]}'/d}"; }

echo "--- version"
curl -fsS "$base/config/server/version" | strip

echo "--- become the image's own administrator (account 1000000, offered by the dev login page)"
curl -sS -c "$jar" -o "$work/login.out" -w 'login: %{http_code}\n' -L "$base/login/%23%2F?account_id=1000000" || true
grep -q GerritAccount "$jar" || { echo "no GerritAccount cookie: $(head -c 300 "$work/login.out")"; exit 1; }
echo "--- cookie jar names: $(awk 'NF>=7{print $6}' "$jar" | tr '\n' ' ')"
curl -sS -b "$jar" -c "$jar" -o /dev/null -w 'GET accounts/self (cookie): %{http_code}\n' "$base/accounts/self"
echo "--- cookie jar names after a REST call: $(awk 'NF>=7{print $6}' "$jar" | tr '\n' ' ')"
xsrf=$(awk '$6=="XSRF_TOKEN"{print $7}' "$jar")

echo "--- mint an HTTP password"
code=$(curl -sS -b "$jar" -H "X-Gerrit-Auth: $xsrf" -H 'Content-Type: application/json' -X PUT \
  -d '{"generate":true}' -o "$work/pass.out" -w '%{http_code}' "$base/accounts/self/password.http")
echo "PUT password.http: $code"
if [ "$code" != 200 ]; then echo "body: $(head -c 300 "$work/pass.out")"; exit 1; fi
strip < "$work/pass.out" | tr -d '"' > "$work/pass"
pass=$(cat "$work/pass")
[ -n "$pass" ] || { echo "empty password"; exit 1; }
echo "::add-mask::$pass"

api() { # api METHOD PATH [JSON]: prints the answer, fails on an error status
  local m=$1 p=$2 d=${3:-}
  if [ -n "$d" ]; then
    curl -fsS -u "$user:$pass" -H 'Content-Type: application/json' -X "$m" -d "$d" "$base/a$p" | strip
  else
    curl -fsS -u "$user:$pass" -X "$m" "$base/a$p" | strip
  fi
}

echo "--- who am I"
api GET /accounts/self

echo "--- register ripple's commit email so Gerrit accepts its commits"
api PUT "/accounts/self/emails/$(printf %s "$email" | sed 's/@/%40/')" '{"no_confirmation":true}'

echo "--- plugins"
api GET "/plugins/?all"

echo "--- let Administrators push and create branches directly, and edit refs/meta/config"
gid=$(api GET /groups/Administrators | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$gid" ] || { echo "no Administrators group id"; exit 1; }
api POST /projects/All-Projects/access "{\"add\":{
  \"refs/heads/*\":{\"permissions\":{
    \"push\":{\"rules\":{\"$gid\":{\"action\":\"ALLOW\",\"force\":true}}},
    \"create\":{\"rules\":{\"$gid\":{\"action\":\"ALLOW\"}}}}},
  \"refs/meta/config\":{\"permissions\":{
    \"push\":{\"rules\":{\"$gid\":{\"action\":\"ALLOW\"}}}}}}}" > /dev/null

echo "--- webhooks.config in All-Projects: every project posts to the relay"
auth=$(printf '%s:%s' "$user" "$pass" | base64 -w0)
echo "::add-mask::$auth"
g() { git -C "$work/meta" -c http.extraHeader="Authorization: Basic $auth" "$@"; }
git init -q "$work/meta"
g fetch -q "$base/a/All-Projects" refs/meta/config
g checkout -q FETCH_HEAD
cat > "$work/meta/webhooks.config" <<EOF
[remote "relay"]
  url = $relay?token=$token
  event = ref-updated
  maxTries = 3
  retryInterval = 500
EOF
git -C "$work/meta" add webhooks.config
git -C "$work/meta" -c user.name=ci -c user.email=ci@example.invalid commit -q -m "ripple CI: post ref-updated events to the relay"
g push -q "$base/a/All-Projects" HEAD:refs/meta/config

{
  echo "RIPPLE_TEST_GERRIT_URL=$base"
  echo "RIPPLE_TEST_GERRIT_USER=$user"
  echo "RIPPLE_TEST_GERRIT_PASSWORD=$pass"
} >> "${GITHUB_ENV:-/dev/null}"
echo "bootstrap done"
