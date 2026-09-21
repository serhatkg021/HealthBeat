#!/bin/sh
# docker compose için .env üretir: rastgele veritabanı şifresi, JWT sırları, şifreleme anahtarı ve
# ilk süper admin şifresi. Var olan .env'e DOKUNMAZ (üzerine yazmak, saklı pull secret'ları çözülemez
# hale getirir ve tüm oturumları kapatır).
#
#   ./deploy/init-env.sh [admin-eposta]
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
env_file="$root/.env"
admin_email="${1:-${BOOTSTRAP_ADMIN_EMAIL:-admin@example.com}}"

if [ -e "$env_file" ]; then
  echo "$env_file zaten var; dokunulmadı." >&2
  exit 0
fi
command -v openssl >/dev/null || { echo "openssl gerekli" >&2; exit 1; }

# Şifre: karışıklığa yol açan karakterler ve özel işaretler olmadan (compose/URL güvenli), 24 karakter.
admin_password="$(openssl rand -base64 48 | LC_ALL=C tr -dc 'A-Za-z0-9' | cut -c1-24)"

umask 077
sed \
  -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(openssl rand -hex 24)|" \
  -e "s|^JWT_ACCESS_SECRET=.*|JWT_ACCESS_SECRET=$(openssl rand -base64 48 | tr -d '\n')|" \
  -e "s|^JWT_REFRESH_SECRET=.*|JWT_REFRESH_SECRET=$(openssl rand -base64 48 | tr -d '\n')|" \
  -e "s|^SECRETS_ENCRYPTION_KEY=.*|SECRETS_ENCRYPTION_KEY=$(openssl rand -base64 32 | tr -d '\n')|" \
  -e "s|^BOOTSTRAP_ADMIN_EMAIL=.*|BOOTSTRAP_ADMIN_EMAIL=$admin_email|" \
  -e "s|^BOOTSTRAP_ADMIN_PASSWORD=.*|BOOTSTRAP_ADMIN_PASSWORD=$admin_password|" \
  "$root/.env.example" > "$env_file"

echo ".env üretildi: $env_file"
echo
echo "İlk giriş: $admin_email / $admin_password"
echo "(geçici şifre; ilk girişte yenisini belirlemen istenir. Bu satırı bir daha göremezsin — .env'de de durur)"
