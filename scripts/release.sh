#!/usr/bin/env bash
# Bir sürüm hattının artifact'larını hazırlar ve sürüm tutarlılığını denetler. Proje İKİ bağımsız sürüm hattıyla
# yayınlanır (docs/DISTRIBUTION.md):
#
#   agent   etiket agent/vX.Y.Z    statik binary'ler, tarball'lar, .deb/.rpm, SHA256SUMS (+ GPG imzası)
#   server  etiket server/vX.Y.Z   server + panel + certs-init imajları (imajları GitHub Actions yayınlar)
#
# Sunuculara hiçbir şey dağıtmaz ve hiçbir şey yayınlamaz: çıktı dist/<hat>-vX.Y.Z/ altındadır.
#
#   scripts/release.sh agent  1.3.1                       # sürüm denetimi + test + build + paket + özet
#   scripts/release.sh agent  1.3.1 --arches "amd64"      # yalnızca bir mimari
#   GPG_KEY_ID=ABCD1234 scripts/release.sh agent 1.3.1    # SHA256SUMS'ı imzala
#   scripts/release.sh server 1.1.0                       # sürüm denetimi + test + panel build + sürüm notları
#   scripts/release.sh server 1.1.0 --check               # yalnızca sürüm tutarlılığı (hızlı; test/build yok)
#
# Ortam: GPG_KEY_ID, GPG_PASSPHRASE (isteğe bağlı), NFPM, MAINTAINER, HOMEPAGE, SOURCE_DATE_EPOCH,
#        TEST_DATABASE_URL (server testleri için; yoksa veritabanı testleri atlanır).
# Seçenekler: --check (yalnızca tutarlılık), --skip-tests, --allow-dirty (kirli çalışma ağacına izin ver),
# --require-tag (HEAD, hattın etiketinde olmalı; CI kullanır), --arches "amd64 arm64" (agent), --out DIR.
set -euo pipefail
export LC_ALL=C

die() { echo "release.sh: error: $*" >&2; exit 1; }
info() { echo "==> $*"; }
USAGE='usage: release.sh agent|server X.Y.Z [--check] [--skip-tests] [--allow-dirty] [--require-tag] [--arches "amd64 arm64"] [--out DIR]'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPONENT="${1:-}"; [[ $# -gt 0 ]] && shift
[[ "$COMPONENT" == agent || "$COMPONENT" == server ]] || die "$USAGE"
VERSION="${1:-}"; [[ $# -gt 0 ]] && shift
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "$USAGE"

CHECK_ONLY=0 SKIP_TESTS=0 ALLOW_DIRTY=0 REQUIRE_TAG=0 ARCHES="amd64 arm64" OUT=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) CHECK_ONLY=1; shift ;;
    --skip-tests) SKIP_TESTS=1; shift ;;
    --allow-dirty) ALLOW_DIRTY=1; shift ;;
    --require-tag) REQUIRE_TAG=1; shift ;;
    --arches) [[ $# -ge 2 ]] || die "--arches needs a value"; ARCHES="$2"; shift 2 ;;
    --out) [[ $# -ge 2 ]] || die "--out needs a value"; OUT="$2"; shift 2 ;;
    *) die "unknown option '$1'" ;;
  esac
done
for a in $ARCHES; do [[ "$a" == amd64 || "$a" == arm64 ]] || die "unsupported architecture '$a' (amd64 or arm64)"; done

TAG="$COMPONENT/v$VERSION"
DIST="${OUT:-$ROOT/dist/$COMPONENT-v$VERSION}"
cd "$ROOT"

# --- 1) Sürüm tutarlılığı: kaynaktaki sürümler, değişiklik günlüğü ve (isteniyorsa) etiket aynı olmalı.
src_var() { sed -n "s/^var $2 = \"\(.*\)\"\$/\1/p" "$1"; }                       # Go: var Version = "1.0.0"
json_version() { awk -F'"' '/"version"[[:space:]]*:/ { print $4; exit }' "$1"; }  # ilk "version" alanı
semver_ok() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; }
tag_exists() { git rev-parse -q --verify "refs/tags/$1" >/dev/null 2>&1; }

if [[ "$COMPONENT" == agent ]]; then
  v="$(src_var agent/internal/version/version.go Version)"
  [[ "$v" == "$VERSION" ]] || die "agent/internal/version/version.go says '$v', not $VERSION (bump it first)"
  # Yeni agent yayınlanırken server'ın "en güncel agent" bilgisi de aynı commit'te güncellenmiş olmalı; aksi halde
  # sonraki server derlemesi bu agent'ı bilmez ve panel onu "güncel" saymaz.
  la="$(src_var server/internal/version/version.go LatestAgent)"
  [[ "$la" == "$VERSION" ]] || die "server/internal/version/version.go LatestAgent is '$la', not $VERSION (bump it in the same commit)"
else
  v="$(src_var server/internal/version/version.go Version)"
  [[ "$v" == "$VERSION" ]] || die "server/internal/version/version.go says '$v', not $VERSION (bump it first)"
  # Panel server'la birlikte yayınlanır ve aynı sürümü taşır.
  pv="$(json_version server/panel/package.json)"
  [[ "$pv" == "$VERSION" ]] || die "server/panel/package.json version is '$pv', not $VERSION (bump it first; also server/panel/package-lock.json)"
  lv="$(json_version server/panel/package-lock.json)"
  [[ "$lv" == "$VERSION" ]] || die "server/panel/package-lock.json version is '$lv', not $VERSION (bump it together with package.json)"
  # Server'ın panelde "güncel agent" saydığı sürüm gerçekten yayınlanmış bir agent olmalı.
  la="$(src_var server/internal/version/version.go LatestAgent)"
  semver_ok "$la" || die "server/internal/version/version.go LatestAgent '$la' is not SemVer"
  if ! tag_exists "agent/v$la"; then
    die "LatestAgent is $la but no agent release tag (agent/v$la) exists: release that agent first, or fix LatestAgent"
  fi
fi
scripts/release-notes.sh "$COMPONENT" "$VERSION" >/dev/null || die "the $COMPONENT changelog has no '## [$VERSION]' section"

if [[ $ALLOW_DIRTY -eq 0 && -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]]; then
  die "the working tree has uncommitted changes (commit them, or pass --allow-dirty for a local trial build)"
fi
if [[ $REQUIRE_TAG -eq 1 ]]; then
  [[ "$(git rev-parse "$TAG^{commit}" 2>/dev/null)" == "$(git rev-parse HEAD)" ]] || die "HEAD is not tagged $TAG"
fi
if [[ $CHECK_ONLY -eq 1 ]]; then
  info "$COMPONENT $VERSION: version consistency OK"
  exit 0
fi

# Yeniden üretilebilirlik: zaman damgaları commit zamanından gelir.
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}"
COMMIT="$(git rev-parse --short HEAD)"

# =========================== server + panel hattı ===========================
if [[ "$COMPONENT" == server ]]; then
  if [[ $SKIP_TESTS -eq 0 ]]; then
    info "server tests (go vet, go test${TEST_DATABASE_URL:+ with database})"
    [[ -n "${TEST_DATABASE_URL:-}" ]] || echo "    note: TEST_DATABASE_URL is not set: database-backed tests are skipped (CI runs them)"
    (cd server && go vet ./... && go test ./...) >/dev/null
    info "panel checks (tsc, test, build)"
    (cd server/panel && npx tsc -b && npm test && npm run build) >/dev/null
  fi
  rm -rf "$DIST"; mkdir -p "$DIST"
  owner="${GITHUB_REPOSITORY_OWNER:-<sahip>}"; owner="${owner,,}"
  {
    scripts/release-notes.sh server "$VERSION"
    printf '\n## İmajlar\n\n'
    printf '```\nghcr.io/%s/healthbeat-server:%s\nghcr.io/%s/healthbeat-panel:%s\nghcr.io/%s/healthbeat-certs-init:%s\n```\n' \
      "$owner" "$VERSION" "$owner" "$VERSION" "$owner" "$VERSION"
    printf '\nKurulum/güncelleme: `docs/DEPLOYMENT.md` ve `docs/DISTRIBUTION.md` bölüm 8 (`HB_VERSION=%s`). ' "$VERSION"
    printf 'Agent bu sürümden bağımsızdır: agent sürümleri `agent/vX.Y.Z` etiketiyle ayrıca yayınlanır.\n'
  } >"$DIST/RELEASE_NOTES.md"
  info "done: $DIST (server $VERSION, commit $COMMIT)"
  exit 0
fi

# ================================ agent hattı ================================
if [[ $SKIP_TESTS -eq 0 ]]; then
  info "agent tests (go vet, go test, install_test.sh)"
  (cd agent && go vet ./... && go test ./...) >/dev/null
  agent/deploy/install_test.sh >/dev/null
fi

rm -rf "$DIST"; mkdir -p "$DIST/.build"
info "building healthbeat-agent $VERSION ($COMMIT) for: $ARCHES"
for arch in $ARCHES; do
  bin="$DIST/.build/healthbeat-agent_linux_$arch"
  (cd agent && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "$bin" ./cmd/agent)

  # tarball: install.sh ile tarball kurulumu (paket sistemi olmayan dağıtımlar için)
  name="healthbeat-agent_${VERSION}_linux_${arch}"
  stage="$DIST/.build/$name"; mkdir -p "$stage"
  install -m 0755 "$bin" "$stage/healthbeat-agent"
  install -m 0755 agent/deploy/install.sh "$stage/install.sh"
  install -m 0644 agent/deploy/healthbeat-agent.service "$stage/healthbeat-agent.service"
  install -m 0644 docs/AGENT.md docs/COMPATIBILITY.md docs/DISTRIBUTION.md "$stage/"
  printf '%s\n' "$VERSION" >"$stage/VERSION"
  tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
      -C "$DIST/.build" -cf - "$name" | gzip -n -9 >"$DIST/$name.tar.gz"

  info "packaging $arch (.deb, .rpm)"
  agent/packaging/build.sh "$arch" "$VERSION" "$DIST" "$bin" >/dev/null
  cp "$bin" "$DIST/healthbeat-agent_${VERSION}_linux_${arch}"
done
rm -rf "$DIST/.build"

# --- Sürüm notları, özet, imza
scripts/release-notes.sh agent "$VERSION" >"$DIST/RELEASE_NOTES.md"
(cd "$DIST" && ls -1 | grep -v -E '^(SHA256SUMS|SHA256SUMS\.asc|RELEASE_NOTES\.md)$' | LC_ALL=C sort | xargs sha256sum >SHA256SUMS)

if [[ -n "${GPG_KEY_ID:-}" ]]; then
  info "signing SHA256SUMS with key $GPG_KEY_ID"
  gpg_args=(--batch --yes --local-user "$GPG_KEY_ID" --armor --detach-sign --output "$DIST/SHA256SUMS.asc")
  if [[ -n "${GPG_PASSPHRASE:-}" ]]; then
    printf '%s' "$GPG_PASSPHRASE" | gpg "${gpg_args[@]}" --pinentry-mode loopback --passphrase-fd 0 "$DIST/SHA256SUMS"
  else
    gpg "${gpg_args[@]}" "$DIST/SHA256SUMS"
  fi
  gpg --batch --verify "$DIST/SHA256SUMS.asc" "$DIST/SHA256SUMS" 2>/dev/null || die "the fresh signature does not verify"
else
  info "no GPG_KEY_ID set: SHA256SUMS is NOT signed (fine for a local trial; releases must be signed)"
fi

info "done: $DIST"
(cd "$DIST" && ls -l | awk 'NR>1 {printf "   %-58s %10d\n", $9, $5}')
