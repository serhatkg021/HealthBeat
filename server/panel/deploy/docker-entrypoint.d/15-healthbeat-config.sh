#!/bin/sh
# Container açılırken API_BASE_URL'den config.js'i (SPA'nın çalışma zamanı yapılandırması) yazar.
# Adı 15- olduğu için imajın 20-envsubst adımından önce çalışır; o adım aynı değeri nginx CSP
# başlığına işler: geçersiz bir değer, iki yere de ulaşmadan container'ı durdurmalı.
# Aşağıdaki karakter aralıkları locale ne olursa olsun düz ASCII anlamına gelmeli (ör. tr_TR
# [A-Za-z]'yi bozar); bu yüzden LC_ALL=C.
set -eu

html_dir="${HB_HTML_DIR:-/usr/share/nginx/html}"
api="${API_BASE_URL:-}"
api="${api%/}"

if [ -n "$api" ] && ! printf '%s' "$api" | LC_ALL=C grep -Eq '^https?://[A-Za-z0-9.-]+(:[0-9]+)?$'; then
  echo "healthbeat: API_BASE_URL must be an origin such as https://api.example.com (no path, no quotes); got: $api" >&2
  exit 1
fi

# API_BASE_URL nginx şablonu tarafından da kullanılır; kırpılmış değeri ona görünür kıl.
export API_BASE_URL="$api"

# API_PROXY_URL (isteğe bağlı): doluysa nginx /api/ isteklerini bu origin'e (ör. https://server:8443)
# iletir; panel API'yi kendi origin'inden kullanır (API_BASE_URL boş, CORS ve sertifika onayı gerekmez).
# Ayrı bir dosyaya yazılır ve şablondan include edilir: envsubst koşullu blok üretemez.
proxy_conf="${HB_PROXY_CONF:-/tmp/healthbeat-api-proxy.conf}"
proxy="${API_PROXY_URL:-}"
proxy="${proxy%/}"
if [ -n "$proxy" ]; then
  if ! printf '%s' "$proxy" | LC_ALL=C grep -Eq '^https?://[A-Za-z0-9.-]+(:[0-9]+)?$'; then
    echo "healthbeat: API_PROXY_URL must be an origin such as https://server:8443 (no path, no quotes); got: $proxy" >&2
    exit 1
  fi
  # Adres her istekte yeniden çözülsün (docker'da container yeniden oluşunca IP değişebilir): bu yüzden
  # sabit bir proxy_pass yerine değişken + resolver kullanılır. Resolver /etc/resolv.conf'tan alınır
  # (docker'ın yerleşik DNS'i 127.0.0.11).
  resolver="$(awk '/^nameserver/ && $2 !~ /:/ { print $2; exit }' /etc/resolv.conf 2>/dev/null || true)"
  resolver="${resolver:-127.0.0.11}"
  cat > "$proxy_conf" <<CONF
# API_PROXY_URL=$proxy
location /api/ {
    resolver $resolver valid=10s ipv6=off;
    set \$api_upstream $proxy;
    proxy_pass \$api_upstream;
    proxy_http_version 1.1;
    proxy_set_header Host \$http_host;
    proxy_set_header X-Forwarded-For \$remote_addr;
    proxy_set_header X-Forwarded-Proto \$scheme;
    # Docker ağı içindeki bağlantı; server'ın sertifikası kendinden imzalı olabilir.
    proxy_ssl_server_name on;
    proxy_ssl_verify off;
    proxy_connect_timeout 5s;
    proxy_read_timeout 60s;
    expires epoch;
}
CONF
  echo "healthbeat: /api/ -> ${proxy} (proxy)"
else
  printf '# API_PROXY_URL tanimli degil: /api/ proxy edilmez\n' > "$proxy_conf"
fi

printf "window.HEALTHBEAT_CONFIG = { apiBaseUrl: '%s' }\n" "$api" > "$html_dir/config.js"
echo "healthbeat: config.js written (apiBaseUrl='${api}')"
