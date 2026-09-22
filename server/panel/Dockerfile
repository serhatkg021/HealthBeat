# HealthBeat paneli: API server'ından ayrı, nginx ile sunulan static SPA. TLS'i kendisi sonlandırır —
# server'ınkiyle AYNI sertifika çiftini okur (aynı domain, farklı port; bkz. docker-compose.yml).
#
#   docker build -t healthbeat-panel .
#   docker run -p 8443:8443 -v healthbeat_certs:/certs:ro -e API_BASE_URL=https://api.example.com healthbeat-panel
#   docker run -p 8443:8443 -v healthbeat_certs:/certs:ro -e API_PROXY_URL=https://server:8443 healthbeat-panel   # /api/ aynı origin'den proxy'lenir
#
# /certs/cert.pem + key.pem kendi sağladığın (self-signed, CSR ile CA imzalı ya da satın alınmış) bir
# çift olabilir — server'ın certs-init'i gibi, dosya zaten varsa hiçbir şey buna dokunmaz.
#
# Tek imaj her ortamda çalışır: API_BASE_URL bundle'a gömülmez, açılışta uygulanır
# (config.js + CSP başlığı).

FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
# Sertifika dosyaları server'ın kullanıcısına (healthbeat, GID 10001) ait ve 640'tır (bkz.
# deploy/certs-init/init-certs.sh); nginx'i aynı gruba ekleyip özel anahtarı dünyaya açmadan
# okuyabilmesini sağlıyoruz. Bu adımlar için geçici olarak root, sonda yine yetkisiz kullanıcı.
USER root
RUN addgroup -g 10001 healthbeat && addgroup nginx healthbeat
USER nginx
# Yetkisiz nginx kullanıcısıyla çalışır; config.js'e yazabilmesi gerekir.
COPY --from=build --chown=nginx:nginx /app/dist /usr/share/nginx/html
COPY deploy/nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --chmod=755 deploy/docker-entrypoint.d/15-healthbeat-config.sh /docker-entrypoint.d/15-healthbeat-config.sh
ENV API_BASE_URL="" \
    API_PROXY_URL=""
EXPOSE 8443
