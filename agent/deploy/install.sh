#!/usr/bin/env bash
# HealthBeat agent kurucusu (docs/MIMARI.md bölüm 6, "Agent kurulumu").
#
#   sudo ./install.sh wizard                                             # adım adım sihirbaz
#   sudo ./install.sh install --mode push --server-url https://api.example.com --host-id <uuid>
#   sudo ./install.sh install --mode pull --allowed-ips 203.0.113.10
#   sudo ./install.sh uninstall [--purge]
#   sudo ./install.sh configure ...                                      # yalnızca config yazar (paketle kurulu agent için; healthbeat-agent-setup)
#   sudo ./install.sh upgrade [--binary PATH] [--dry-run]                # yalnızca binary'yi değiştirir; config'e dokunmaz
#   sudo ./install.sh rollback                                           # bir önceki binary'ye dön
#   sudo ./install.sh token                                              # yalnızca API token'ı / pull secret'ı değiştirir (healthbeat-agent-setup token)
#   sudo ./install.sh encrypt                                            # config'te düz duran token'ı / secret'ı şifreler (healthbeat-agent-setup encrypt)
#
# Secret'lar (push api_token / pull secret) asla komut satırı argümanı olarak alınmaz
# (`ps` çıktısında ve shell geçmişinde görünürlerdi): HEALTHBEAT_API_TOKEN /
# HEALTHBEAT_PULL_SECRET ortam değişkenini, --token-file / --secret-file'ı ya da etkileşimli
# istemi kullanın. agent.json'a bu makinenin kimliğine bağlı şifreli olarak yazılırlar (agent -encrypt-token).
# Tüm seçenekler için --help ile çalıştırın.
set -euo pipefail
# Aşağıdaki karakter sınıfı regex'leri her locale'de düz ASCII anlamına gelmeli (tr_TR'de
# GNU grep/bash, [A-Za-z] içinde 'i'yi reddeder).
export LC_ALL=C

readonly SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SERVICE_NAME=healthbeat-agent
readonly SERVICE_USER=healthbeat

usage() {
  cat <<'USAGE'
Kullanım: install.sh <eylem> [seçenekler]
Paketle kurulu agent'ta:
  healthbeat-agent-setup [token|encrypt] [seçenekler]

Eylemler
  wizard      Adım adım kurulum sihirbazı (terminal gerekir). Paketle
              kurulu agent'ta seçeneksiz healthbeat-agent-setup.
  install     Binary'yi, servisi ve yapılandırmayı kurar (tarball).
  configure   Yalnızca yapılandırmayı yazar ve servisi başlatır
              (paketle kurulu agent; healthbeat-agent-setup).
  token       Panelde yenilenen API token'ı (pull'da secret'ı) verir.
  encrypt     Config'te düz duran token'ı aynen şifreler.
  upgrade     Yalnızca binary'yi değiştirir (tarball).
  rollback    Bir önceki binary'ye döner (tarball).
  uninstall   Agent'ı kaldırır (tarball).

Kurulum seçenekleri (install, configure; wizard'da ön doldurma)
  --mode push|pull           Çalışma modu
  --binary DOSYA             Kurulacak binary (yalnızca install;
                             varsayılan: betiğin yanındaki)
  --disk-mounts /,/data      Raporlanacak diskler (varsayılan /);
                             "auto" = bütün gerçek dosya sistemleri.
                             Alert üretecek diskler panelden seçilir.
  --docker                   Docker container'larını izle: servis
                             'docker' grubuna eklenir (Docker soketine
                             erişim bu makinede root yetkisine denktir)
  --force                    Var olan config'in üzerine yaz (önce
                             yedeklenir)
  --no-start                 Kur ama servisi başlatma

  push modu
  --server-url URL           Server adresi: https://adres[:port]
  --host-id UUID             Panelin gösterdiği host id
  --interval SANİYE          Gönderim aralığı (varsayılan 30)
  --ca-cert DOSYA            Server sertifikasını imzalayan özel CA
                             (PEM); yalnızca o CA'ya güvenilir
  --insecure-skip-verify     Her sertifikayı kabul et (yalnızca test;
                             --ca-cert ile birlikte olmaz)
  API token: HEALTHBEAT_API_TOKEN, --token-file DOSYA ya da gizli istem

  pull modu
  --allowed-ips IP[,CIDR]    Sorgulamasına izin verilen server
                             adresleri (zorunlu)
  --listen ADRES             Dinleme adresi (varsayılan 0.0.0.0:9443;
                             port 1024 ve üstü)
  --endpoint YOL             Status yolu (varsayılan /api/v1/status)
  --tls-cert DOSYA --tls-key DOSYA
                             Mevcut sertifika (verilmezse kendinden
                             imzalı üretilir)
  Pull secret: HEALTHBEAT_PULL_SECRET, --secret-file DOSYA ya da istem

token / encrypt
  --no-restart               Değeri yaz ama servisi yeniden başlatma
  token: değer HEALTHBEAT_API_TOKEN / HEALTHBEAT_PULL_SECRET,
  --token-file / --secret-file ya da gizli istemle verilir; mod
  config'ten okunur. encrypt hiçbir şey sormaz, zaten şifreliyse bir
  şey yapmaz.

upgrade / rollback
  --binary DOSYA             Yeni binary (upgrade; varsayılan:
                             betiğin yanındaki)
  --dry-run                  Her şeyi denetle, planı yaz, değiştirme
  --no-restart               Binary'yi koy ama servisi yeniden başlatma
  --allow-downgrade          Daha eski sürüme geçmeye izin ver
  --update-unit              systemd unit'ini de güncelle
  --verify-seconds N         Yeniden başlayan servisin sağlıklı
                             kalması gereken süre (varsayılan 15);
                             çökerse önceki binary geri konur
  Yeni binary önce sürümünü bildirmeli ve MEVCUT config'i kabul
  etmelidir. Eski binary healthbeat-agent.prev olarak saklanır.

uninstall
  --purge                    /etc/healthbeat'i (config, sertifikalar) ve
                             servis kullanıcısını da sil

Ortak
  --root DİZİN               / yerine DİZİN'e kur (hazırlık/test;
                             systemctl ve chown atlanır)
  -h, --help                 Bu yardımı göster

Token ve secret komut satırından alınmaz. Config'e bu makineye
bağlı şifreli yazılır; düz değer de çalışır (agent uyarır).
USAGE
}

die() { echo "install.sh: hata: $*" >&2; exit 1; }
# info: sihirbazdan sonra (WIZARD_DONE) ✓ satırı olarak yazılır.
info() {
  if [[ ${WIZARD_DONE:-0} -eq 1 ]]; then printf '  %s✓%s %s\n' "${C_G:-}" "${C_0:-}" "$*"; else echo "==> $*"; fi
}

# ---------------------------------------------------------------- argüman ayrıştırma
WIZARD=0
CONFIGURE_ONLY=0
ACTION="${1:-}"
case "$ACTION" in
  install | uninstall | upgrade | rollback) shift ;;
  wizard) ACTION=install; WIZARD=1; shift ;;
  configure) ACTION=install; CONFIGURE_ONLY=1; shift ;;
  token | encrypt) shift ;;
  -h | --help) usage; exit 0 ;;
  "") usage >&2; exit 2 ;;
  *) die "bilinmeyen eylem '$ACTION' (geçerli: install, uninstall, wizard, upgrade, rollback, configure, token, encrypt)" ;;
esac

MODE="" BINARY="" DISK_MOUNTS="/" DOCKER=0 FORCE=0 NO_START=0 PURGE=0 ROOT=""
DRY_RUN=0 NO_RESTART=0 ALLOW_DOWNGRADE=0 UPDATE_UNIT=0 VERIFY_SECONDS=15
SERVER_URL="" HOST_ID="" INTERVAL=30 INSECURE=0 TOKEN_FILE="" CA_CERT=""
ALLOWED_IPS="" LISTEN="0.0.0.0:9443" ENDPOINT="/api/v1/status" TLS_CERT="" TLS_KEY="" SECRET_FILE="" SECRET=""

need_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 seçeneği bir değer ister"; }
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
    *) die "bilinmeyen seçenek '$1' (bkz. --help)" ;;
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
  die "root olarak çalıştırın (sudo) ya da bir hazırlık dizinine kurmak için --root DİZİN verin"
fi

run_systemctl() { [[ $STAGING -eq 1 ]] || systemctl "$@"; }

# Paketle (.deb/.rpm) kurulu bir makinede binary'yi ve unit'i paket yöneticisi yönetir; bu betiğin
# install/upgrade/rollback/uninstall'ı aynı dosyalara ikinci bir kopya yazıp paket kaydıyla
# çelişirdi. Yalnızca configure ve token (config yazar) çalışır.
readonly PACKAGE_MARKER="${ROOT}/usr/share/healthbeat/installed-from-package"
if [[ -e "$PACKAGE_MARKER" && $CONFIGURE_ONLY -eq 0 && "$ACTION" != token && "$ACTION" != encrypt ]]; then
  die "bu makinedeki agent paketle (.deb/.rpm) kurulmuş; kurma, yükseltme, sürüm düşürme ve kaldırma paket yöneticisiyle (apt/dnf) yapılır (bkz. docs/DISTRIBUTION.md). Burada yalnızca 'install.sh configure', 'token' ve 'encrypt' çalışır."
fi

# plain_secret_notice BINARY KOMUT: config'teki sır düz duruyorsa (agent'ın kendi okuma kuralıyla) nasıl şifreleneceğini
# söyler; yükseltmenin sonunda çağrılır, config'e dokunmaz.
plain_secret_notice() {
  local field
  field="$("$1" -config "$CONF_PATH" -check-config 2>&1 | sed -n 's/.*warning: \([a-z_]*\) is stored in plain text.*/\1/p' | head -n1)"
  [[ -n "$field" ]] || return 0
  echo "install.sh: UYARI: /etc/healthbeat/agent.json içindeki $field düz metin olarak duruyor." >&2
  echo "  Bu makine için şifreleyin (değer aynı kalır; servis yeniden başlar):" >&2
  echo "    sudo $2" >&2
}

# Sırrı şifreleyen binary: install'da az önce kurulan, configure/token'da paketin (/usr/bin) ya da tarball'ın kurduğu.
AGENT_BIN="$BIN_PATH"
[[ "$ACTION" == install && $CONFIGURE_ONLY -eq 0 ]] || [[ ! -x "${ROOT}/usr/bin/healthbeat-agent" ]] || AGENT_BIN="${ROOT}/usr/bin/healthbeat-agent"

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
    info "Agent, yapılandırması ve '$SERVICE_USER' kullanıcısı kaldırıldı"
  else
    info "Agent kaldırıldı; yapılandırma $CONF_DIR içinde duruyor (silmek için --purge)"
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
  [[ "$VERIFY_SECONDS" =~ ^[0-9]{1,3}$ ]] || die "--verify-seconds tam sayı (saniye) olmalı"

  # HB_SYSTEMCTL testlerin sahte bir systemctl vermesi içindir; --root ile bile o zaman kullanılır.
  SYSTEMCTL="${HB_SYSTEMCTL:-systemctl}"
  SERVICE_CONTROL=1
  [[ $STAGING -eq 1 && -z "${HB_SYSTEMCTL:-}" ]] && SERVICE_CONTROL=0
  svc() { "$SYSTEMCTL" "$@"; }

  [[ -x "$BIN_PATH" && -f "$CONF_PATH" ]] || die "kurulum bulunamadı ($BIN_PATH / $CONF_PATH): önce 'install' çalıştırın; upgrade yalnızca var olan kurulumun binary'sini değiştirir"

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
  CUR_DESC="${CUR_VER:-bilinmiyor (sürüm bildirmiyor)}"

  if [[ "$ACTION" == rollback ]]; then
    [[ -x "$PREV_PATH" ]] || die "önceki binary yok ($PREV_PATH): geri dönülecek bir şey yok"
    PREV_VER="$(binary_version "$PREV_PATH" || true)"
    info "Geri alma: $CUR_DESC -> ${PREV_VER:-bilinmiyor (sürüm bildirmiyor)}"
    if [[ $DRY_RUN -eq 1 ]]; then info "Deneme (--dry-run): hiçbir şey değiştirilmedi"; exit 0; fi

    was_active=0; service_active && was_active=1
    # Yalnızca geri dönülecek binary --check-config biliyorsa (1.2.0+) yapılandırma denetlenir.
    prev_help="$("$PREV_PATH" --help 2>&1 || true)"
    if [[ "$prev_help" == *-check-config* ]]; then
      "$PREV_PATH" -config "$CONF_PATH" -check-config >/dev/null 2>&1 || die "önceki binary mevcut yapılandırmayı kabul etmiyor; geri alınmadı"
    fi
    cp -p "$BIN_PATH" "$BIN_PATH.swap"   # geri alınanı, rollback'in rollback'i için sakla
    swap_in "$PREV_PATH"
    mv -f "$BIN_PATH.swap" "$PREV_PATH"
    if [[ $NO_RESTART -eq 1 || $SERVICE_CONTROL -eq 0 ]]; then
      info "Binary değiştirildi; kullanmak için servisi yeniden başlatın: systemctl restart ${SERVICE_NAME}"
    elif [[ $was_active -eq 1 ]]; then
      if restart_and_verify; then
        info "Geri alındı; servis çalışıyor ($CUR_DESC binary'si $PREV_PATH olarak saklandı)"
      else
        # Geri alınan binary de ayakta kalmadı: eski durumu tekrar kur ve yüksek sesle bildir.
        cp -p "$BIN_PATH" "$BIN_PATH.swap"; swap_in "$PREV_PATH"; mv -f "$BIN_PATH.swap" "$PREV_PATH"
        svc restart "${SERVICE_NAME}.service" || true
        die "servis geri almadan sonra sağlıklı kalmadı; önceki durum geri kuruldu (bkz. journalctl -u ${SERVICE_NAME})"
      fi
    else
      info "Binary değiştirildi; servis çalışmıyordu, başlatılmadı"
    fi
    exit 0
  fi

  # ----- upgrade
  if [[ -z "$BINARY" ]]; then
    for candidate in "$SELF_DIR/healthbeat-agent" "$SELF_DIR/../healthbeat-agent"; do
      [[ -x "$candidate" ]] && { BINARY="$candidate"; break; }
    done
  fi
  [[ -n "$BINARY" && -x "$BINARY" ]] || die "yeni agent binary'si bulunamadı; derleyin (cd agent && go build -o healthbeat-agent ./cmd/agent) ya da --binary DOSYA verin"

  NEW_VER="$(binary_version "$BINARY")" || die "yeni binary sürüm bildirmiyor ('$BINARY --version' başarısız): başka bir mimari için derlenmiş ya da bir HealthBeat agent'ı değil; kurulmadı"
  if ! check_out="$("$BINARY" -config "$CONF_PATH" -check-config 2>&1)"; then
    die "yeni binary MEVCUT yapılandırmayı ($CONF_PATH) kabul etmiyor; hiçbir şey değiştirilmedi: ${check_out}"
  fi

  if [[ -n "$CUR_VER" && "$CUR_VER" == "$NEW_VER" ]] && cmp -s "$BINARY" "$BIN_PATH"; then
    info "Zaten güncel ($NEW_VER); yapılacak bir şey yok"
    exit 0
  fi
  if [[ -n "$CUR_VER" && $ALLOW_DOWNGRADE -eq 0 ]] && core_lt "$NEW_VER" "$CUR_VER"; then
    die "sürüm düşürülmedi: $CUR_VER -> $NEW_VER (isteniyorsa --allow-downgrade verin)"
  fi

  unit_drift=0
  if [[ -f "$UNIT_PATH" ]] && ! cmp -s "$SELF_DIR/healthbeat-agent.service" "$UNIT_PATH"; then unit_drift=1; fi

  info "Yükseltme: $CUR_DESC -> $NEW_VER (yapılandırma denetimi: tamam)"
  if [[ $unit_drift -eq 1 ]]; then
    if [[ $UPDATE_UNIT -eq 1 ]]; then info "Kurulu systemd unit'i betiğin yanındakinden farklı ve GÜNCELLENECEK (--update-unit)"
    else info "Not: kurulu systemd unit'i betiğin yanındakinden farklı; dokunulmadı (değiştirmek için --update-unit)"; fi
  fi
  if [[ $DRY_RUN -eq 1 ]]; then info "Deneme (--dry-run): hiçbir şey değiştirilmedi"; exit 0; fi

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
    info "$NEW_VER kuruldu; kullanmak için servisi yeniden başlatın: systemctl restart ${SERVICE_NAME}"
  elif [[ $was_active -eq 1 ]]; then
    info "${SERVICE_NAME} yeniden başlatılıyor ve ${VERIFY_SECONDS} sn izleniyor"
    if ! restart_and_verify; then
      info "Servis $NEW_VER ile sağlıklı kalmadı; $CUR_DESC geri konuyor"
      swap_in "$PREV_PATH"
      if [[ -n "$unit_backup" ]]; then mv -f "$unit_backup" "$UNIT_PATH"; svc daemon-reload || true; fi
      svc restart "${SERVICE_NAME}.service" || true
      die "$NEW_VER yükseltmesi başarısız oldu ve kendiliğinden geri alındı; yapılandırmaya dokunulmadı (bkz. journalctl -u ${SERVICE_NAME})"
    fi
  else
    info "Servis çalışmıyordu, başlatılmadı (başlatıldığında $NEW_VER kullanılır)"
  fi

  info "Yükseltildi: $CUR_DESC -> $NEW_VER"
  info "Önceki binary $PREV_PATH olarak saklandı (geri almak için: install.sh rollback); panelde bu sunucunun $NEW_VER gösterdiğini kontrol edin"
  plain_secret_notice "$BIN_PATH" "./install.sh encrypt"
  exit 0
fi

# ---------------------------------------------------------------- girdi yardımcıları
interactive() { [[ -t 0 && -t 1 ]]; }

# configure: hiç seçenek verilmediyse ve bir terminaldeyse adım adım sorar (wizard gibi).
if [[ $CONFIGURE_ONLY -eq 1 && $WIZARD -eq 0 && -z "$MODE" ]] && interactive; then WIZARD=1; fi

ask() { # ask DEĞİŞKEN "istem" [varsayılan]
  local var="$1" prompt="$2" default="${3:-}" reply
  interactive || die "$prompt: eksik (seçenek olarak verin; etkileşimli çalışmıyor)"
  read -r -p "$prompt${default:+ [$default]}: " reply
  printf -v "$var" '%s' "${reply:-$default}"
}

read_secret() { # read_secret DEĞİŞKEN ORTAMADI DOSYA "istem"
  local var="$1" envname="$2" file="$3" prompt="$4" value=""
  if [[ -n "$file" ]]; then
    [[ -r "$file" ]] || die "$file okunamıyor"
    value="$(head -n1 "$file" | tr -d '\r\n')"
  elif [[ -n "${!envname:-}" ]]; then
    value="${!envname}"
  else
    interactive || die "$prompt eksik: $envname ortam değişkenini ya da dosya seçeneğini verin veya etkileşimli çalıştırın"
    read -r -s -p "$prompt: " value
    echo
  fi
  printf -v "$var" '%s' "$value"
}

# Değerler printf ile yazılan bir JSON belgesinin içine girer; bu yüzden kaçırılmak yerine
# sıkı bir karakter kümesine karşı doğrulanır.
require_match() { # require_match DEĞER REGEX ne
  [[ "$1" =~ $2 ]] || die "geçersiz $3: '$1'"
}
readonly RE_UUID='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
readonly RE_SECRET='^[A-Za-z0-9_-]{20,}$'
readonly RE_URL='^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$'
readonly RE_IPCIDR='^[0-9A-Fa-f:.]+(/[0-9]{1,3})?$'
readonly RE_PATH='^/[A-Za-z0-9._/-]*$'

# encrypt_secret [strict]: SECRET'ı bu makinenin kimliğine bağlı şifreli biçime çevirir. Makine kimliği yoksa (ör.
# systemd'siz bir konteyner) uyarıp düz bırakır (agent düz değeri de kabul eder); "strict" ise (encrypt) hata verir.
encrypt_secret() {
  local enc
  [[ "$SECRET" != enc:v1:* ]] || return 0
  if enc="$(printf '%s\n' "$SECRET" | "${ENCRYPT_BIN:-$AGENT_BIN}" -encrypt-token 2>&1)" && [[ "$enc" =~ ^enc:v1:[A-Za-z0-9_-]+$ ]]; then
    SECRET="$enc"
  elif [[ "${1:-}" == strict ]]; then
    die "değer şifrelenemedi (${enc##*encrypt-token: }); $CONF_PATH değiştirilmedi"
  else
    echo "install.sh: UYARI: değer şifrelenemedi, düz metin olarak yazılıyor (${enc##*encrypt-token: })" >&2
  fi
}

json_array() { # json_array a,b,c -> ["a","b","c"]
  local IFS=',' item out="" first=1
  for item in $1; do
    [[ $first -eq 1 ]] || out+=","
    out+="\"$item\""; first=0
  done
  printf '[%s]' "$out"
}

# ---------------------------------------------------------------- yalnızca token / encrypt
# token: panelde kimlik bilgisi yenilendiğinde yeni değeri verir. encrypt: config'te düz duran değeri (eski kurulum,
# elle düzenleme) aynen şifreler; hiçbir şey sormaz, zaten şifreliyse dokunmaz. İkisinde de değer agent.json'daki tek
# alanın yerine yazılır (öteki ayarlara, izinlere ve sahibe dokunulmaz), sonuç gerçek agent'a denetletilir, servis
# yeniden başlatılır. Yedek alınmaz: eski dosya eski (belki düz) sırrı taşırdı.
if [[ "$ACTION" == token || "$ACTION" == encrypt ]]; then
  [[ -f "$CONF_PATH" ]] || die "yapılandırma yok ($CONF_PATH); önce agent'ı yapılandırın (healthbeat-agent-setup)"
  [[ -x "$AGENT_BIN" ]] || die "kurulu agent binary'si yok ($AGENT_BIN)"
  if grep -Eq '"mode"[[:space:]]*:[[:space:]]*"pull"' "$CONF_PATH"; then
    field=pull_secret what="pull secret" envname=HEALTHBEAT_PULL_SECRET file="$SECRET_FILE"
  else
    field=api_token what="API token" envname=HEALTHBEAT_API_TOKEN file="$TOKEN_FILE"
  fi
  re_field="(\"$field\"[[:space:]]*:[[:space:]]*\")[^\"]*\""
  grep -Eq "$re_field" "$CONF_PATH" || die "$CONF_PATH içinde $field alanı yok; yapılandırmayı yeniden yazın: healthbeat-agent-setup --force"

  if [[ "$ACTION" == token ]]; then
    read_secret SECRET "$envname" "$file" "Yeni $what"
    require_match "$SECRET" "$RE_SECRET" "$what (panelin bir kez gösterdiği değer beklenir)"
    encrypt_secret
  else
    SECRET="$(sed -nE "s/.*\"$field\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\1/p" "$CONF_PATH" | head -n1)"
    if [[ "$SECRET" == enc:v1:* ]]; then
      info "$field zaten şifreli; yapılacak bir şey yok"
      exit 0
    fi
    [[ "$SECRET" =~ $RE_SECRET ]] || die "$CONF_PATH içindeki $field geçerli bir $what değil; yeniden verin: healthbeat-agent-setup token"
    encrypt_secret strict
  fi

  tmp="$(mktemp "$CONF_DIR/.agent.json.XXXXXX")"
  trap 'rm -f "$tmp"' EXIT
  sed -E "s|$re_field|\1$SECRET\"|" "$CONF_PATH" >"$tmp"
  chmod 0640 "$tmp"
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$tmp"
  "$AGENT_BIN" -config "$tmp" -check-config >/dev/null 2>&1 || die "agent güncellenen yapılandırmayı kabul etmedi; $CONF_PATH değiştirilmedi"
  mv -f "$tmp" "$CONF_PATH"
  if [[ "$ACTION" == token ]]; then info "$field güncellendi: $CONF_PATH"; else info "$field şifrelendi: $CONF_PATH"; fi
  if [[ $NO_RESTART -eq 1 || $STAGING -eq 1 ]]; then
    info "Kullanmak için servisi yeniden başlatın: systemctl restart ${SERVICE_NAME}"
  elif systemctl is-enabled --quiet "${SERVICE_NAME}.service" 2>/dev/null || systemctl is-active --quiet "${SERVICE_NAME}.service"; then
    systemctl restart "${SERVICE_NAME}.service"
    info "Servis yeniden başlatıldı; logu izlemek için: journalctl -u ${SERVICE_NAME} -f"
  fi
  exit 0
fi

# ---------------------------------------------------------------- sihirbaz
# Adım adım kurulum (install.sh wizard; paketle kurulu agent'ta seçeneksiz healthbeat-agent-setup). Değişkenleri
# doldurur, bağlantıyı dener ve özeti onaylatır; yazma ve başlatma aşağıdaki ortak kurulum yolundan geçer.
# Görünüm: satırlar en çok 72 karakter, soru kendi satırında ve cevap altındaki "> " satırında, menüler alt alta
# numaralı. Var olan bir yapılandırmanın değerleri varsayılan olur.
WIZARD_DONE=0 KEEP_TLS=0 TOKEN_TAIL="" EXISTING_SECRET="" EXISTING_MODE="" CHECK_BIN=""
if [[ $WIZARD -eq 1 ]]; then
  interactive || die "sihirbaz bir terminal gerektirir; etkileşimsiz kurulum için seçenekleri verin (bkz. --help)"

  if [[ -t 1 && -z "${NO_COLOR:-}" && "${TERM:-dumb}" != dumb ]]; then
    C_B=$'\e[1m' C_D=$'\e[2m' C_G=$'\e[32m' C_R=$'\e[31m' C_0=$'\e[0m'
  else
    C_B="" C_D="" C_G="" C_R="" C_0=""
  fi

  # w_len METİN: görünen karakter sayısı (LC_ALL=C altında ${#} bayt sayar; UTF-8 devam baytları düşülür).
  w_len() {
    local cont
    cont="$(printf '%s' "$1" | tr -dc '\200-\277' | wc -c)"
    echo $((${#1} - cont))
  }
  w_pad() { printf '%s%*s' "$1" $(($2 - $(w_len "$1"))) ''; }
  # w_section: ok tuşlu terminalde ekran temizlenir, her adım tek başına görünür (kaydırma geçmişi silinmez).
  w_section() {
    local head="── Adım $1/6 · $2 " n
    n=$((60 - $(w_len "$head")))
    if [[ "${TERM:-dumb}" != dumb ]]; then
      printf '\e[H\e[2J%sHealthBeat agent kurulumu%s\n\n' "$C_B" "$C_0"
    fi
    if [[ $1 -eq 1 ]]; then
      printf '%s\n' "${W_INTRO[@]}"
      echo
    fi
    printf '%s%s' "$C_B" "$head"
    printf '─%.0s' $(seq 1 "$n")
    printf '%s\n\n' "$C_0"
  }
  w_ok() { printf '  %s✓%s %s\n' "$C_G" "$C_0" "$1"; }
  w_fail() {
    printf '  %s✗%s %s\n' "$C_R" "$C_0" "$1"
    shift
    local d
    for d in "$@"; do printf '    %s\n' "$d"; done
  }
  # w_err: geçersiz girişin altına (cevap satırına hizalı) yazılan hata.
  w_err() {
    printf '    %s✗%s %s\n' "$C_R" "$C_0" "$1"
    shift
    local d
    for d in "$@"; do printf '      %s\n' "$d"; done
    echo
  }
  w_text() {
    local l
    for l in "$@"; do printf '  %s\n' "$l"; done
  }
  w_ask() { # w_ask DEĞİŞKEN "soru" [varsayılan]
    local var="$1" q="$2" def="${3:-}" reply
    if [[ -n "$def" ]]; then printf '  %s [%s]\n' "$q" "$def"; else printf '  %s\n' "$q"; fi
    read -r -p "  > " reply || exit 1
    printf -v "$var" '%s' "${reply:-$def}"
  }
  w_pick() { # w_pick DEĞİŞKEN varsayılan seçenek_sayısı: alt alta yazılmış menüden seçim okur
    local var="$1" def="$2" n="$3" reply
    while :; do
      read -r -p "  Seçiminiz [$def]: " reply || exit 1
      reply="${reply:-$def}"
      [[ "$reply" =~ ^[0-9]+$ && "$reply" -ge 1 && "$reply" -le "$n" ]] && break
      printf '    %s✗%s 1 ile %d arasında bir sayı yazın.\n' "$C_R" "$C_0" "$n"
    done
    printf -v "$var" '%s' "$reply"
    echo
  }
  w_menu() { # w_menu DEĞİŞKEN "soru" varsayılan seçenek...
    local var="$1" q="$2" def="$3" i=1 o
    shift 3
    printf '  %s\n' "$q"
    for o in "$@"; do printf '    %d) %s\n' "$i" "$o"; i=$((i + 1)); done
    w_pick "$var" "$def" $#
  }
  w_yesno() { # w_yesno "soru" e|h -> evet ise 0
    local q="$1" def="$2" reply hint="e/H"
    [[ "$def" == e ]] && hint="E/h"
    while :; do
      read -r -p "  $q [$hint]: " reply || exit 1
      reply="${reply:-$def}"
      case "${reply,,}" in
        e | evet | y | yes) echo; return 0 ;;
        h | hayır | hayir | n | no) echo; return 1 ;;
      esac
    done
  }
  conf_str() { sed -nE "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\1/p" "$CONF_PATH" | head -n1; }
  conf_raw() { sed -nE "s/.*\"$1\"[[:space:]]*:[[:space:]]*([^,}\" ]+).*/\1/p" "$CONF_PATH" | head -n1; }
  conf_list() { sed -nE "s/.*\"$1\"[[:space:]]*:[[:space:]]*\[([^]]*)\].*/\1/p" "$CONF_PATH" | head -n1 | tr -d '" '; }

  # --- sorular
  # w_back_prompt DEĞİŞKEN "soru" "şu anki değer": menüden açılan soru; varsayılan yok, boş Enter geri döner (1).
  w_back_prompt() {
    local var="$1" reply
    printf '  %s (boş: geri dön)\n' "$2"
    [[ -z "$3" ]] || printf '  Şu anki değer: %s\n' "$3"
    read -r -p "  > " reply || exit 1
    printf -v "$var" '%s' "$reply"
    [[ -n "$reply" ]] || { echo; return 1; }
  }
  # w_ask_url [geri]: "geri" ile menüden açılır; boş Enter menüye döner (1).
  w_ask_url() {
    local v changed q="Server adresi (https://adres:port)"
    [[ -z "$SERVER_URL" ]] || q="Server adresi"
    while :; do
      changed=0
      if [[ "${1:-}" == geri ]]; then
        w_back_prompt v "Server adresi" "$SERVER_URL" || return 1
      else
        w_ask v "$q" "$SERVER_URL"
      fi
      v="${v%/}"
      if [[ "$v" == http://* ]]; then
        w_err "Adres https:// ile başlamalı; http token'ı" "şifresiz gönderirdi."
        continue
      fi
      [[ "$v" == *://* ]] || { v="https://$v"; changed=1; }
      if [[ ! "$v" =~ $RE_URL ]]; then
        w_err "Geçersiz adres. Örnek: https://hb.sirket.local:8443"
        continue
      fi
      [[ $changed -eq 0 ]] || printf '    %s olarak kullanılacak.\n' "$v"
      SERVER_URL="$v"
      echo
      return
    done
  }
  # w_ask_host_id [geri]: "geri" ile menüden açılır; boş Enter menüye döner (1).
  w_ask_host_id() {
    local v q="Host ID (panelde sunucunun Ayarlar → Bağlantı ve kimlik kartında)"
    [[ -z "$HOST_ID" ]] || q="Host ID"
    while :; do
      if [[ "${1:-}" == geri ]]; then
        w_back_prompt v "Host ID" "$HOST_ID" || return 1
      else
        w_ask v "$q" "$HOST_ID"
      fi
      if [[ "$v" =~ $RE_UUID ]]; then HOST_ID="$v"; echo; return; fi
      w_err "Host ID bir UUID olmalı, örneğin:" "96bddcba-a745-47b0-b11b-fff2ed2f0f5a"
    done
  }
  w_ask_secret() { # w_ask_secret "API token" | "Pull secret" [ask]: ortam değişkeni ya da dosya verildiyse sormaz ("ask" hariç)
    local what="$1" v envname=HEALTHBEAT_API_TOKEN file="$TOKEN_FILE"
    [[ "$what" == "API token" ]] || { envname=HEALTHBEAT_PULL_SECRET file="$SECRET_FILE"; }
    if [[ "${2:-}" != ask && ( -n "$file" || -n "${!envname:-}" ) ]]; then
      read_secret v "$envname" "$file" "$what"
      if [[ "$v" =~ $RE_SECRET ]]; then
        SECRET="$v" TOKEN_TAIL="${v: -4}"
        if [[ -n "$file" ]]; then w_text "$what dosyadan alındı."; else w_text "$what $envname değişkeninden alındı."; fi
        echo
        ENCRYPT_BIN="$CHECK_BIN" encrypt_secret
        return
      fi
      w_err "Verilen $what geçersiz; elle girin."
    fi
    while :; do
      if [[ -n "$EXISTING_SECRET" ]]; then
        printf '  %s (Enter: mevcut değer korunur)\n' "$what"
      elif [[ "${2:-}" == ask ]]; then
        printf '  %s (yazdığınız görünmez; boş: geri dön)\n' "$what"
      else
        printf '  %s (yazdığınız görünmez)\n' "$what"
      fi
      read -r -s -p "  > " v || exit 1
      if [[ -z "$v" && -z "$EXISTING_SECRET" && "${2:-}" == ask ]]; then
        printf '\n\n'
        return 1
      fi
      if [[ -z "$v" && -n "$EXISTING_SECRET" ]]; then
        printf '(mevcut)\n\n'
        SECRET="$EXISTING_SECRET" TOKEN_TAIL=""
        break
      fi
      printf '••••••••••••••••\n'
      if [[ "$v" =~ $RE_SECRET ]]; then
        SECRET="$v" TOKEN_TAIL="${v: -4}"
        echo
        break
      fi
      w_err "Geçersiz değer: panelin bir kez gösterdiği değeri" "olduğu gibi yapıştırın."
    done
    ENCRYPT_BIN="$CHECK_BIN" encrypt_secret
  }
  # w_ask_ca: boş Enter menüye döner (1 döndürür).
  w_ask_ca() {
    local v
    while :; do
      w_ask v "CA sertifikası (PEM dosya yolu; boş: geri dön)" "$CA_CERT"
      [[ -n "$v" ]] || { echo; return 1; }
      if [[ -r "$v" ]] && grep -q 'BEGIN CERTIFICATE' "$v"; then CA_CERT="$v" INSECURE=0; echo; return 0; fi
      w_err "Dosya okunamadı ya da PEM sertifikası içermiyor."
    done
  }

  # w_check: girilen değerlerle geçici bir yapılandırma yazıp agent -check-connection'ı çalıştırır; çıkış kodunu döndürür.
  w_check() {
    local d rc ca_field=""
    d="$(mktemp -d)"
    [[ -z "$CA_CERT" ]] || printf -v ca_field '  "ca_cert_file": "%s",\n' "$CA_CERT"
    (
      umask 0077
      printf '{\n  "mode": "push",\n  "server_url": "%s",\n  "host_id": "%s",\n  "api_token": "%s",\n  "interval_seconds": %s,\n%s  "insecure_skip_verify": %s\n}\n' \
        "$SERVER_URL" "$HOST_ID" "$SECRET" "$INTERVAL" "$ca_field" "$([[ $INSECURE -eq 1 ]] && echo true || echo false)" >"$d/agent.json"
    )
    set +e
    if [[ -n "$C_G" ]]; then
      "$CHECK_BIN" -config "$d/agent.json" -check-connection 2>"$d/err" | sed -u "s/✓/${C_G}✓${C_0}/; s/✗/${C_R}✗${C_0}/"
    else
      "$CHECK_BIN" -config "$d/agent.json" -check-connection 2>"$d/err"
    fi
    rc=${PIPESTATUS[0]}
    set -e
    if [[ $rc -eq 1 || $rc -eq 2 ]]; then
      w_fail "Bağlantı denenemedi:" "$(sed -E 's/^[0-9\/]+ [0-9:]+ //' "$d/err" | grep -v 'plain text' | head -n2 | cut -c1-64)"
    fi
    rm -rf "$d"
    echo
    return $rc
  }
  w_continue() {
    read -r -p "  Devam etmek için Enter'a basın." _ || exit 1
  }
  w_cancel() {
    w_text "İptal edildi; hiçbir şey değiştirilmedi."
    exit 0
  }

  # w_disks: disk listesi (agent -list-mounts) ve seçim; DISK_MOUNTS'u ayarlar.
  w_disks() {
    local -a paths=() labels=() sel=()
    local line p t b u size m i
    while IFS=$'\t' read -r p t b u; do
      [[ "$p" =~ $RE_PATH ]] || continue
      size="$(awk -v b="$b" 'BEGIN {
        if (b >= 1099511627776) printf "%.1f TB", b / 1099511627776
        else if (b >= 10737418240) printf "%.0f GB", b / 1073741824
        else if (b >= 1073741824) printf "%.1f GB", b / 1073741824
        else printf "%.0f MB", b / 1048576 }')"
      paths+=("$p")
      labels+=("$(printf '%-12s %-6s %7s  %%%s dolu' "$p" "$t" "$size" "$u")")
    done < <("$CHECK_BIN" -list-mounts 2>/dev/null || true)
    # Yapılandırmada olup şu an bağlı olmayan diskler kaybolmasın.
    IFS=',' read -r -a _cur <<<"$DISK_MOUNTS"
    for m in "${_cur[@]}"; do
      [[ "$m" == auto || -z "$m" ]] && continue
      if [[ " ${paths[*]} " != *" $m "* ]]; then
        paths+=("$m")
        labels+=("$(printf '%-12s (şu an bağlı değil)' "$m")")
      fi
    done
    [[ ${#paths[@]} -gt 0 ]] || { paths=("/"); labels=("/"); }
    for p in "${paths[@]}"; do
      if [[ ",$DISK_MOUNTS," == *",$p,"* ]]; then sel+=(1); else sel+=(0); fi
    done
    paths+=(auto)
    labels+=("Hepsi: sonradan takılan diskler de eklenir")
    if [[ ",$DISK_MOUNTS," == *",auto,"* ]]; then sel+=(1); else sel+=(0); fi

    local n=${#paths[@]} last=$((${#paths[@]} - 1)) cur=0 key rest any
    w_text "Raporlanacak diskler"
    if [[ "${TERM:-dumb}" == dumb ]]; then
      # Ok tuşu olmayan terminal: numaralı liste.
      for ((i = 0; i < n; i++)); do
        printf '    %d) [%s] %s\n' $((i + 1)) "$([[ ${sel[i]} -eq 1 ]] && echo x || echo ' ')" "${labels[i]}"
      done
      local reply ok
      while :; do
        read -r -p "  Seçilecek numaralar (ör. 1 2) [işaretliler]: " reply || exit 1
        [[ -z "$reply" ]] && break
        ok=1
        for i in $reply; do [[ "$i" =~ ^[0-9]+$ && "$i" -ge 1 && "$i" -le $n ]] || ok=0; done
        if [[ $ok -eq 1 ]]; then
          for ((i = 0; i < n; i++)); do sel[i]=0; done
          for i in $reply; do sel[i - 1]=1; done
          break
        fi
        printf '    %s✗%s 1 ile %d arasında numaralar yazın.\n' "$C_R" "$C_0" "$n"
      done
    else
      w_text "↑/↓ gezin · Boşluk seç/bırak · Enter onayla"
      echo
      w_draw() {
        local i mark box
        for ((i = 0; i < n; i++)); do
          mark=" "
          [[ $i -eq $cur ]] && mark="❯"
          if [[ ${sel[last]} -eq 1 && $i -ne $last ]]; then
            box="${C_D}[x]"
          elif [[ ${sel[i]} -eq 1 ]]; then
            box="[x]"
          else
            box="[ ]"
          fi
          printf '\e[2K  %s %s %s%s\n' "$mark" "$box" "${labels[i]}" "$C_0"
        done
      }
      printf '\e[?25l'
      trap 'printf "\e[?25h"' EXIT
      w_draw
      while :; do
        IFS= read -rsn1 key || exit 1
        case "$key" in
          $'\e')
            rest=""
            IFS= read -rsn2 -t 0.05 rest || true
            case "$rest" in
              '[A') [[ $cur -gt 0 ]] && cur=$((cur - 1)) ;;
              '[B') [[ $cur -lt $last ]] && cur=$((cur + 1)) ;;
            esac
            ;;
          k) [[ $cur -gt 0 ]] && cur=$((cur - 1)) ;;
          j) [[ $cur -lt $last ]] && cur=$((cur + 1)) ;;
          ' ')
            if [[ ${sel[last]} -eq 0 || $cur -eq $last ]]; then
              sel[cur]=$((1 - sel[cur]))
            fi
            ;;
          '')
            any=0
            for ((i = 0; i < n; i++)); do [[ ${sel[i]} -eq 1 ]] && any=1; done
            [[ $any -eq 1 ]] && break
            ;;
        esac
        printf '\e[%dA' "$n"
        w_draw
      done
      printf '\e[?25h'
      trap - EXIT
    fi
    echo
    if [[ ${sel[last]} -eq 1 ]]; then
      DISK_MOUNTS=auto
    else
      DISK_MOUNTS=""
      for ((i = 0; i < last; i++)); do
        [[ ${sel[i]} -eq 1 ]] && DISK_MOUNTS+="${DISK_MOUNTS:+,}${paths[i]}"
      done
      [[ -n "$DISK_MOUNTS" ]] || DISK_MOUNTS="/"
    fi
    w_text "Hangi disklerin alert üreteceği panelden seçilir."
    echo
  }

  # --- agent binary'si (bağlantı denemesi, disk listesi ve şifreleme için)
  if [[ $CONFIGURE_ONLY -eq 1 ]]; then
    CHECK_BIN="$AGENT_BIN"
    [[ -x "$CHECK_BIN" ]] || die "kurulu agent binary'si bulunamadı ($CHECK_BIN): önce paketi kurun"
  else
    if [[ -z "$BINARY" ]]; then
      for candidate in "$SELF_DIR/healthbeat-agent" "$SELF_DIR/../healthbeat-agent"; do
        [[ -x "$candidate" ]] && { BINARY="$candidate"; break; }
      done
    fi
    while [[ -z "$BINARY" || ! -x "$BINARY" ]]; do
      w_text "Bu betiğin yanında derlenmiş bir healthbeat-agent bulunamadı" "(derlemek için: cd agent && go build -o healthbeat-agent ./cmd/agent)."
      w_ask BINARY "healthbeat-agent binary'sinin yolu"
      echo
    done
    CHECK_BIN="$BINARY"
  fi

  # Giriş notu ilk adımın başında gösterilir (ekran temizlense de görünsün).
  W_INTRO=("Varsayılanı kabul etmek için Enter'a basın.")
  [[ "${TERM:-dumb}" != dumb ]] || printf '\n%sHealthBeat agent kurulumu%s\n' "$C_B" "$C_0"

  # --- var olan yapılandırma: değerleri varsayılan olur
  if [[ -f "$CONF_PATH" ]]; then
    W_INTRO+=("/etc/healthbeat/agent.json zaten var; mevcut değerler" "varsayılan olarak gösterilir. Değiştirmeyeceğiniz" "alanlarda Enter'a basın.")
    [[ -n "$MODE" ]] || MODE="$(conf_str mode)"
    [[ -n "$SERVER_URL" ]] || SERVER_URL="$(conf_str server_url)"
    [[ -n "$HOST_ID" ]] || HOST_ID="$(conf_str host_id)"
    [[ "$INTERVAL" != 30 ]] || INTERVAL="$(conf_raw interval_seconds)"
    INTERVAL="${INTERVAL:-30}"
    [[ "$(conf_raw insecure_skip_verify)" != true ]] || INSECURE=1
    [[ -n "$CA_CERT" || -z "$(conf_str ca_cert_file)" || ! -f "$CONF_DIR/ca.pem" ]] || CA_CERT="$CONF_DIR/ca.pem"
    [[ "$DISK_MOUNTS" != "/" ]] || DISK_MOUNTS="$(conf_list disk_mounts)"
    DISK_MOUNTS="${DISK_MOUNTS:-/}"
    [[ -n "$ALLOWED_IPS" ]] || ALLOWED_IPS="$(conf_list allowed_server_ips)"
    [[ "$LISTEN" != "0.0.0.0:9443" || -z "$(conf_str listen_addr)" ]] || LISTEN="$(conf_str listen_addr)"
    [[ "$ENDPOINT" != "/api/v1/status" || -z "$(conf_str pull_endpoint)" ]] || ENDPOINT="$(conf_str pull_endpoint)"
    [[ ! -f "$DROPIN_DIR/docker.conf" ]] || DOCKER=1
    EXISTING_MODE="$(conf_str mode)"
    EXISTING_MODE="${EXISTING_MODE:-push}"
  fi

  # --- 1. mod
  w_section 1 "Çalışma modu"
  w_text "1) push  Agent server'a bağlanıp rapor gönderir." \
    "         Bu makinede port açmak gerekmez. (önerilen)" \
    "2) pull  Server bu makineye bağlanıp sorgular." \
    "         Bu makinede gelen bağlantı için port açılır."
  echo
  choice=1
  [[ "$MODE" != pull ]] || choice=2
  w_pick choice "$choice" 2
  if [[ "$choice" == 2 ]]; then MODE=pull; else MODE=push; fi
  if [[ -f "$CONF_PATH" && "$MODE" == "$EXISTING_MODE" ]]; then
    if [[ "$MODE" == push ]]; then EXISTING_SECRET="$(conf_str api_token)"; else EXISTING_SECRET="$(conf_str pull_secret)"; fi
  fi

  # --- 2. bağlantı bilgileri
  w_section 2 "Server bağlantısı"
  if [[ "$MODE" == push ]]; then
    w_ask_url
    w_ask_host_id
    w_ask_secret "API token"
  else
    while :; do
      w_ask ALLOWED_IPS "Sorgulamasına izin verilen server IP'leri (virgülle)" "$ALLOWED_IPS"
      ok=1
      IFS=',' read -r -a _ips <<<"$ALLOWED_IPS"
      [[ ${#_ips[@]} -gt 0 ]] || ok=0
      for ip in "${_ips[@]}"; do [[ "$ip" =~ $RE_IPCIDR ]] || ok=0; done
      [[ $ok -eq 1 ]] && { echo; break; }
      w_err "IP ya da CIDR yazın, örneğin: 10.20.0.5 ya da 10.20.0.0/24"
    done
    w_ask_secret "Pull secret"
  fi

  # --- 3. bağlantı denemesi
  w_section 3 "Bağlantı denemesi"
  if [[ "$MODE" == pull ]]; then
    w_text "Pull modunda bağlantıyı server başlatır. Kurulumdan sonra" \
      "panelde sunucunun çevrimiçi olduğunu kontrol edin."
    echo
    w_continue
  else
    recheck=1
    while :; do
      if [[ $recheck -eq 1 ]]; then
        rc=0
        w_check || rc=$?
      fi
      recheck=1
      case $rc in
        0)
          w_continue
          break
          ;;
        10 | 11)
          w_menu act "Ne yapılsın?" 1 "Server adresini yeniden gir" "Yine de kaydet (server'a sonra ulaşılacak)" "Vazgeç"
          case $act in 1) { w_ask_url geri || recheck=0; } ;; 2) break ;; 3) w_cancel ;; esac
          ;;
        12)
          w_menu act "Ne yapılsın?" 1 "CA sertifikası dosyasını ver" "Sertifika doğrulamasını kapat (yalnızca test)" \
            "Server adresini yeniden gir" "Vazgeç"
          case $act in 1) w_ask_ca || recheck=0 ;; 2) INSECURE=1 CA_CERT="" ;; 3) { w_ask_url geri || recheck=0; } ;; 4) w_cancel ;; esac
          ;;
        13)
          w_menu act "Ne yapılsın?" 1 "Server adresini yeniden gir" "Sertifika doğrulamasını kapat (yalnızca test)" "Vazgeç"
          case $act in 1) { w_ask_url geri || recheck=0; } ;; 2) INSECURE=1 CA_CERT="" ;; 3) w_cancel ;; esac
          ;;
        15)
          w_menu act "Ne yapılsın?" 1 "Token'ı yeniden gir" "Host ID'yi yeniden gir" \
            "Yine de kaydet (server'a sonra ulaşılacak)" "Vazgeç"
          case $act in 1) EXISTING_SECRET="" && { w_ask_secret "API token" ask || recheck=0; } ;; 2) { w_ask_host_id geri || recheck=0; } ;; 3) break ;; 4) w_cancel ;; esac
          ;;
        *)
          w_menu act "Ne yapılsın?" 1 "Yeniden dene" "Server adresini yeniden gir" "Yine de kaydet" "Vazgeç"
          case $act in 1) ;; 2) { w_ask_url geri || recheck=0; } ;; 3) break ;; 4) w_cancel ;; esac
          ;;
      esac
    done
  fi

  # --- 4. izlenecekler
  w_section 4 "İzlenecekler"
  w_disks
  # HB_DOCKER_SOCK testlerin Docker sorusunu kapatması içindir.
  if [[ -S "${HB_DOCKER_SOCK:-/var/run/docker.sock}" ]] && getent group docker >/dev/null; then
    running=""
    if command -v docker >/dev/null; then
      running="$(timeout 5 docker ps -q 2>/dev/null | wc -l || true)"
    fi
    if [[ -n "$running" ]]; then
      w_text "Docker bu makinede kurulu ($running container çalışıyor)."
    else
      w_text "Docker bu makinede kurulu."
    fi
    w_text "İzlenirse agent servisi 'docker' grubuna eklenir."
    echo
    def=h
    [[ $DOCKER -eq 0 ]] || def=e
    if w_yesno "Container'lar izlensin mi?" "$def"; then DOCKER=1; else DOCKER=0; fi
  else
    DOCKER=0
  fi

  # --- 5. gelişmiş ayarlar
  w_section 5 "Gelişmiş ayarlar"
  if [[ "$MODE" == push ]]; then
    while :; do
      read -r -p "  Gönderim aralığı, saniye [$INTERVAL]: " v || exit 1
      v="${v:-$INTERVAL}"
      if [[ "$v" =~ ^[0-9]+$ && "$v" -ge 1 && "$v" -le 86400 ]]; then INTERVAL="$v"; echo; break; fi
      w_err "1 ile 86400 arasında bir sayı yazın."
    done
  else
    while :; do
      w_ask v "Dinleme adresi (adres:port, port 1024 ve üstü)" "$LISTEN"
      if [[ "$v" =~ ^[][A-Za-z0-9.:-]*:([0-9]{1,5})$ ]] && [[ "${BASH_REMATCH[1]}" -ge 1024 && "${BASH_REMATCH[1]}" -le 65535 ]]; then
        LISTEN="$v"
        echo
        break
      fi
      w_err "adres:port biçiminde yazın; port 1024 ile 65535 arası." "Örnek: 0.0.0.0:9443"
    done
    while :; do
      w_ask v "Status yolu" "$ENDPOINT"
      if [[ "$v" =~ $RE_PATH ]]; then ENDPOINT="$v"; echo; break; fi
      w_err "/ ile başlayan bir yol yazın, örneğin: /api/v1/status"
    done
    if [[ -f "$TLS_DIR/cert.pem" && -f "$TLS_DIR/key.pem" && -z "$TLS_CERT" ]] && w_yesno "Mevcut TLS sertifikası korunsun mu?" e; then
      KEEP_TLS=1
    else
      w_text "Agent HTTPS için bir sertifika kullanır; verilmezse" "kendinden imzalı bir sertifika üretilir (server onu" "doğrulamaz)."
      echo
      if w_yesno "Kendi sertifikanızı vermek ister misiniz?" h; then
        while :; do
          w_ask TLS_CERT "Sertifika (PEM dosya yolu)" "$TLS_CERT"
          [[ -r "$TLS_CERT" ]] && { echo; break; }
          w_err "Dosya okunamadı."
        done
        while :; do
          w_ask TLS_KEY "Özel anahtar (PEM dosya yolu)" "$TLS_KEY"
          [[ -r "$TLS_KEY" ]] && { echo; break; }
          w_err "Dosya okunamadı."
        done
      fi
    fi
  fi

  # --- 6. özet
  w_section 6 "Özet"
  if [[ -n "$TOKEN_TAIL" ]]; then token_view="••••••••…$TOKEN_TAIL (şifreli saklanacak)"; else token_view="mevcut değer korunuyor"; fi
  w_kv() { printf '  %s %s\n' "$(w_pad "$1" 16)" "$2"; }
  w_kv "Mod" "$MODE"
  if [[ "$MODE" == push ]]; then
    w_kv "Server" "$SERVER_URL"
    w_kv "Host ID" "$HOST_ID"
    w_kv "Token" "$token_view"
    if [[ -n "$CA_CERT" ]]; then
      w_kv "CA sertifikası" "${CA_CERT#"$ROOT"}"
    elif [[ $INSECURE -eq 1 ]]; then
      w_kv "Sertifika" "doğrulanmıyor (yalnızca test)"
    fi
  else
    w_kv "İzinli IP'ler" "${ALLOWED_IPS//,/, }"
    w_kv "Secret" "$token_view"
    w_kv "Dinleme" "$LISTEN$ENDPOINT"
    if [[ $KEEP_TLS -eq 1 ]]; then
      w_kv "TLS sertifikası" "mevcut korunuyor"
    elif [[ -n "$TLS_CERT" ]]; then
      w_kv "TLS sertifikası" "$TLS_CERT"
    else
      w_kv "TLS sertifikası" "kendinden imzalı üretilecek"
    fi
  fi
  if [[ "$DISK_MOUNTS" == auto ]]; then w_kv "Diskler" "hepsi (auto)"; else w_kv "Diskler" "${DISK_MOUNTS//,/, }"; fi
  if [[ $DOCKER -eq 1 ]]; then w_kv "Docker" "izleniyor"; else w_kv "Docker" "izlenmiyor"; fi
  [[ "$MODE" != push ]] || w_kv "Aralık" "$INTERVAL sn"
  echo
  w_yesno "Bu ayarlarla devam edilsin mi?" e || w_cancel

  [[ ! -f "$CONF_PATH" ]] || FORCE=1
  WIZARD=0 WIZARD_DONE=1
fi

# ---------------------------------------------------------------- topla + doğrula
[[ -n "$MODE" ]] || ask MODE "Mod (push ya da pull)" push
[[ "$MODE" == push || "$MODE" == pull ]] || die "--mode push ya da pull olmalı"

if [[ $CONFIGURE_ONLY -eq 1 ]]; then
  [[ -z "$BINARY" ]] || die "--binary configure'da kullanılmaz (yalnızca yapılandırma yazılır; binary paketten gelir)"
  [[ -x "${ROOT}/usr/bin/healthbeat-agent" || -x "$BIN_PATH" ]] || die "kurulu agent binary'si yok (${ROOT}/usr/bin/healthbeat-agent ve $BIN_PATH arandı): önce paketi kurun ya da tarball kurulumu için 'install.sh install' kullanın"
else
  if [[ -z "$BINARY" ]]; then
    for candidate in "$SELF_DIR/healthbeat-agent" "$SELF_DIR/../healthbeat-agent"; do
      [[ -x "$candidate" ]] && { BINARY="$candidate"; break; }
    done
  fi
  [[ -n "$BINARY" && -x "$BINARY" ]] || die "agent binary'si bulunamadı; derleyin (cd agent && go build -o healthbeat-agent ./cmd/agent) ya da --binary DOSYA verin"
fi

# Listelenen her mount düz, mutlak bir yol olmalı.
IFS=',' read -r -a _mounts <<<"$DISK_MOUNTS"
for m in "${_mounts[@]}"; do [[ "$m" == auto ]] || require_match "$m" "$RE_PATH" "disk (mutlak yol ya da auto)"; done

if [[ "$MODE" == push ]]; then
  [[ -n "$SERVER_URL" ]] || ask SERVER_URL "HealthBeat server adresi (https://adres[:port])"
  [[ -n "$HOST_ID" ]] || ask HOST_ID "Host id (panelde gösterilir)"
  SERVER_URL="${SERVER_URL%/}"
  require_match "$SERVER_URL" "$RE_URL" "--server-url (https://adres[:port] olmalı; düz http token'ı şifresiz gönderirdi)"
  require_match "$HOST_ID" "$RE_UUID" "--host-id (UUID beklenir)"
  [[ "$INTERVAL" =~ ^[0-9]+$ && "$INTERVAL" -ge 1 && "$INTERVAL" -le 86400 ]] || die "--interval 1..86400 saniye olmalı"
  if [[ -n "$CA_CERT" ]]; then
    [[ $INSECURE -eq 0 ]] || die "--ca-cert ile --insecure-skip-verify çelişiyor: server'ı CA ile doğrulayın ya da doğrulamayı kapatın, ikisini birden değil"
    [[ -r "$CA_CERT" ]] || die "--ca-cert okunamıyor: $CA_CERT"
    grep -q 'BEGIN CERTIFICATE' "$CA_CERT" || die "--ca-cert $CA_CERT PEM sertifika içermiyor"
  fi
  # Sihirbaz değeri zaten sordu (ve şifreledi ya da mevcut şifreli değeri korudu).
  [[ -n "$SECRET" ]] || read_secret SECRET HEALTHBEAT_API_TOKEN "$TOKEN_FILE" "API token"
  [[ "$SECRET" == enc:v1:* ]] || require_match "$SECRET" "$RE_SECRET" "API token (panelin bir kez gösterdiği token beklenir)"
else
  [[ -z "$CA_CERT" ]] || die "--ca-cert yalnızca push modunda geçerli (agent'ın bağlandığı server'ı doğrular)"
  [[ -n "$ALLOWED_IPS" ]] || ask ALLOWED_IPS "Bu agent'ı sorgulayacak HealthBeat server IP'si (virgülle ayrılmış)"
  IFS=',' read -r -a _ips <<<"$ALLOWED_IPS"
  for ip in "${_ips[@]}"; do require_match "$ip" "$RE_IPCIDR" "izin verilen IP/CIDR"; done
  # "]" köşeli ifadede ilk, "-" ise son olmalı ki gerçek karakter olsun.
  [[ "$LISTEN" =~ ^[][A-Za-z0-9.:-]*:([0-9]{1,5})$ ]] || die "--listen adres:port biçiminde olmalı"
  port="${BASH_REMATCH[1]}"
  [[ "$port" -ge 1024 && "$port" -le 65535 ]] || die "--listen portu 1024..65535 olmalı (servis yetkisiz kullanıcıyla çalışır)"
  require_match "$ENDPOINT" "$RE_PATH" "--endpoint"
  if [[ -n "$TLS_CERT" || -n "$TLS_KEY" ]]; then
    [[ -r "$TLS_CERT" && -r "$TLS_KEY" ]] || die "--tls-cert ve --tls-key ikisi de okunabilir dosya olmalı"
  fi
  [[ -n "$SECRET" ]] || read_secret SECRET HEALTHBEAT_PULL_SECRET "$SECRET_FILE" "Pull secret"
  [[ "$SECRET" == enc:v1:* ]] || require_match "$SECRET" "$RE_SECRET" "pull secret (panelin bir kez gösterdiği secret beklenir)"
fi

if [[ $DOCKER -eq 1 && $STAGING -eq 0 ]]; then
  getent group docker >/dev/null || die "--docker istendi ama bu makinede 'docker' grubu yok"
fi
if [[ -e "$CONF_PATH" && $FORCE -eq 0 ]]; then
  die "$CONF_PATH zaten var; üzerine yazmak için --force verin (yedek alınır)"
fi

# ---------------------------------------------------------------- kurulum
if [[ $STAGING -eq 0 ]] && ! id "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
  info "Sistem kullanıcısı oluşturuldu: $SERVICE_USER"
fi

if [[ $CONFIGURE_ONLY -eq 0 ]]; then
  install -D -m 0755 "$BINARY" "$BIN_PATH"
  info "Agent kuruldu: ${BIN_PATH#"$ROOT"}"
fi

install -d -m 0750 "$CONF_DIR"
[[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$CONF_DIR"

if [[ -e "$CONF_PATH" ]]; then
  backup="$CONF_PATH.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$CONF_PATH" "$backup"
  info "Önceki yapılandırma yedeklendi: ${backup#"$ROOT"}"
fi

encrypt_secret
write_config() {
  umask 0077
  if [[ "$MODE" == push ]]; then
    local ca_field=""
    [[ -z "$CA_CERT" ]] || printf -v ca_field '  "ca_cert_file": "%s",\n' "$LIVE_CA_PATH"
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
info "Yapılandırma yazıldı: /etc/healthbeat/agent.json"

# Yeniden yapılandırmada CA zaten yerindeyse (sihirbaz onu varsayılan yapar) kopyalanmaz.
if [[ -n "$CA_CERT" && "$CA_CERT" != "$CONF_DIR/ca.pem" ]]; then
  info "CA sertifikası kuruldu: /etc/healthbeat/ca.pem"
  install -m 0644 "$CA_CERT" "$CONF_DIR/ca.pem" # bir CA sertifikası herkese açıktır
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$CONF_DIR/ca.pem"
fi

if [[ "$MODE" == pull ]]; then
  install -d -m 0750 "$TLS_DIR"
  [[ $STAGING -eq 1 ]] || chown "root:$SERVICE_USER" "$TLS_DIR"
  if [[ $KEEP_TLS -eq 1 ]]; then
    : # sihirbazda mevcut sertifika korundu
  elif [[ -n "$TLS_CERT" ]]; then
    install -m 0644 "$TLS_CERT" "$TLS_DIR/cert.pem"
    install -m 0640 "$TLS_KEY" "$TLS_DIR/key.pem"
  else
    command -v openssl >/dev/null || die "openssl bulunamadı; kurun ya da --tls-cert/--tls-key verin"
    info "Kendinden imzalı TLS sertifikası üretiliyor (server doğrulamaz)"
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
  info "systemd servisi kuruldu"
  install -D -m 0644 "$SELF_DIR/healthbeat-agent.service" "$UNIT_PATH"
fi
if [[ $DOCKER -eq 1 ]]; then
  install -d -m 0755 "$DROPIN_DIR"
  printf '[Service]\n# Docker soketine (/var/run/docker.sock) erişim bu makinede root yetkisine denktir.\nSupplementaryGroups=docker\n' >"$DROPIN_DIR/docker.conf"
  if [[ $WIZARD_DONE -eq 1 ]]; then
    info "Agent servisi 'docker' grubuna eklendi"
  else
    info "Agent servisi 'docker' grubuna eklendi; Docker soketine erişim bu makinede root yetkisine denktir"
  fi
else
  rm -rf "$DROPIN_DIR"
fi

run_systemctl daemon-reload
if [[ $NO_START -eq 0 ]]; then
  START_TS="$(date '+%Y-%m-%d %H:%M:%S')"
  # restart: çalışan bir servis yeni yapılandırmayı ancak yeniden başlayınca okur (enable --now çalışanı başlatmaz).
  run_systemctl enable "${SERVICE_NAME}.service"
  run_systemctl restart "${SERVICE_NAME}.service"
  [[ $STAGING -eq 1 ]] || info "Servis başlatıldı"
  [[ $STAGING -eq 1 || $WIZARD_DONE -eq 1 ]] || info "Logu izlemek için: journalctl -u ${SERVICE_NAME} -f"
fi

if [[ $WIZARD_DONE -eq 1 ]]; then
  # Sihirbaz: servisin ilk raporunu (pull'da dinlemeye başlamasını) journal'da ~40 sn bekler.
  if [[ $NO_START -eq 0 && $STAGING -eq 0 ]] && command -v journalctl >/dev/null; then
    [[ "$MODE" == push ]] && want="pushed metrics" || want="listening on"
    seen=0
    for ((i = 0; i < 20; i++)); do
      out="$(journalctl -u "$SERVICE_NAME" --since "$START_TS" -o cat --no-pager 2>/dev/null || true)"
      if grep -q "$want" <<<"$out"; then
        [[ "$MODE" == push ]] && w_ok "İlk rapor gönderildi" || w_ok "Agent dinlemede ($LISTEN$ENDPOINT)"
        seen=1
        break
      fi
      line="$(grep -m1 -E 'push metrics:|yapılandırma hatası|pull server:' <<<"$out" || true)"
      if [[ -n "$line" ]]; then
        w_fail "Agent bir hata bildirdi:" "$(cut -c1-64 <<<"$line")"
        seen=1
        break
      fi
      sleep 2
    done
    [[ $seen -eq 1 ]] || w_fail "40 sn içinde rapor görülmedi; logu kontrol edin."
  fi
  echo
  if [[ "$MODE" == push ]]; then
    w_text "Sunucu birkaç saniye içinde panelde çevrimiçi görünür."
  else
    w_text "Panelde sunucunun çevrimiçi olduğunu kontrol edin. Bu makinede" "TCP ${port} portu server'a açık olmalı."
  fi
  w_text "Logu izlemek için:" "  journalctl -u ${SERVICE_NAME} -f"
  exit 0
fi

info "Tamamlandı (mod: $MODE, yapılandırma: /etc/healthbeat/agent.json)"
[[ "$MODE" == pull ]] && info "Güvenlik duvarında server'dan bu makineye TCP ${port} izni verin"
exit 0
