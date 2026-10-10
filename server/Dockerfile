# HealthBeat API server: tek statik Go binary'si (migration'lar içine gömülü).
#
#   docker build -t healthbeat-server ./server
#   docker run --env-file .env -v healthbeat_certs:/certs:ro -p 8443:8443 healthbeat-server
#
# Yapılandırma yalnızca ortam değişkenleriyle yapılır (docs/DEPLOYMENT.md). TLS sertifikası ve isteğe
# bağlı CA dosyası /certs volume'ünden okunur; container başka hiçbir yere dosya yazmaz.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthbeat-server ./cmd/server

FROM alpine:3.24
# ca-certificates: SMTP sunucusunun TLS sertifikasını doğrulamak için.
# /var/log/healthbeat: LOG_FILE'ın dizini. Yeni bir named volume ilk bağlanışta bu dizinin sahipliğini alır; böylece
# 10001 olarak çalışan server içine yazabilir.
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 healthbeat \
 && adduser -S -u 10001 -G healthbeat -H -s /sbin/nologin healthbeat \
 && install -d -o 10001 -g 10001 -m 750 /var/log/healthbeat
COPY --from=build /out/healthbeat-server /usr/local/bin/healthbeat-server
USER 10001:10001
EXPOSE 8443
# Sağlık kontrolünü binary'nin kendisi yapar (HTTP_ADDR'deki porta, container'ın içinden): wget kullanılmaz, çünkü
# busybox wget'in TLS için açtığı ssl_client PID 1'e (server) kalıp zombi olarak birikiyordu.
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD ["healthbeat-server", "healthcheck"]
ENTRYPOINT ["healthbeat-server"]
