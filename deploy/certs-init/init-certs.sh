#!/bin/sh
# /certs içinde cert.pem + key.pem yoksa kendinden imzalı bir çift üretir.
#
# HB_TLS_HOSTS: sertifikanın geçerli olacağı virgülle ayrılmış DNS adları / IP'ler
# (varsayılan: localhost,server). Agent'lar server'a hangi adresle bağlanıyorsa onu ekle.
# HB_TLS_UID: anahtarın sahibi olacak kullanıcı (server imajındaki kullanıcı: 10001).
set -eu

dir="${HB_CERTS_DIR:-/certs}"
hosts="${HB_TLS_HOSTS:-localhost,server}"
uid="${HB_TLS_UID:-10001}"

if [ -s "$dir/cert.pem" ] && [ -s "$dir/key.pem" ]; then
  echo "healthbeat: $dir/cert.pem ve key.pem zaten var; dokunulmadı"
  exit 0
fi
if [ -e "$dir/cert.pem" ] || [ -e "$dir/key.pem" ]; then
  echo "healthbeat: $dir içinde yalnızca biri var (cert.pem / key.pem); yarım bir çifti ezmemek için duruyorum" >&2
  exit 1
fi

san="IP:127.0.0.1"
old_ifs="$IFS"
IFS=','
for h in $hosts; do
  h="$(printf '%s' "$h" | tr -d ' ')"
  [ -n "$h" ] || continue
  # Yalnızca güvenli karakterler: değer openssl komut satırına gidiyor.
  if ! printf '%s' "$h" | LC_ALL=C grep -Eq '^[A-Za-z0-9.:_-]+$'; then
    echo "healthbeat: HB_TLS_HOSTS içinde geçersiz değer: $h" >&2
    exit 1
  fi
  if printf '%s' "$h" | grep -Eq '^[0-9.]+$|:'; then san="$san,IP:$h"; else san="$san,DNS:$h"; fi
done
IFS="$old_ifs"

mkdir -p "$dir"
umask 077
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
  -subj "/CN=healthbeat" -addext "subjectAltName=$san" \
  -keyout "$dir/key.pem" -out "$dir/cert.pem" 2>/dev/null

chown "$uid:$uid" "$dir/key.pem" "$dir/cert.pem"
chmod 600 "$dir/key.pem"
chmod 644 "$dir/cert.pem"
echo "healthbeat: kendinden imzalı sertifika üretildi ($san); gerçek sertifikanı $dir içine cert.pem/key.pem olarak koyarak değiştirebilirsin"
