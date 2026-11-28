#!/usr/bin/env bash
# Fails when a file about to be uploaded as a workflow artifact holds a secret of the job. The values are
# never printed: only the variable's name and the file are.
#
#   ci-check-artifacts.sh VAR_NAMES FILE_OR_DIR...
#
# VAR_NAMES is a space-separated list of environment variables whose values must not appear ("-" when the job
# has none, so only the generic patterns below are checked). Independently of
# them it rejects any "::add-mask::" workflow command (it carries the raw value) and any "glrt-" token (the
# GitLab runner token is not in the environment).
set -uo pipefail
vars=${1:?variable names, or - for none}
shift
[ "$vars" = "-" ] && vars=""
bad=0
for f in "$@"; do
  [ -e "$f" ] || continue
  if grep -rqF -- '::add-mask::' "$f"; then echo "$f holds an ::add-mask:: command"; bad=1; fi
  if grep -rqF -- 'glrt-' "$f"; then echo "$f holds a glrt- runner token"; bad=1; fi
  for v in $vars; do
    val=${!v:-}
    # an empty variable would make the grep below vacuous: the guard must know what it is guarding against
    [ -n "$val" ] || { echo "$v is empty, so the check against it proves nothing"; bad=1; continue; }
    if grep -rqF -- "$val" "$f"; then echo "$f holds the value of $v"; bad=1; fi
  done
done
[ $bad -eq 0 ] && echo "artifact files are free of the job's secrets"
exit $bad
