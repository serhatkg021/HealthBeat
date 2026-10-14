#!/usr/bin/env bash
# Bir sürüm hattının artifact'larını hazırlar ve sürüm tutarlılığını denetler. Proje İKİ bağımsız sürüm hattıyla
# yayınlanır (docs/DISTRIBUTION.md):
#
#   agent   etiket agent/vX.Y.Z    statik binary'ler, tarball'lar, .deb/.rpm, SHA256SUMS (+ GPG imzası)
#   server  etiket server/vX.Y.Z   server + panel + init imajları (imajları GitHub Actions yayınlar) ve kurulum paketi
#                                  (healthbeat-server_X.Y.Z.tar.gz: compose + .env.example + init-env.sh), SHA256SUMS (+ imza)
#
# Sunuculara hiçbir şey dağıtmaz ve hiçbir şey yayınlamaz: çıktı dist/<hat>-vX.Y.Z/ altındadır.
#
#   scripts/release.sh agent  1.3.1                       # sürüm denetimi + test + build + paket + özet
#   scripts/release.sh agent  1.3.1 --arches "amd64"      # yalnızca bir mimari
#   GPG_KEY_ID=ABCD1234 scripts/release.sh agent 1.3.1    # SHA256SUMS'ı imzala
#   GITHUB_REPOSITORY_OWNER=sahip GITHUB_REPOSITORY=sahip/repo \
#     scripts/release.sh server 1.1.0                     # sürüm denetimi + test + panel build + notlar + kurulum paketi
#   scripts/release.sh server 1.1.0 --check               # yalnızca sürüm tutarlılığı (hızlı; test/build yok)
#
# Ortam: GPG_KEY_ID, GPG_PASSPHRASE (isteğe bağlı), NFPM, MAINTAINER, HOMEPAGE, SOURCE_DATE_EPOCH,
#        GITHUB_REPOSITORY(_OWNER) (server için zorunlu; Actions kendiliğinden verir),
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

if [[ "$COMPONENT" == agent ]]; then
  v="$(src_var agent/internal/version/version.go Version)"
  [[ "$v" == "$VERSION" ]] || die "agent/internal/version/version.go says '$v', not $VERSION (bump it first)"
  # Panelin "güncel agent" saydığı sürüm server'a gömülü değildir: agent yayınlandıktan sonra panelden (Ayarlar) girilir.
else
  v="$(src_var server/internal/version/version.go Version)"
  [[ "$v" == "$VERSION" ]] || die "server/internal/version/version.go says '$v', not $VERSION (bump it first)"
  # Panel server'la birlikte yayınlanır ve aynı sürümü taşır.
  pv="$(json_version server/panel/package.json)"
  [[ "$pv" == "$VERSION" ]] || die "server/panel/package.json version is '$pv', not $VERSION (bump it first; also server/panel/package-lock.json)"
  lv="$(json_version server/panel/package-lock.json)"
  [[ "$lv" == "$VERSION" ]] || die "server/panel/package-lock.json version is '$lv', not $VERSION (bump it together with package.json)"
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

# sums_and_sign: $DIST'teki artifact'ların SHA256SUMS'ını yazar ve GPG_KEY_ID varsa imzalar (iki hat da kullanır).
sums_and_sign() {
  (cd "$DIST" && ls -1 | grep -v -E '^(SHA256SUMS|SHA256SUMS\.asc|RELEASE_NOTES\.md)$' | LC_ALL=C sort | xargs sha256sum >SHA256SUMS)
  if [[ -n "${GPG_KEY_ID:-}" ]]; then
    info "signing SHA256SUMS with key $GPG_KEY_ID"
    local gpg_args=(--batch --yes --local-user "$GPG_KEY_ID" --armor --detach-sign --output "$DIST/SHA256SUMS.asc")
    if [[ -n "${GPG_PASSPHRASE:-}" ]]; then
      printf '%s' "$GPG_PASSPHRASE" | gpg "${gpg_args[@]}" --pinentry-mode loopback --passphrase-fd 0 "$DIST/SHA256SUMS"
    else
      gpg "${gpg_args[@]}" "$DIST/SHA256SUMS"
    fi
    gpg --batch --verify "$DIST/SHA256SUMS.asc" "$DIST/SHA256SUMS" 2>/dev/null || die "the fresh signature does not verify"
  else
    info "no GPG_KEY_ID set: SHA256SUMS is NOT signed (fine for a local trial; releases must be signed)"
  fi
}

# reproducible_tar DİZİN AD ÇIKTI: DİZİN/AD'ı her üretimde aynı baytlarla tar.gz'ler (sıra, zaman, sahip sabit).
reproducible_tar() {
  tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
      -C "$1" -cf - "$2" | gzip -n -9 >"$3"
}

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
  # İmaj adresi ve depo, notlara ve kurulum paketine yazılır; Actions ikisini de kendiliğinden verir, yerelde elle ver.
  [[ -n "${GITHUB_REPOSITORY_OWNER:-}" && -n "${GITHUB_REPOSITORY:-}" ]] ||
    die "set GITHUB_REPOSITORY_OWNER and GITHUB_REPOSITORY (e.g. owner and owner/repo): the bundle needs the image registry"
  owner="${GITHUB_REPOSITORY_OWNER,,}"
  {
    scripts/release-notes.sh server "$VERSION"
    printf '\n## İmajlar\n\n'
    printf '```\nghcr.io/%s/healthbeat-server:%s\nghcr.io/%s/healthbeat-panel:%s\nghcr.io/%s/healthbeat-init:%s\n```\n' \
      "$owner" "$VERSION" "$owner" "$VERSION" "$owner" "$VERSION"
    printf '\nKurulum/güncelleme: `healthbeat-server_%s.tar.gz` (içindeki `KURULUM.md`; `SHA256SUMS` + `.asc` ile doğrula), ' "$VERSION"
    printf 'ayrıntı `docs/DEPLOYMENT.md` ve `docs/DISTRIBUTION.md` bölüm 8. '
    printf 'Agent bu sürümden bağımsızdır: agent sürümleri `agent/vX.Y.Z` etiketiyle ayrıca yayınlanır.\n'
  } >"$DIST/RELEASE_NOTES.md"

  # --- Kurulum paketi: kaynak kod olmadan Docker Compose ile kurulum ve güncelleme (deploy/KURULUM.md).
  registry="ghcr.io/$owner"
  repo="$GITHUB_REPOSITORY"
  bundle="healthbeat-server_$VERSION.tar.gz"
  stage="$DIST/.build/healthbeat"
  mkdir -p "$stage/deploy"
  # build: satırları yalnızca kaynak koddan derlerken anlamlı; pakette bir imaj bulunamazsa derlemeye kalkmasın.
  # Yalnızca tek satırlık `build: <dizin>` silinebilir; çok satırlısı (context:, dockerfile:) yarım YAML bırakırdı.
  ! grep -q -E '^[[:space:]]+build:[[:space:]]*$' docker-compose.yml ||
    die "bundle: docker-compose.yml has a multi-line build: (only 'build: <dir>' can be stripped)"
  {
    printf '# healthbeat-server_%s kurulum paketinden (KURULUM.md): build: satırları çıkarıldı, imajlar .env'"'"'deki\n' "$VERSION"
    printf '# HB_REGISTRY / HB_VERSION'"'"'dan çekilir. Kendi ayarların için docker-compose.override.yml kullan.\n#\n'
    grep -v -E '^[[:space:]]+build:' docker-compose.yml
  } >"$stage/docker-compose.yml"
  sed -e "s|^# HB_REGISTRY=\$|HB_REGISTRY=$registry|" -e "s|^# HB_VERSION=\$|HB_VERSION=$VERSION|" .env.example >"$stage/.env.example"
  sed -e "s|@VERSION@|$VERSION|g" -e "s|@REGISTRY@|$registry|g" -e "s|@REPO@|$repo|g" deploy/KURULUM.md >"$stage/KURULUM.md"
  install -m 0755 deploy/init-env.sh "$stage/deploy/init-env.sh"
  install -m 0644 LICENSE "$stage/LICENSE"
  chmod 0644 "$stage/docker-compose.yml" "$stage/.env.example" "$stage/KURULUM.md"
  chmod 0755 "$stage" "$stage/deploy"   # umask'a bağlı kalmasın: yerel ve CI aynı baytları üretsin
  ! grep -q -E '^[[:space:]]+build:' "$stage/docker-compose.yml" || die "bundle: docker-compose.yml still has build: lines"
  grep -qx "HB_VERSION=$VERSION" "$stage/.env.example" || die "bundle: .env.example has no 'HB_VERSION=$VERSION' (is '# HB_VERSION=' still in .env.example?)"
  grep -qx "HB_REGISTRY=$registry" "$stage/.env.example" || die "bundle: .env.example has no 'HB_REGISTRY=$registry'"
  ! grep -q -E '@(VERSION|REGISTRY|REPO)@' "$stage/KURULUM.md" || die "bundle: KURULUM.md has unfilled placeholders"
  reproducible_tar "$DIST/.build" healthbeat "$DIST/$bundle"
  rm -rf "$DIST/.build"
  sums_and_sign

  info "done: $DIST (server $VERSION, commit $COMMIT)"
  (cd "$DIST" && ls -l | awk 'NR>1 {printf "   %-58s %10d\n", $9, $5}')
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
  install -m 0644 docs/AGENT.md docs/COMPATIBILITY.md docs/DISTRIBUTION.md LICENSE "$stage/"
  printf '%s\n' "$VERSION" >"$stage/VERSION"
  reproducible_tar "$DIST/.build" "$name" "$DIST/$name.tar.gz"

  info "packaging $arch (.deb, .rpm)"
  agent/packaging/build.sh "$arch" "$VERSION" "$DIST" "$bin" >/dev/null
  cp "$bin" "$DIST/healthbeat-agent_${VERSION}_linux_${arch}"
done
rm -rf "$DIST/.build"

# --- Sürüm notları, özet, imza
scripts/release-notes.sh agent "$VERSION" >"$DIST/RELEASE_NOTES.md"
sums_and_sign

info "done: $DIST"
(cd "$DIST" && ls -l | awk 'NR>1 {printf "   %-58s %10d\n", $9, $5}')
