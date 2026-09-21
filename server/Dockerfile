# HealthBeat API server: tek statik Go binary'si (migration'lar içine gömülü).
#
#   docker build -t healthbeat-server ./server
#   docker run --env-file .env -v healthbeat_certs:/certs:ro -p 8443:8443 healthbeat-server
#
# Yapılandırma yalnızca ortam değişkenleriyle yapılır (docs/DEPLOYMENT.md). TLS sertifikası ve isteğe
# bağlı CA dosyası /certs volume'ünden okunur; container başka hiçbir yere dosya yazmaz.

FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthbeat-server ./cmd/server

FROM alpine:3.21
# ca-certificates: SMTP sunucusunun TLS sertifikasını doğrulamak için.
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 healthbeat \
 && adduser -S -u 10001 -G healthbeat -H -s /sbin/nologin healthbeat
COPY --from=build /out/healthbeat-server /usr/local/bin/healthbeat-server
USER 10001:10001
EXPOSE 8443
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD wget -q --no-check-certificate -O /dev/null https://127.0.0.1:8443/healthz || exit 1
ENTRYPOINT ["healthbeat-server"]
