#!/usr/bin/env bash
# HealthBeat agent kurucusu (docs/MIMARI.md bölüm 6, "Agent kurulumu").
#
#   sudo ./install.sh wizard                                             # guided, asks one question at a time
#   sudo ./install.sh install --mode push --server-url https://api.example.com --host-id <uuid>
#   sudo ./install.sh install --mode pull --allowed-ips 203.0.113.10
#   sudo ./install.sh uninstall [--purge]
#   sudo ./install.sh configure ...                                      # yalnızca config yazar (paketle kurulu agent için; healthbeat-agent-setup)
#   sudo ./install.sh upgrade [--binary PATH] [--dry-run]                # yalnızca binary'yi değiştirir; config'e dokunmaz
#   sudo ./install.sh rollback                                           # bir önceki binary'ye dön
#
# Secret'lar (push api_token / pull secret) asla komut satırı argümanı olarak alınmaz
# (`ps` çıktısında ve shell geçmişinde görünürlerdi): HEALTHBEAT_API_TOKEN /
# HEALTHBEAT_PULL_SECRET ortam değişkenini, --token-file / --secret-file'ı ya da etkileşimli
# istemi kullanın. Tüm seçenekler için --help ile çalıştırın.
set -euo pipefail
# Aşağıdaki karakter sınıfı regex'leri her locale'de düz ASCII anlamına gelmeli (tr_TR'de
# GNU grep/bash, [A-Za-z] içinde 'i'yi reddeder).
export LC_ALL=C

readonly SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SERVICE_NAME=healthbeat-agent
readonly SERVICE_USER=healthbeat

usage() {
  cat <<'USAGE'
Usage: install.sh <install|uninstall|wizard|upgrade|rollback|configure> [options]

wizard
  guided install: explains each choice and asks for it one at a time, then shows a
  summary before writing anything (requires a terminal). Equivalent to 'install' with
  no options, plus prompts for --docker/--disk-mounts/--ca-cert/--tls-cert too. Any
  option below can still be passed alongside 'wizard' to pre-fill (and skip) that
  question.

install options
  --mode push|pull            operating mode (prompted if omitted)
  --binary PATH               agent binary to install (default: ./healthbeat-agent or ../healthbeat-agent)
  --disk-mounts /,/data       mount points to report (default: /). "auto" means every real filesystem
                              (virtual, tmpfs and snap/squashfs mounts are left out), e.g. --disk-mounts auto
                              or /,auto. Reporting a mount does not raise alerts by itself: choose which
                              mounts alert per agent in the panel.
  --docker                    let the agent read the Docker socket (adds the service to the
                              'docker' group -- that is effectively ROOT on this host, see docs/AGENT.md)
  --force                     overwrite an existing config (it is backed up first)
  --no-start                  install but do not enable/start the service

  push mode
  --server-url URL            https://host[:port] of the HealthBeat server
  --host-id UUID            host id shown when the agent was created in the panel
  --interval SECONDS          push interval (default 30)
  --ca-cert FILE              PEM file of the (private) CA that signed the server's certificate;
                              only that CA is trusted. Use this instead of --insecure-skip-verify.
  --insecure-skip-verify      accept ANY server certificate (dev only; conflicts with --ca-cert)
  API token: HEALTHBEAT_API_TOKEN env var, --token-file FILE, or prompt

  pull mode
  --allowed-ips IP[,IP|CIDR]  the server address(es) allowed to poll this agent (required)
  --listen ADDR               listen address (default 0.0.0.0:9443; port must be >= 1024)
  --endpoint PATH             status path (default /api/v1/status)
  --tls-cert FILE --tls-key FILE   existing certificate (default: generate a self-signed one)
  pull secret: HEALTHBEAT_PULL_SECRET env var, --secret-file FILE, or prompt

uninstall options
  --purge                     also delete /etc/healthbeat (config, certificates) and the service user

configure write ONLY the configuration (and certificates / docker drop-in), then enable+start the service.
          Used after installing the .deb/.rpm package, which puts the binary and unit in place but no config
          (the package installs a 'healthbeat-agent-setup' command that runs this). Takes the same options as
          'install' (--mode, --server-url, ...) except --binary; on a terminal with no options it asks step by step.

upgrade   replace ONLY the agent binary; the config, certificates and unit are left alone
  --binary PATH               the new agent binary (default: ./healthbeat-agent or ../healthbeat-agent)
  --dry-run                   check everything and print the plan, change nothing
  --no-restart                install the binary but do not restart the service
  --allow-downgrade           allow installing an older version than the current one
  --update-unit               also install the systemd unit shipped next to this script
  --verify-seconds N          how long the restarted service must stay healthy before the upgrade
                              counts as successful (default 15); if it crashes, the previous binary
                              is restored automatically and the exit code is 1
  Before touching anything the new binary must report its version (--version) and must accept the
  EXISTING config (--check-config). The old binary is kept as healthbeat-agent.prev.

rollback  swap the previous binary (healthbeat-agent.prev) back in and restart
  --dry-run, --no-restart, --verify-seconds N   as for upgrade

common
  --root DIR                  install into DIR instead of / (packaging/staging; skips systemctl and chown)
  -h, --help
USAGE
}

die() { echo "install.sh: error: $*" >&2; exit 1; }
info() { echo "==> $*"; }

# ---------------------------------------------------------------- argüman ayrıştırma
WIZARD=0
CONFIGURE_ONLY=0
ACTION="${1:-}"
case "$ACTION" in
  install | uninstall | upgrade | rollback) shift ;;
  wizard) ACTION=install; WIZARD=1; shift ;;
  configure) ACTION=install; CONFIGURE_ONLY=1; shift ;;
  -h | --help) usage; exit 0 ;;
  "") usage >&2; exit 2 ;;
  *) die "unknown action '$ACTION' (expected install, uninstall, wizard, upgrade, rollback, or configure)" ;;
esac

MODE="" BINARY="" DISK_MOUNTS="/" DOCKER=0 FORCE=0 NO_START=0 PURGE=0 ROOT=""
DRY_RUN=0 NO_RESTART=0 ALLOW_DOWNGRADE=0 UPDATE_UNIT=0 VERIFY_SECONDS=15
SERVER_URL="" HOST_ID="" INTERVAL=30 INSECURE=0 TOKEN_FILE="" CA_CERT=""
ALLOWED_IPS="" LISTEN="0.0.0.0:9443" ENDPOINT="/api/v1/status" TLS_CERT="" TLS_KEY="" SECRET_FILE=""

need_value() { [[ $# -ge 2 && -n "$2" ]] || die "option $1 needs a value"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) need_value "$@"; MODE="$2"; shift 2 ;;
    --binary) need_value "$@"; BINARY="$2"; shift 2 ;;
    --disk-mounts) need_value "$@"; DISK_MOUNTS="$2"; shift 2 ;;
    --docker) DOCKER=1; shift ;;
    --force) FORCE=1; shift ;;
    --no-start) NO_START=1; shift ;;
    --purge) PURGE=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --no-restart) NO_RESTART=1; shift ;;
    --allow-downgrade) ALLOW_DOWNGRADE=1; shift ;;
    --update-unit) UPDATE_UNIT=1; shift ;;
    --verify-seconds) need_value "$@"; VERIFY_SECONDS="$2"; shift 2 ;;
    --root) need_value "$@"; ROOT="${2%/}"; shift 2 ;;
    --server-url) need_value "$@"; SERVER_URL="$2"; shift 2 ;;
    --host-id) need_value "$@"; HOST_ID="$2"; shift 2 ;;
    --interval) need_value "$@"; INTERVAL="$2"; shift 2 ;;
    --insecure-skip-verify) INSECURE=1; shift ;;
    --ca-cert) need_value "$@"; CA_CERT="$2"; shift 2 ;;
    --token-file) need_value "$@"; TOKEN_FILE="$2"; shift 2 ;;
    --allowed-ips) need_value "$@"; ALLOWED_IPS="$2"; shift 2 ;;
    --listen) need_value "$@"; LISTEN="$2"; shift 2 ;;
    --endpoint) need_value "$@"; ENDPOINT="$2"; shift 2 ;;
    --tls-cert) need_value "$@"; TLS_CERT="$2"; shift 2 ;;
    --tls-key) need_value "$@"; TLS_KEY="$2"; shift 2 ;;
    --secret-file) need_value "$@"; SECRET_FILE="$2"; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) die "unknown option '$1' (see --help)" ;;
  esac
done

# ---------------------------------------------------------------- yollar
readonly BIN_PATH="${ROOT}/usr/local/bin/healthbeat-agent"
readonly CONF_DIR="${ROOT}/etc/healthbeat"
readonly CONF_PATH="${CONF_DIR}/agent.json"
readonly TLS_DIR="${CONF_DIR}/tls"
readonly UNIT_PATH="${ROOT}/etc/systemd/system/${SERVICE_NAME}.service"
readonly DROPIN_DIR="${UNIT_PATH}.d"
# Çalışan servisin gördüğü (unit ve yapılandırma her zaman gerçek sistem yollarına başvurur).
readonly LIVE_TLS_DIR="/etc/healthbeat/tls"
readonly LIVE_CA_PATH="/etc/healthbeat/ca.pem"

STAGING=0
[[ -n "$ROOT" ]] && STAGING=1
if [[ $STAGING -eq 0 && $EUID -ne 0 ]]; then
  die "must run as root (use sudo), or pass --root DIR to install into a staging directory"
fi

run_systemctl() { [[ $STAGING -eq 1 ]] || systemctl "$@"; }

# Paketle (.deb/.rpm) kurulu bir makinede binary'yi ve unit'i paket yöneticisi yönetir; bu betiğin
# install/upgrade/rollback/uninstall'ı aynı dosyalara ikinci bir kopya yazıp paket kaydıyla
# çelişirdi. Yalnızca configure (config yazar) çalışır.
readonly PACKAGE_MARKER="${ROOT}/usr/share/healthbeat/installed-from-package"
if [[ -e "$PACKAGE_MARKER" && $CONFIGURE_ONLY -eq 0 ]]; then
  die "the agent on this host was installed from a package (.deb/.rpm), which the package manager owns: use apt/dnf to install, upgrade, downgrade or remove it (see docs/DISTRIBUTION.md). Only 'install.sh configure' works here."
fi

# ---------------------------------------------------------------- kaldırma
if [[ "$ACTION" == uninstall ]]; then
  if [[ $STAGING -eq 0 ]] && systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
    systemctl disable --now "${SERVICE_NAME}.service" 2>/dev/null || true
  fi
  rm -f "$BIN_PATH" "$UNIT_PATH"
  rm -rf "$DROPIN_DIR"
  run_systemctl daemon-reload
  if [[ $PURGE -eq 1 ]]; then
    rm -rf "$CONF_DIR"
    if [[ $STAGING -eq 0 ]] && id "$SERVICE_USER" >/dev/null 2>&1; then userdel "$SERVICE_USER" || true; fi
    info "removed the agent, its configuration and the '$SERVICE_USER' user"
  else
    info "removed the agent; configuration kept in $CONF_DIR (use --purge to delete it)"
  fi
  exit 0
fi

# ---------------------------------------------------------------- yükseltme / geri alma
# Yalnızca binary değişir: yapılandırma, sertifikalar ve unit'e dokunulmaz (unit için --update-unit).
# Yeni binary önce doğrulanır (sürüm bildirmeli, MEVCUT yapılandırmayı kabul etmeli), eskisi
# `.prev` olarak saklanır, yerine geçiş atomiktir (rename) ve servis yeniden başlatıldıktan sonra
# çökme döngüsüne girerse eski binary otomatik geri konur.
if [[ "$ACTION" == upgrade || "$ACTION" == rollback ]]; then
  readonly PREV_PATH="${BIN_PATH}.prev"
  readonly RE_SEMVER='^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'
  [[ "$VERIFY_SECONDS" =~ ^[0-9]{1,3}$ ]] || die "--verify-seconds must be a whole number of seconds"

  # HB_SYSTEMCTL testlerin sahte bir systemctl vermesi içindir; --root ile bile o zaman kullanılır.
  SYSTEMCTL="${HB_SYSTEMCTL:-systemctl}"
  SERVICE_CONTROL=1
  [[ $STAGING -eq 1 && -z "${HB_SYSTEMCTL:-}" ]] && SERVICE_CONTROL=0
  svc() { "$SYSTEMCTL" "$@"; }

  [[ -x "$BIN_PATH" && -f "$CONF_PATH" ]] || die "no installation found at $BIN_PATH / $CONF_PATH (use 'install' first; upgrade only replaces the binary of an existing install)"

  # binary_version PATH: binary'nin bildirdiği SemVer'i yazar; sürüm bildirmiyorsa (bozuk ya da
  # bir HealthBeat agent'ı değil) başarısız olur.
  binary_version() {
    local out ver
    out="$("$1" --version 2>/dev/null)" || return 1
    ver="$(awk 'NR == 1 { print $2 }' <<<"$out")"
    [[ "$ver" =~ $RE_SEMVER ]] || return 1
    printf '%s' "$ver"
  }
  core_of() { sed -E 's/[-+].*$//' <<<"$1"; }
  # core_lt A B: A'nın MAJOR.MINOR.PATCH çekirdeği B'ninkinden KÜÇÜK mü (önsürüm eki yok sayılır).
  core_lt() {
    local a b
    a="$(core_of "$1")"; b="$(core_of "$2")"
    [[ "$a" != "$b" && "$(printf '%s\n%s\n' "$a" "$b" | sort -V | head -n1)" == "$a" ]]
  }

  # swap_in SRC: SRC'yi BIN_PATH'in yerine atomik olarak koyar (çalışan süreç eski inode'u tutmaya devam eder).
  swap_in() {
    install -m 0755 "$1" "$BIN_PATH.new"
    mv -f "$BIN_PATH.new" "$BIN_PATH"
  }
  trap 'rm -f "$BIN_PATH.new" "$BIN_PATH.swap"' EXIT

  service_active() { [[ $SERVICE_CONTROL -eq 1 ]] && svc is-active --quiet "${SERVICE_NAME}.service"; }
  nrestarts() { svc show -p NRestarts --value "${SERVICE_NAME}.service" 2>/dev/null || echo 0; }

  # restart_and_verify: yeniden başlatır ve VERIFY_SECONDS boyunca servisin ayakta kaldığını, otomatik
  # yeniden başlatma (çökme döngüsü) yaşamadığını denetler. Push modunda server'a ulaşılamaması
  # bir çökme değildir; bu yüzden "metrik gitti mi" değil, "süreç yaşıyor mu" bakılır.
  restart_and_verify() {
    local before after i
    before="$(nrestarts)"
    svc restart "${SERVICE_NAME}.service" || return 1
    for ((i = 0; i < VERIFY_SECONDS; i++)); do
      sleep 1
      svc is-active --quiet "${SERVICE_NAME}.service" || return 1
      after="$(nrestarts)"
      [[ "$after" == "$before" ]] || return 1
    done
  }

  CUR_VER="$(binary_version "$BIN_PATH" || true)"
  CUR_DESC="${CUR_VER:-unknown (no version reported)}"

  if [[ "$ACTION" == rollback ]]; then
    [[ -x "$PREV_PATH" ]] || die "no previous binary at $PREV_PATH: nothing to roll back to"
    PREV_VER="$(binary_version "$PREV_PATH" || true)"
    info "rollback: $CUR_DESC -> ${PREV_VER:-unknown (no version reported)}"
    if [[ $DRY_RUN -eq 1 ]]; then info "dry run: nothing was changed"; exit 0; fi

    was_active=0; service_active && was_active=1
    # Yalnızca geri dönülecek binary --check-config biliyorsa (1.2.0+) yapılandırma denetlenir.
    prev_help="$("$PREV_PATH" --help 2>&1 || true)"
    if [[ "$prev_help" == *-check-config* ]]; then
      "$PREV_PATH" -config "$CONF_PATH" -check-config >/dev/null 2>&1 || die "the previous binary does not accept the current config; not rolling back"
    fi
    cp -p "$BIN_PATH" "$BIN_PATH.swap"   # geri alınanı, rollback'in rollback'i için sakla
    swap_in "$PREV_PATH"
    mv -f "$BIN_PATH.swap" "$PREV_PATH"
    if [[ $NO_RESTART -eq 1 || $SERVICE_CONTROL -eq 0 ]]; then
      info "binary swapped; restart the service to use it: systemctl restart ${SERVICE_NAME}"
    elif [[ $was_active -eq 1 ]]; then
      if restart_and_verify; then
        info "rolled back; the service is running ($CUR_DESC binary kept as $PREV_PATH)"
      else
        # Geri alınan binary de ayakta kalmadı: eski durumu tekrar kur ve yüksek sesle bildir.
        cp -p "$BIN_PATH" "$BIN_PATH.swap"; swap_in "$PREV_PATH"; mv -f "$BIN_PATH.swap" "$PREV_PATH"
        svc restart "${SERVICE_NAME}.service" || true
        die "the service did not stay healthy after the rollback; the previous state was restored (check: journalctl -u ${SERVICE_NAME})"
      fi
    else
      info "binary swapped; the service was not running, so it was not started"
    fi
    exit 0
  fi

  # ----- upgrade
  if [[ -z "$BINARY" ]]; then
    for candidate in "$SELF_DIR/healthbeat-agent" "$SELF_DIR/../healthbeat-agent"; do
      [[ -x "$candidate" ]] && { BINARY="$candidate"; break; }
    done
  fi
  [[ -n "$BINARY" && -x "$BINARY" ]] || die "new agent binary not found; build it (cd agent && go build -o healthbeat-agent ./cmd/agent) or pass --binary PATH"

  NEW_VER="$(binary_version "$BINARY")" || die "the new binary does not report a version ('$BINARY --version' failed): it is built for another architecture, or not a HealthBeat agent; refusing to install it"
  if ! check_out="$("$BINARY" -config "$CONF_PATH" -check-config 2>&1)"; then
    die "the new binary does not accept the EXISTING config ($CONF_PATH); nothing was changed: ${check_out}"
  fi

  if [[ -n "$CUR_VER" && "$CUR_VER" == "$NEW_VER" ]] && cmp -s "$BINARY" "$BIN_PATH"; then
    info "already up to date ($NEW_VER); nothing to do"
    exit 0
  fi
  if [[ -n "$CUR_VER" && $ALLOW_DOWNGRADE -eq 0 ]] && core_lt "$NEW_VER" "$CUR_VER"; then
    die "refusing to downgrade $CUR_VER -> $NEW_VER (pass --allow-downgrade if that is intended)"
  fi

  unit_drift=0
  if [[ -f "$UNIT_PATH" ]] && ! cmp -s "$SELF_DIR/healthbeat-agent.service" "$UNIT_PATH"; then unit_drift=1; fi

  info "upgrade: $CUR_DESC -> $NEW_VER (config check: ok)"
  if [[ $unit_drift -eq 1 ]]; then
    if [[ $UPDATE_UNIT -eq 1 ]]; then info "the systemd unit differs from the shipped one and WILL be updated (--update-unit)"
    else info "note: the installed systemd unit differs from the one shipped next to this script; it was left alone (add --update-unit to replace it)"; fi
  fi
  if [[ $DRY_RUN -eq 1 ]]; then info "dry run: nothing was changed"; exit 0; fi

  was_active=0; service_active && was_active=1

  # Geri dönüş için önce yedek; yeni binary'yi atomik olarak yerleştir.
  install -m 0755 "$BIN_PATH" "$PREV_PATH"
  swap_in "$BINARY"
  unit_backup=""
  if [[ $UPDATE_UNIT -eq 1 && $unit_drift -eq 1 ]]; then
    unit_backup="$UNIT_PATH.prev"
    cp -p "$UNIT_PATH" "$unit_backup"
    install -D -m 0644 "$SELF_DIR/healthbeat-agent.service" "$UNIT_PATH"
    [[ $SERVICE_CONTROL -eq 0 ]] || svc daemon-reload
  fi

  if [[ $NO_RESTART -eq 1 || $SERVICE_CONTROL -eq 0 ]]; then
    info "installed $NEW_VER; restart the service to use it: systemctl restart ${SERVICE_NAME}"
  elif [[ $was_active -eq 1 ]]; then
    info "restarting ${SERVICE_NAME} and watching it for ${VERIFY_SECONDS}s"
    if ! restart_and_verify; then
      info "the service did not stay healthy with $NEW_VER; restoring $CUR_DESC"
      swap_in "$PREV_PATH"
      if [[ -n "$unit_backup" ]]; then mv -f "$unit_backup" "$UNIT_PATH"; svc daemon-reload || true; fi
      svc restart "${SERVICE_NAME}.service" || true
      die "upgrade to $NEW_VER failed and was rolled back automatically; the config was not touched (check: journalctl -u ${SERVICE_NAME})"
    fi
  else
    info "the service was not running, so it was not started (it will use $NEW_VER when you start it)"
  fi

  info "upgraded $CUR_DESC -> $NEW_VER"
  info "previous binary kept as $PREV_PATH (undo with: install.sh rollback); verify in the panel that this server now shows $NEW_VER"
  exit 0
fi

# ---------------------------------------------------------------- girdi yardımcıları
interactive() { [[ -t 0 && -t 1 ]]; }

# configure: hiç seçenek verilmediyse ve bir terminaldeyse adım adım sorar (wizard gibi).
if [[ $CONFIGURE_ONLY -eq 1 && $WIZARD -eq 0 && -z "$MODE" ]] && interactive; then WIZARD=1; fi

if [[ $WIZARD -eq 1 ]]; then
  interactive || die "wizard requires an interactive terminal (no stdin/stdout tty); use 'install' with flags instead (see --help)"
  echo "HealthBeat agent -- guided install. Press Enter to accept a default shown in [brackets]."
fi

# wizard: bir soru sormadan önce ne için sorulduğunu kısaca açıkla.
note() { printf '\n%s\n' "$*"; }

# wizard: confirm "soru" varsayılan(y|n) -> "evet"se 0 (başarı)
confirm() {
  local prompt="$1" default="${2:-y}" reply suffix
  if [[ "$default" == y ]]; then suffix="Y/n"; else suffix="y/N"; fi
  read -r -p "$prompt [$suffix]: " reply
  reply="${reply:-$default}"
  [[ "$reply" =~ ^[Yy] ]]
}

ask() { # ask DEĞİŞKEN "istem" [varsayılan]
  local var="$1" prompt="$2" default="${3:-}" reply
  interactive || die "$prompt: missing (pass it as an option; not running interactively)"
  read -r -p "$prompt${default:+ [$default]}: " reply
  printf -v "$var" '%s' "${reply:-$default}"
}

read_secret() { # read_secret DEĞİŞKEN ORTAMADI DOSYA "istem"
  local var="$1" envname="$2" file="$3" prompt="$4" value=""
  if [[ -n "$file" ]]; then
    [[ -r "$file" ]] || die "cannot read $file"
    value="$(head -n1 "$file" | tr -d '\r\n')"
  elif [[ -n "${!envname:-}" ]]; then
    value="${!envname}"
  else
    interactive || die "$prompt missing: set $envname, pass a file option, or run interactively"
    read -r -s -p "$prompt: " value
    echo
  fi
  printf -v "$var" '%s' "$value"
}

# Değerler printf ile yazılan bir JSON belgesinin içine girer; bu yüzden kaçırılmak yerine
# sıkı bir karakter kümesine karşı doğrulanır.
require_match() { # require_match DEĞER REGEX ne
  [[ "$1" =~ $2 ]] || die "invalid $3: '$1'"
}
readonly RE_UUID='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
readonly RE_SECRET='^[A-Za-z0-9_-]{20,}$'
readonly RE_URL='^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$'
readonly RE_IPCIDR='^[0-9A-Fa-f:.]+(/[0-9]{1,3})?$'
readonly RE_PATH='^/[A-Za-z0-9._/-]*$'

json_array() { # json_array a,b,c -> ["a","b","c"]
  local IFS=',' item out="" first=1
  for item in $1; do
    [[ $first -eq 1 ]] || out+=","
    out+="\"$item\""; first=0
  done
  printf '[%s]' "$out"
}

# ---------------------------------------------------------------- topla + doğrula
if [[ $WIZARD -eq 1 && -z "$MODE" ]]; then
  note "Two modes: 'push' -- the agent connects OUT to the server (works behind NAT/firewalls, no
inbound port needed here -- the recommended default). 'pull' -- the server connects IN to the
agent (an inbound port must be opened on this host)."
fi
[[ -n "$MODE" ]] || ask MODE "Mode (push or pull)" push
[[ "$MODE" == push || "$MODE" == pull ]] || die "--mode must be push or pull"

if [[ $CONFIGURE_ONLY -eq 1 ]]; then
  [[ -z "$BINARY" ]] || die "--binary does not apply to configure (it only writes the configuration; the binary comes from the package)"
  [[ -x "${ROOT}/usr/bin/healthbeat-agent" || -x "$BIN_PATH" ]] || die "no agent binary is installed (looked for ${ROOT}/usr/bin/healthbeat-agent and $BIN_PATH): install the package first, or use 'install.sh install' for a tarball install"
else
  if [[ -z "$BINARY" ]]; then
    for candidate in "$SELF_DIR/healthbeat-agent" "$SELF_DIR/../healthbeat-agent"; do
      [[ -x "$candidate" ]] && { BINARY="$candidate"; break; }
    done
  fi
  if [[ $WIZARD -eq 1 && ( -z "$BINARY" || ! -x "$BINARY" ) ]]; then
    note "Could not find a compiled healthbeat-agent binary next to this script (build it with:
cd agent && go build -o healthbeat-agent ./cmd/agent)."
    ask BINARY "Path to the compiled healthbeat-agent binary"
  fi
  [[ -n "$BINARY" && -x "$BINARY" ]] || die "agent binary not found; build it (cd agent && go build -o healthbeat-agent ./cmd/agent) or pass --binary PATH"
fi

if [[ $WIZARD -eq 1 && "$DISK_MOUNTS" == "/" ]]; then
  note "Which filesystems should the agent report metrics for? This only controls what is
reported/graphed -- which of them raise alerts is chosen per-server later, in the panel.
Comma-separated absolute paths, or 'auto' for every real filesystem (skips virtual/tmpfs/snap
mounts)."
  ask DISK_MOUNTS "Mount points to report" "/"
fi
# Listelenen her mount düz, mutlak bir yol olmalı.
IFS=',' read -r -a _mounts <<<"$DISK_MOUNTS"
for m in "${_mounts[@]}"; do [[ "$m" == auto ]] || require_match "$m" "$RE_PATH" "disk mount (an absolute path or auto)"; done

if [[ "$MODE" == push ]]; then
  if [[ $WIZARD -eq 1 && -z "$SERVER_URL" ]]; then
    note "The HealthBeat server's address, as this agent will reach it. Must be https:// (plain
http would send the API token unencrypted)."
  fi
  [[ -n "$SERVER_URL" ]] || ask SERVER_URL "HealthBeat server URL (https://host[:port])"
  if [[ $WIZARD -eq 1 && -z "$HOST_ID" ]]; then
    note "The agent ID the panel showed when you added this server (Organizations -> your
organization -> Add server)."
  fi
  [[ -n "$HOST_ID" ]] || ask HOST_ID "Host ID (from the panel)"
  SERVER_URL="${SERVER_URL%/}"
  require_match "$SERVER_URL" "$RE_URL" "--server-url (must be https://host[:port]; plain http would send the token unencrypted)"
  require_match "$HOST_ID" "$RE_UUID" "--host-id (expected a UUID)"
  if [[ $WIZARD -eq 1 && $INTERVAL -eq 30 ]]; then
    note "How often the agent sends a metrics snapshot to the server, in seconds."
    ask INTERVAL "Push interval, seconds" 30
  fi
  [[ "$INTERVAL" =~ ^[0-9]+$ && "$INTERVAL" -ge 1 && "$INTERVAL" -le 86400 ]] || die "--interval must be 1..86400 seconds"
  if [[ $WIZARD -eq 1 && -z "$CA_CERT" && $INSECURE -eq 0 ]]; then
    note "If the server's TLS certificate was signed by your own (private/corporate) CA rather
than a public one, give its PEM file here so the agent can verify the server. Leave empty for a
public CA (Let's Encrypt etc.) -- that's the common case."
    ask CA_CERT "Path to a private CA certificate (PEM), or leave empty"
  fi
  if [[ -n "$CA_CERT" ]]; then
    [[ $INSECURE -eq 0 ]] || die "--ca-cert and --insecure-skip-verify contradict each other: verify the server with the CA, or skip verification, not both"
    [[ -r "$CA_CERT" ]] || die "cannot read --ca-cert $CA_CERT"
    grep -q 'BEGIN CERTIFICATE' "$CA_CERT" || die "--ca-cert $CA_CERT does not contain a PEM certificate"
  elif [[ $WIZARD -eq 1 && $INSECURE -eq 0 ]]; then
    if ! confirm "Verify the server's TLS certificate normally? (answer no only for local testing with a self-signed cert)" y; then
      INSECURE=1
      note "insecure_skip_verify will be enabled -- ANY server certificate will be accepted. Do not use this in production."
    fi
  fi
  [[ $WIZARD -eq 0 ]] || note "Paste the API token the panel showed you (input is hidden). If you
don't have it, generate one from the server's detail page in the panel first (rotate credentials)."
  read_secret SECRET HEALTHBEAT_API_TOKEN "$TOKEN_FILE" "API token"
  require_match "$SECRET" "$RE_SECRET" "API token (expected the URL-safe token shown once by the panel)"
else
  [[ -z "$CA_CERT" ]] || die "--ca-cert only applies to push mode (it verifies the server the agent connects to)"
  if [[ $WIZARD -eq 1 && -z "$ALLOWED_IPS" ]]; then
    note "Which server IP(s) are allowed to poll this agent? Connections from any other address
are refused before TLS. Comma-separated IPs or CIDRs."
  fi
  [[ -n "$ALLOWED_IPS" ]] || ask ALLOWED_IPS "HealthBeat server IP allowed to poll this agent (comma-separated)"
  IFS=',' read -r -a _ips <<<"$ALLOWED_IPS"
  for ip in "${_ips[@]}"; do require_match "$ip" "$RE_IPCIDR" "allowed IP/CIDR"; done
  if [[ $WIZARD -eq 1 && "$LISTEN" == "0.0.0.0:9443" ]]; then
    note "Address and port the agent listens on for the server's poll requests (must be >= 1024;
the service runs unprivileged). Remember to open this port in the firewall for the server's IP."
    ask LISTEN "Listen address" "0.0.0.0:9443"
  fi
  # "]" köşeli ifadede ilk, "-" ise son olmalı ki gerçek karakter olsun.
  [[ "$LISTEN" =~ ^[][A-Za-z0-9.:-]*:([0-9]{1,5})$ ]] || die "--listen must look like host:port"
  port="${BASH_REMATCH[1]}"
  [[ "$port" -ge 1024 && "$port" -le 65535 ]] || die "--listen port must be 1024..65535 (the service runs unprivileged)"
  if [[ $WIZARD -eq 1 && "$ENDPOINT" == "/api/v1/status" ]]; then
    ask ENDPOINT "Status path" "/api/v1/status"
  fi
  require_match "$ENDPOINT" "$RE_PATH" "--endpoint"
  if [[ $WIZARD -eq 1 && -z "$TLS_CERT" ]]; then
    note "The agent needs a TLS certificate to serve HTTPS. Leave this empty to have the installer
generate a self-signed one now (the server does not verify it -- authentication is the shared
secret + IP allow-list above), or provide your own."
    if confirm "Provide your own certificate/key instead of a self-signed one?" n; then
      ask TLS_CERT "Path to certificate (PEM)"
      ask TLS_KEY "Path to private key (PEM)"
    fi
  fi
  if [[ -n "$TLS_CERT" || -n "$TLS_KEY" ]]; then
    [[ -r "$TLS_CERT" && -r "$TLS_KEY" ]] || die "--tls-cert and --tls-key must both be readable files"
  fi
  [[ $WIZARD -eq 0 ]] || note "Paste the pull secret the panel showed you (input is hidden)."
  read_secret SECRET HEALTHBEAT_PULL_SECRET "$SECRET_FILE" "Pull secret"
  require_match "$SECRET" "$RE_SECRET" "pull secret (expected the URL-safe secret shown once by the panel)"
fi

if [[ $WIZARD -eq 1 && $DOCKER -eq 0 ]]; then
  note "Optional: let the agent read /var/run/docker.sock to report Docker container status.
Access to the Docker socket is effectively ROOT on this host, so only enable this on hosts you
actually want to monitor Docker on (see docs/AGENT.md)."
  confirm "Monitor Docker containers on this host?" n && DOCKER=1
fi
if [[ $DOCKER -eq 1 && $STAGING -eq 0 ]]; then
  getent group docker >/dev/null || die "--docker requested but this host has no 'docker' group"
fi
if [[ -e "$CONF_PATH" && $FORCE -eq 0 ]]; then
  if [[ $WIZARD -eq 1 ]] && confirm "$CONF_PATH already exists. Overwrite it? (the current file is backed up first)" n; then
    FORCE=1
  else
    die "$CONF_PATH already exists; pass --force to replace it (a backup is kept)"
  fi
fi

if [[ $WIZARD -eq 1 ]]; then
  echo
  if [[ $CONFIGURE_ONLY -eq 1 ]]; then echo "About to write the configuration:"; else echo "About to install:"; fi
  echo "  mode:         $MODE"
  if [[ "$MODE" == push ]]; then
    echo "  server:       $SERVER_URL"
    echo "  host id:    $HOST_ID"
    echo "  interval:     ${INTERVAL}s"
    [[ -n "$CA_CERT" ]] && echo "  private CA:   $CA_CERT"
    [[ $INSECURE -eq 1 ]] && echo "  WARNING:      server certificate will NOT be verified"
  else
    echo "  listen:       $LISTEN$ENDPOINT"
    echo "  allowed IPs:  $ALLOWED_IPS"
    if [[ -n "$TLS_CERT" ]]; then echo "  certificate:  $TLS_CERT"; else echo "  certificate:  self-signed (generated now)"; fi
  fi
  echo "  disk mounts:  $DISK_MOUNTS"
  if [[ $DOCKER -eq 1 ]]; then echo "  docker:       enabled"; else echo "  docker:       disabled"; fi
  [[ $CONFIGURE_ONLY -eq 1 ]] || echo "  binary:       $BINARY"
  echo
  confirm "Proceed with these settings?" y || { info "cancelled, nothing was changed"; exit 0; }
fi

# ---------------------------------------------------------------- kurulum
if [[ $STAGING -eq 0 ]] && ! id "$SERVICE_USER" >/dev/null 2>&1; then
  info "creating system user '$SERVICE_USER'"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi

if [[ $CONFIGURE_ONLY -eq 0 ]]; then
  info "installing the agent to $BIN_PATH"
  install -D -m 0755 "$BINARY" "$BIN_PATH"
fi

install -d -m 0750 "$CONF_DIR"
[[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$CONF_DIR"

if [[ -e "$CONF_PATH" ]]; then
  backup="$CONF_PATH.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$CONF_PATH" "$backup"
  info "existing config backed up to $backup"
fi

write_config() {
  umask 0077
  if [[ "$MODE" == push ]]; then
    local ca_field=""
    [[ -z "$CA_CERT" ]] || ca_field="$(printf '  "ca_cert_file": "%s",\n' "$LIVE_CA_PATH")"
    printf '{\n  "mode": "push",\n  "server_url": "%s",\n  "host_id": "%s",\n  "api_token": "%s",\n  "interval_seconds": %s,\n%s  "insecure_skip_verify": %s,\n  "disk_mounts": %s\n}\n' \
      "$SERVER_URL" "$HOST_ID" "$SECRET" "$INTERVAL" "$ca_field" "$([[ $INSECURE -eq 1 ]] && echo true || echo false)" "$(json_array "$DISK_MOUNTS")" >"$CONF_PATH"
  else
    printf '{\n  "mode": "pull",\n  "listen_addr": "%s",\n  "pull_endpoint": "%s",\n  "pull_secret": "%s",\n  "allowed_server_ips": %s,\n  "tls_cert_file": "%s",\n  "tls_key_file": "%s",\n  "disk_mounts": %s\n}\n' \
      "$LISTEN" "$ENDPOINT" "$SECRET" "$(json_array "$ALLOWED_IPS")" "$LIVE_TLS_DIR/cert.pem" "$LIVE_TLS_DIR/key.pem" "$(json_array "$DISK_MOUNTS")" >"$CONF_PATH"
  fi
  chmod 0640 "$CONF_PATH" # yalnızca servis grubu okuyabilir; kimlik bilgisini tutar
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$CONF_PATH"
}
write_config

if [[ -n "$CA_CERT" ]]; then
  info "installing the CA certificate to ${CONF_DIR}/ca.pem"
  install -m 0644 "$CA_CERT" "$CONF_DIR/ca.pem" # bir CA sertifikası herkese açıktır
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$CONF_DIR/ca.pem"
fi

if [[ "$MODE" == pull ]]; then
  install -d -m 0750 "$TLS_DIR"
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$TLS_DIR"
  if [[ -n "$TLS_CERT" ]]; then
    install -m 0644 "$TLS_CERT" "$TLS_DIR/cert.pem"
    install -m 0640 "$TLS_KEY" "$TLS_DIR/key.pem"
  else
    command -v openssl >/dev/null || die "openssl not found; install it or pass --tls-cert/--tls-key"
    info "generating a self-signed certificate (the server does not verify it; authentication is the secret + IP allow-list)"
    host="$(hostname 2>/dev/null || echo healthbeat-agent)"
    (
      umask 0077
      openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
        -subj "/CN=${host//[^A-Za-z0-9.-]/-}" -keyout "$TLS_DIR/key.pem" -out "$TLS_DIR/cert.pem" 2>/dev/null
    )
    chmod 0644 "$TLS_DIR/cert.pem"; chmod 0640 "$TLS_DIR/key.pem"
  fi
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$TLS_DIR/cert.pem" "$TLS_DIR/key.pem"
fi

if [[ $CONFIGURE_ONLY -eq 0 ]]; then
  info "installing the systemd unit"
  install -D -m 0644 "$SELF_DIR/healthbeat-agent.service" "$UNIT_PATH"
fi
if [[ $DOCKER -eq 1 ]]; then
  install -d -m 0755 "$DROPIN_DIR"
  printf '[Service]\n# Reading /var/run/docker.sock is equivalent to root on this host.\nSupplementaryGroups=docker\n' >"$DROPIN_DIR/docker.conf"
  info "WARNING: the agent can now talk to Docker, which is equivalent to root access on this host"
else
  rm -rf "$DROPIN_DIR"
fi

run_systemctl daemon-reload
if [[ $NO_START -eq 0 ]]; then
  run_systemctl enable --now "${SERVICE_NAME}.service"
  [[ $STAGING -eq 1 ]] || info "started; follow the log with: journalctl -u ${SERVICE_NAME} -f"
fi

info "done (mode: $MODE, config: /etc/healthbeat/agent.json)"
[[ "$MODE" == pull ]] && info "remember to allow inbound TCP ${port} from the HealthBeat server in the firewall"
exit 0
