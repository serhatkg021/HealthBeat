#!/bin/sh
# HealthBeat hazırlık adımı (docker compose `init` servisi): server ve panel başlamadan önce root olarak bir kez
# çalışır, kalıcı dizinleri hazırlar ve çıkar. Her `docker compose up`'ta yeniden çalışır; her bölüm tekrar
# çalıştırılabilir (hazır olan bir şeye dokunmaz, yalnızca sahipliği ve izinleri yeniden düzeltir).
#
#   /certs  TLS çifti (cert.pem + key.pem). Yoksa kendinden imzalı bir çift üretilir. Varsa (kendi sertifikan)
#           denetlenir; kullanılamıyorsa açık bir hatayla durulur, server başlamaz. Sahiplik/izin her seferinde düzeltilir:
#           elle kopyalanan (root'a ait, 600) dosyaları server okuyamazdı.
#   /logs   server'ın kalıcı log dizini (LOG_FILE): sahibi server kullanıcısı yapılır.
#
# PostgreSQL'in veri dizinine dokunulmaz: postgres imajı onu kendisi oluşturur ve sahipliğini ayarlar; db bu adımı
# beklemez (sertifika sorunu veritabanını durdurmasın).
#
# Ortam:
#   HB_TLS_HOSTS  kendinden imzalı sertifikanın geçerli olacağı virgülle ayrılmış DNS adları / IP'ler
#                 (varsayılan: localhost,server). Agent'lar server'a hangi adresle bağlanıyorsa onu ekle.
#   HB_TLS_UID    dosyaların sahibi olacak kullanıcı (server imajındaki kullanıcı: 10001).
#   HB_CERTS_DIR, HB_LOGS_DIR  dizinler (varsayılan /certs, /logs; testler için).
set -eu

certs="${HB_CERTS_DIR:-/certs}"
logs="${HB_LOGS_DIR:-/logs}"
hosts="${HB_TLS_HOSTS:-localhost,server}"
uid="${HB_TLS_UID:-10001}"

say() { echo "healthbeat-init: $*"; }
die() {
  echo "healthbeat-init: HATA: $*" >&2
  exit 1
}

# ---------------------------------------------------------------- sertifika

# generate_self_signed: HB_TLS_HOSTS için kendinden imzalı bir çift yazar.
generate_self_signed() {
  san="IP:127.0.0.1"
  old_ifs="$IFS"
  IFS=','
  for h in $hosts; do
    h="$(printf '%s' "$h" | tr -d ' ')"
    [ -n "$h" ] || continue
    # Yalnızca güvenli karakterler: değer openssl komut satırına gidiyor.
    if ! printf '%s' "$h" | LC_ALL=C grep -Eq '^[A-Za-z0-9.:_-]+$'; then
      die "HB_TLS_HOSTS içinde geçersiz değer: $h"
    fi
    if printf '%s' "$h" | grep -Eq '^[0-9.]+$|:'; then san="$san,IP:$h"; else san="$san,DNS:$h"; fi
  done
  IFS="$old_ifs"

  (
    umask 077
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
      -subj "/CN=healthbeat" -addext "subjectAltName=$san" \
      -keyout "$certs/key.pem" -out "$certs/cert.pem" 2>/dev/null
  ) || die "kendinden imzalı sertifika üretilemedi"
  say "kendinden imzalı sertifika üretildi ($san); kendi sertifikanı kullanmak için cert.pem ve key.pem'i certs dizinine koy"
}

# check_pair: var olan çifti denetler. Kullanılamayan bir çift hatadır (server açılsa da sunamaz ya da yanlış sertifikayı
# sunar); süresi dolmuş/dolmak üzere olan sertifika yalnızca uyarıdır (agent'lar zaten reddeder, panel uyarıyla açılır).
check_pair() {
  openssl x509 -in "$certs/cert.pem" -noout 2>/dev/null ||
    die "cert.pem okunamadı: PEM biçiminde bir sertifika (zincir) olmalı"
  key_pub="$(openssl pkey -in "$certs/key.pem" -pubout -passin pass: 2>/dev/null)" ||
    die "key.pem okunamadı: şifresiz, PEM biçiminde bir özel anahtar olmalı (şifreliyse: openssl pkey -in key.pem -out key.pem)"
  cert_pub="$(openssl x509 -in "$certs/cert.pem" -noout -pubkey 2>/dev/null)"
  [ "$key_pub" = "$cert_pub" ] ||
    die "key.pem, cert.pem'deki sertifikanın anahtarı değil (dosyanın ilk sertifikası sunucu sertifikası olmalı)"

  subject="$(openssl x509 -in "$certs/cert.pem" -noout -subject 2>/dev/null | sed 's/^subject=//')"
  not_after="$(openssl x509 -in "$certs/cert.pem" -noout -enddate 2>/dev/null | sed 's/^notAfter=//')"
  if ! openssl x509 -in "$certs/cert.pem" -noout -checkend 0 >/dev/null 2>&1; then
    say "UYARI: sertifikanın süresi dolmuş ($not_after); yenisini certs dizinine koyup docker compose up -d çalıştır"
  elif ! openssl x509 -in "$certs/cert.pem" -noout -checkend 2592000 >/dev/null 2>&1; then
    say "UYARI: sertifikanın süresi 30 günden az ($not_after)"
  fi
  say "sertifika hazır: $subject (bitiş: $not_after)"
}

mkdir -p "$certs"
if [ -e "$certs/cert.pem" ] || [ -e "$certs/key.pem" ]; then
  [ -s "$certs/cert.pem" ] && [ -s "$certs/key.pem" ] ||
    die "$certs içinde cert.pem ve key.pem birlikte olmalı (biri eksik ya da boş); yarım bir çifti ezmemek için duruyorum"
  check_pair
else
  generate_self_signed
fi

# Sahiplik/izin her seferinde: server (uid) okur, panel (nginx) aynı gruptadır ve anahtarı grup izniyle okur.
chown "$uid:$uid" "$certs/cert.pem" "$certs/key.pem"
chmod 644 "$certs/cert.pem"
chmod 640 "$certs/key.pem"

# ---------------------------------------------------------------- loglar

if [ -d "$logs" ]; then
  # -R: elle kopyalanmış (root'a ait) eski log dosyalarına da server'ın yazabilmesi için.
  chown -R "$uid:$uid" "$logs"
  chmod 750 "$logs"
  say "log dizini hazır: $logs"
fi
