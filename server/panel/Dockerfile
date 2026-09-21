# HealthBeat paneli: API server'ından ayrı, nginx ile sunulan static SPA.
#
#   docker build -t healthbeat-panel .
#   docker run -p 8080:8080 -e API_BASE_URL=https://api.example.com healthbeat-panel
#   docker run -p 8080:8080 -e API_PROXY_URL=https://server:8443 healthbeat-panel   # /api/ aynı origin'den proxy'lenir
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
# Yetkisiz nginx kullanıcısıyla çalışır; config.js'e yazabilmesi gerekir.
COPY --from=build --chown=nginx:nginx /app/dist /usr/share/nginx/html
COPY deploy/nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --chmod=755 deploy/docker-entrypoint.d/15-healthbeat-config.sh /docker-entrypoint.d/15-healthbeat-config.sh
ENV API_BASE_URL="" \
    API_PROXY_URL=""
EXPOSE 8080
