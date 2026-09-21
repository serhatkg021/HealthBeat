#!/usr/bin/env bash
# Paket testleri: .deb/.rpm'yi GERÇEK dağıtım imajlarında (Docker) kurar ve dosya yerleşimini, kullanıcıyı,
# izinleri, unit'i, yapılandırma sihirbazını, yükseltmede config korunmasını, kaldırmayı ve purge'ü doğrular.
#
#   agent/packaging/test_packages.sh                         # tüm dağıtımlar
#   agent/packaging/test_packages.sh ubuntu:24.04 rockylinux:9   # seçilenler
#
# SINIR: konteynerde systemd çalışmaz. Servis işlemleri (enable/start/restart) atlanır (paket script'leri
# /run/systemd/system'a bakar) ve `systemctl` için bir taklit konur. Servisin gerçekten kalkması, yükseltmede
# yeniden başlaması ve SELinux davranışı gerçek bir VM ister (docs/DISTRIBUTION.md, "Doğrulama sınırı").
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
ROOT="$PWD"

DEB_IMAGES=(ubuntu:22.04 ubuntu:24.04 debian:12)
RPM_IMAGES=(rockylinux:9 almalinux:8 amazonlinux:2023)
if [[ $# -gt 0 ]]; then IMAGES=("$@"); else IMAGES=("${DEB_IMAGES[@]}" "${RPM_IMAGES[@]}"); fi

command -v docker >/dev/null || { echo "docker gerekli"; exit 2; }
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
FAILS=0 PASSES=0

echo "building two package versions (1.2.0 and 1.2.1) for amd64..."
V1_VER=1.2.0; V2_VER=1.2.1
for v in $V1_VER $V2_VER; do
  ( cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X healthbeat-agent/internal/version.Version=$v" -o "$WORK/hb-$v" ./cmd/agent ) || exit 2
  agent/packaging/build.sh amd64 "$v" "$WORK/pkg-$v" "$WORK/hb-$v" >/dev/null || exit 2
done

# Konteyner içinde çalışan sınama betiği (bash). Her satır "PASS ad" ya da "FAIL ad: ayrıntı".
read -r -d '' INNER <<'INNER_EOF'
set -u
KIND="$1"; V1="$2"; V2="$3"
ok()   { echo "PASS $1"; }
bad()  { echo "FAIL $1: ${2:-}"; }
check() { local name="$1"; shift; if "$@" >/dev/null 2>&1; then ok "$name"; else bad "$name"; fi; }

# systemd yok: systemctl'ı taklit et (install.sh'ın daemon-reload/enable çağrıları için)
printf '#!/bin/sh\nexit 0\n' >/usr/local/bin/systemctl; chmod +x /usr/local/bin/systemctl

if [ "$KIND" = deb ]; then
  export DEBIAN_FRONTEND=noninteractive
  # Docker imajları /usr/share/doc'u kurulumdan hariç tutar (imajı küçültmek için); gerçek sunucuda böyle değildir.
  rm -f /etc/dpkg/dpkg.cfg.d/excludes /etc/dpkg/dpkg.cfg.d/docker
  pkg_install() { apt-get install -y -qq "$@" 2>&1; }
  pkg_downgrade() { apt-get install -y -qq --allow-downgrades "$@" 2>&1; }
  pkg_remove()  { apt-get remove -y -qq healthbeat-agent 2>&1; }
  pkg_purge()   { apt-get purge -y -qq healthbeat-agent 2>&1; }
  pkg_verify()  { dpkg --verify healthbeat-agent; }
  UNIT=/lib/systemd/system/healthbeat-agent.service
  PKGFILE1="$(ls /pkg1/*.deb)"; PKGFILE2="$(ls /pkg2/*.deb)"
else
  pkg_install() { dnf install -y "$@" 2>&1; }
  pkg_downgrade() { dnf downgrade -y "$@" 2>&1; }
  pkg_remove()  { dnf remove -y healthbeat-agent 2>&1; }
  pkg_purge()   { pkg_remove; }
  pkg_verify()  { rpm -V healthbeat-agent; }
  UNIT=/usr/lib/systemd/system/healthbeat-agent.service
  PKGFILE1="$(ls /pkg1/*.rpm)"; PKGFILE2="$(ls /pkg2/*.rpm)"
fi

# --- tarball kurulumundan kalan dosya paketle çakışır: uyarı verilmeli
mkdir -p /usr/local/bin; printf '#!/bin/sh\n' >/usr/local/bin/healthbeat-agent; chmod +x /usr/local/bin/healthbeat-agent
out="$(pkg_install "$PKGFILE1")"; rc=$?
[ $rc -eq 0 ] && ok "install v$V1" || { bad "install v$V1" "$out"; exit 0; }
case "$out" in *"tarball (install.sh) installation is still present"*) ok "warns about a leftover tarball install" ;; *) bad "warns about a leftover tarball install" "$out" ;; esac
rm -f /usr/local/bin/healthbeat-agent

case "$(/usr/bin/healthbeat-agent --version 2>&1)" in *"$V1"*) ok "binary reports $V1" ;; *) bad "binary reports $V1" ;; esac
case "$out" in *"NOT configured yet"*"healthbeat-agent-setup"*) ok "tells how to configure" ;; *) bad "tells how to configure" "$out" ;; esac

# --- kullanıcı, dizin, izinler
uid="$(getent passwd healthbeat | cut -d: -f3)"; shell="$(getent passwd healthbeat | cut -d: -f7)"
[ -n "$uid" ] && [ "$uid" -lt 1000 ] && [ "$shell" = /usr/sbin/nologin ] && ok "system user healthbeat (nologin)" || bad "system user healthbeat" "uid=$uid shell=$shell"
[ "$(stat -c '%a %U:%G' /etc/healthbeat)" = "750 root:healthbeat" ] && ok "/etc/healthbeat is 750 root:healthbeat" || bad "/etc/healthbeat perms" "$(stat -c '%a %U:%G' /etc/healthbeat)"
[ -x /usr/bin/healthbeat-agent ] && [ -x /usr/sbin/healthbeat-agent-setup ] && [ -x /usr/share/healthbeat/install.sh ] && ok "binary, setup and install.sh are executable" || bad "executables"
[ -f /usr/share/healthbeat/installed-from-package ] && ok "package marker present" || bad "package marker"
[ -f /usr/share/doc/healthbeat/AGENT.md ] && ok "docs installed" || bad "docs"

# --- envanter: bu dağıtımda çalışır, doğru işletim sistemini ve konteyner ortamını bildirir
inv="$(/usr/bin/healthbeat-agent --print-inventory 2>&1)"; rc=$?
. /etc/os-release
[ $rc -eq 0 ] && ok "--print-inventory runs" || bad "--print-inventory runs" "rc=$rc $inv"
case "$inv" in *"\"id\": \"$ID\""*) ok "inventory reports the OS id ($ID)" ;; *) bad "inventory reports the OS id ($ID)" "$inv" ;; esac
case "$inv" in *'"kind": "container"'*) ok "inventory detects the container environment" ;; *) bad "inventory detects the container" "$inv" ;; esac
case "$inv" in *'"release": "'*) ok "inventory reports the kernel" ;; *) bad "inventory reports the kernel" ;; esac
case "$inv" in *serial*|*uuid*) bad "inventory has no serial/uuid" "$inv" ;; *) ok "inventory has no serial/uuid" ;; esac

# --- unit: repodaki unit'in aynısı, yalnızca ExecStart yolu farklı
[ -f "$UNIT" ] && ok "unit at $UNIT" || bad "unit at $UNIT"
expected_unit="$(sed 's#^ExecStart=/usr/local/bin/healthbeat-agent #ExecStart=/usr/bin/healthbeat-agent #' /deploy/healthbeat-agent.service)"
[ "$expected_unit" = "$(cat "$UNIT")" ] && [ "$expected_unit" != "$(cat /deploy/healthbeat-agent.service)" ] && ok "unit differs from the tarball unit only in ExecStart" || bad "unit differs only in ExecStart"
[ ! -e /etc/systemd/system/healthbeat-agent.service ] && ok "no unit shadowing in /etc/systemd/system" || bad "unit shadowing"
check "package verifies clean ($KIND)" pkg_verify

# --- paket yönetilen makinede install.sh install reddedilir; configure çalışır
out="$(/usr/share/healthbeat/install.sh install --binary /usr/bin/healthbeat-agent 2>&1)"; rc=$?
[ $rc -ne 0 ] && case "$out" in *"installed from a package"*) ok "install.sh install is refused" ;; *) bad "install.sh install is refused" "$out" ;; esac || bad "install.sh install is refused" "rc=0"
check "healthbeat-agent-setup --help" sh -c '/usr/sbin/healthbeat-agent-setup --help | grep -q "configure"'

TOKEN="AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"; UUID="123e4567-e89b-12d3-a456-426614174000"
out="$(HEALTHBEAT_API_TOKEN=$TOKEN /usr/sbin/healthbeat-agent-setup --no-start --mode push --server-url https://api.example.com --host-id $UUID 2>&1)"; rc=$?
[ $rc -eq 0 ] && ok "healthbeat-agent-setup (non-interactive)" || bad "healthbeat-agent-setup" "$out"
[ "$(stat -c '%a %U:%G' /etc/healthbeat/agent.json)" = "640 root:healthbeat" ] && ok "config is 640 root:healthbeat" || bad "config perms" "$(stat -c '%a %U:%G' /etc/healthbeat/agent.json)"
check "the agent accepts the written config" /usr/bin/healthbeat-agent -config /etc/healthbeat/agent.json -check-config
case "$out" in *"$TOKEN"*) bad "token not printed" "the API token was printed" ;; *) ok "token not printed" ;; esac
# `su` minimal imajlarda olmayabilir; izinler zaten yukarıda (640 root:healthbeat) doğrulandı.
if command -v su >/dev/null 2>&1; then
  su -s /bin/sh healthbeat -c 'cat /etc/healthbeat/agent.json' >/dev/null 2>&1 && ok "the service user can read its config" || bad "the service user can read its config"
else
  ok "the service user can read its config (skipped: no su in this image)"
fi
sum1="$(sha256sum /etc/healthbeat/agent.json | cut -d' ' -f1)"

# --- yükseltme: config ve kullanıcı korunur
out="$(pkg_install "$PKGFILE2")"; rc=$?
[ $rc -eq 0 ] && ok "upgrade to v$V2" || bad "upgrade to v$V2" "$out"
case "$(/usr/bin/healthbeat-agent --version 2>&1)" in *"$V2"*) ok "binary reports $V2 after upgrade" ;; *) bad "binary reports $V2" ;; esac
[ "$(sha256sum /etc/healthbeat/agent.json | cut -d' ' -f1)" = "$sum1" ] && ok "config untouched by the upgrade" || bad "config untouched by the upgrade"
case "$out" in *"NOT configured yet"*) bad "no 'not configured' nag on upgrade" "$out" ;; *) ok "no 'not configured' nag on upgrade" ;; esac
[ "$(stat -c '%a %U:%G' /etc/healthbeat/agent.json)" = "640 root:healthbeat" ] && ok "config perms kept after upgrade" || bad "config perms after upgrade"

# --- geri alma (downgrade): eski paket kurulur, config dokunulmaz (docs/DISTRIBUTION.md bölüm 5.3)
out="$(pkg_downgrade "$PKGFILE1")"; rc=$?
[ $rc -eq 0 ] && ok "downgrade to v$V1" || bad "downgrade to v$V1" "$out"
case "$(/usr/bin/healthbeat-agent --version 2>&1)" in *"$V1"*) ok "binary reports $V1 after downgrade" ;; *) bad "binary reports $V1 after downgrade" ;; esac
[ "$(sha256sum /etc/healthbeat/agent.json | cut -d' ' -f1)" = "$sum1" ] && ok "config untouched by the downgrade" || bad "config untouched by the downgrade"

# --- kaldırma: binary ve unit gider, yapılandırma ve kullanıcı kalır
out="$(pkg_remove)"; rc=$?
[ $rc -eq 0 ] && ok "remove" || bad "remove" "$out"
[ ! -e /usr/bin/healthbeat-agent ] && [ ! -e "$UNIT" ] && ok "binary and unit removed" || bad "binary and unit removed"
[ -f /etc/healthbeat/agent.json ] && getent passwd healthbeat >/dev/null && ok "config and user kept after remove" || bad "config and user kept after remove"

# --- yeniden kurulum eski yapılandırmayı bulur; purge (yalnızca deb) hepsini siler
out="$(pkg_install "$PKGFILE1")"; rc=$?
[ $rc -eq 0 ] && [ "$(sha256sum /etc/healthbeat/agent.json | cut -d' ' -f1)" = "$sum1" ] && ok "reinstall keeps the old config" || bad "reinstall keeps the old config" "$out"
if [ "$KIND" = deb ]; then
  out="$(pkg_purge)"; rc=$?
  [ $rc -eq 0 ] && [ ! -e /etc/healthbeat ] && ! getent passwd healthbeat >/dev/null && ok "purge removes config and user" || bad "purge removes config and user" "rc=$rc $(ls /etc/healthbeat 2>&1)"
  out="$(pkg_install "$PKGFILE1")"; rc=$?
  [ $rc -eq 0 ] && getent passwd healthbeat >/dev/null && ok "install after purge recreates the user" || bad "install after purge" "$out"
fi
INNER_EOF

run_image() {
  local image="$1" kind=deb
  case "$image" in rockylinux*|almalinux*|amazonlinux*|centos*|fedora*) kind=rpm ;; esac
  echo "=== $image ($kind) ==="
  local out
  out="$(docker run --rm -v "$WORK/pkg-$V1_VER:/pkg1:ro" -v "$WORK/pkg-$V2_VER:/pkg2:ro" -v "$ROOT/agent/deploy:/deploy:ro" \
        "$image" bash -c "$INNER" _ "$kind" "$V1_VER" "$V2_VER" 2>&1)" || true
  local line pass=0 fail=0
  while IFS= read -r line; do
    case "$line" in
      PASS*) pass=$((pass + 1)) ;;
      FAIL*) fail=$((fail + 1)); echo "  $line" | cut -c1-260 ;;
    esac
  done <<<"$out"
  if [[ $pass -eq 0 && $fail -eq 0 ]]; then fail=1; echo "  no test output; container said:"; echo "$out" | tail -8 | sed 's/^/    /'; fi
  echo "  $pass passed, $fail failed"
  PASSES=$((PASSES + pass)); FAILS=$((FAILS + fail))
}

for image in "${IMAGES[@]}"; do run_image "$image"; done

# --- mimari üstverisi (arm64 paketleri bu makinede çalıştırılamaz; yalnızca üstveri doğrulanır)
agent/packaging/build.sh arm64 "$V1_VER" "$WORK/pkg-arm64" "$WORK/hb-$V1_VER" >/dev/null 2>&1 || true
if [[ "$(dpkg-deb -f "$WORK"/pkg-arm64/*.deb Architecture 2>/dev/null)" == arm64 ]]; then PASSES=$((PASSES + 1)); else FAILS=$((FAILS + 1)); echo "  FAIL arm64 .deb architecture"; fi

echo
echo "test_packages.sh: $PASSES passed, $FAILS failed"
[[ $FAILS -eq 0 ]]
