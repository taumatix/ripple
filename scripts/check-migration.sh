#!/usr/bin/env bash
# Fails when a `ripple:deprecated <id>` marker in Go code has no MIGRATION.md entry, or an
# active entry (a "**Marker:** `id`" line above the "Removed" heading) has no marker in code.
set -euo pipefail
cd "$(dirname "$0")/.."

code=$(grep -rhoE --include='*.go' 'ripple:deprecated [a-z0-9-]+' cmd internal | awk '{print $2}' | sort -u)
active=$(awk '/^## Removed/{exit} /^- \*\*Marker:\*\* `/{gsub(/.*`([^`]*)`.*/,"&"); match($0,/`[^`]+`/); print substr($0,RSTART+1,RLENGTH-2)}' MIGRATION.md | sort -u)

rc=0
for id in $code; do
  grep -qx "$id" <<<"$active" || { echo "marker '$id' in code has no active MIGRATION.md entry" >&2; rc=1; }
done
for id in $active; do
  grep -qx "$id" <<<"$code" || { echo "MIGRATION.md entry '$id' has no ripple:deprecated marker in code" >&2; rc=1; }
done
[ "$rc" = 0 ] && echo "migration check ok: $(wc -w <<<"$active") active deprecation(s)"
exit $rc
