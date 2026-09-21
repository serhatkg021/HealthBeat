#!/usr/bin/env bash
# Verilen binary'den .deb ve .rpm üretir (nfpm).
#
#   agent/packaging/build.sh ARCH VERSION OUTDIR BINARY
#     ARCH     amd64 | arm64 (Go mimari adı; nfpm .deb/.rpm karşılığına çevirir)
#     VERSION  SemVer (1.0.0 ya da 1.0.0-rc.1: önsürüm .deb'de '~' ile sıralanır)
#     OUTDIR   paketlerin yazılacağı dizin
#     BINARY   bu mimari için derlenmiş healthbeat-agent
#
# Ortam: NFPM (nfpm yolu; yoksa PATH / ~/go/bin), MAINTAINER, HOMEPAGE, SOURCE_DATE_EPOCH.
# scripts/release.sh ve agent/packaging/test_packages.sh bunu kullanır.
set -euo pipefail
export LC_ALL=C

die() { echo "build.sh: error: $*" >&2; exit 1; }
[[ $# -eq 4 ]] || die "usage: build.sh ARCH VERSION OUTDIR BINARY"
ARCH="$1" VERSION="$2" OUT="$3" BINARY="$4"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[[ "$ARCH" == amd64 || "$ARCH" == arm64 ]] || die "ARCH must be amd64 or arm64"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "VERSION must be SemVer, got '$VERSION'"
[[ -x "$BINARY" ]] || die "binary not found or not executable: $BINARY"

NFPM="${NFPM:-$(command -v nfpm || true)}"
[[ -n "$NFPM" ]] || NFPM="$HOME/go/bin/nfpm"
[[ -x "$NFPM" ]] || die "nfpm not found (go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.41.1, or set NFPM=/path/to/nfpm)"

mkdir -p "$OUT"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# Paketin unit'i, repodakinin (tarball kurulumunun) aynısıdır; tek fark binary'nin yeri: /usr/bin.
sed 's#^ExecStart=/usr/local/bin/healthbeat-agent #ExecStart=/usr/bin/healthbeat-agent #' \
  "$ROOT/agent/deploy/healthbeat-agent.service" >"$STAGE/healthbeat-agent.service"
grep -q '^ExecStart=/usr/bin/healthbeat-agent ' "$STAGE/healthbeat-agent.service" \
  || die "could not rewrite ExecStart in the unit (did its format change?)"
printf 'This agent was installed from a package (.deb/.rpm); the package manager owns its files.\n' >"$STAGE/installed-from-package"

export GOARCH="$ARCH" VERSION PKG_BINARY="$BINARY" PKG_STAGE="$STAGE"
export MAINTAINER="${MAINTAINER:-HealthBeat Maintainers <maintainers@healthbeat.invalid>}"
export HOMEPAGE="${HOMEPAGE:-https://github.com/serhatkg021/HealthBeat}"
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct 2>/dev/null || date +%s)}"

# nfpm ortam değişkenlerini `contents.src` içinde açmaz; şablonu burada çözüp nfpm'e hazır veririz.
python3 - "$ROOT/agent/packaging/nfpm.yaml" "$STAGE/nfpm.yaml" <<'PY'
import os, re, sys
src, dst = sys.argv[1:3]
text = open(src).read()
missing = []
def sub(m):
    name = m.group(1)
    if name not in os.environ:
        missing.append(name)
        return m.group(0)
    return os.environ[name]
text = re.sub(r"\$\{([A-Z_]+)\}", sub, text)
if missing:
    sys.exit("nfpm.yaml uses undefined variables: " + ", ".join(sorted(set(missing))))
open(dst, "w").write(text)
PY

cd "$ROOT"
for packager in deb rpm; do
  if ! log="$("$NFPM" package --packager "$packager" --config "$STAGE/nfpm.yaml" --target "$OUT/" 2>&1)"; then
    die "nfpm ($packager) failed: $log"
  fi
done
ls -1 "$OUT" | grep -E "healthbeat-agent.*\.(deb|rpm)$" | sed "s#^#$OUT/#"
