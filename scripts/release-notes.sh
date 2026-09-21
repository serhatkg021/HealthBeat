#!/usr/bin/env bash
# Bir bileşenin değişiklik günlüğünden bir sürümün notlarını yazdırır (GitHub release gövdesi için).
#   scripts/release-notes.sh agent  1.3.1     # agent/CHANGELOG.md
#   scripts/release-notes.sh server 1.1.0     # server/CHANGELOG.md
set -euo pipefail
export LC_ALL=C
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
usage() { echo "usage: release-notes.sh agent|server X.Y.Z" >&2; exit 2; }
COMPONENT="${1:-}"; VERSION="${2:-}"
case "$COMPONENT" in
  agent) FILE="$ROOT/agent/CHANGELOG.md" ;;
  server) FILE="$ROOT/server/CHANGELOG.md" ;;
  *) usage ;;
esac
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || usage
[[ -f "$FILE" ]] || { echo "release-notes.sh: $FILE not found" >&2; exit 1; }

notes="$(awk -v v="$VERSION" '
  $0 ~ "^## \\[" v "\\]" { on = 1; next }
  on && /^## \[/ { exit }
  on { print }
' "$FILE")"
# baştaki/sondaki boş satırları at
notes="$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')"
[[ -n "$notes" ]] || { echo "release-notes.sh: no '## [$VERSION]' section (or it is empty) in ${FILE#"$ROOT/"}" >&2; exit 1; }
printf '%s\n' "$notes"
