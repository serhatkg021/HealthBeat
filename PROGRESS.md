# HealthBeat — Geliştirme durumu

Bu dosya kısa tutulur: **şu anki durum, nasıl çalıştırılır, bilinen sınırlar ve sıradaki işler.** Tasarım kararları
`docs/MIMARI.md`'de, veritabanı `docs/VERITABANI.md`'de, geçmiş değişiklikler `agent/CHANGELOG.md` ve `server/CHANGELOG.md`'dedir.
Anlamlı bir iş bitince bu dosya güncellenir.

**Son güncelleme:** 2026-09-27 — Server bağımlılıkları güncellendi (`pgx` v5.11.0, `x/text` v0.42.0 ve diğerleri);
`govulncheck` kodun çağırdığı açık bulmuyor. Önceki adımlar: Go 1.27 (#27), store temizliği (#26), alert değerlendirme
bağlamı (#25), yanıt tipleri (#24), store hata nedenleri (#23), alert bildirim geçmişi (#22), kalıcı bildirim kuyruğu
(#21), alert motoru bölme (#20), ortak ingest (#19), erişim kapsamı (#18), handler hata akışı (#17), log/Faz A (#12–#16).
Sırada: E2b (CI'a `staticcheck`, `govulncheck`, Dependabot), E3 (`DB_MAX_CONNS`, `/readyz`), E4 (arka plan işleri
kapanışta beklenir).

## Durum

Sürüm **1.0.0** (agent ve server+panel ayrı hatlarda). Monitoring, alert, bildirim kuralları, organizasyon ağacı, panel: tamam.
Geliştirme sürecinden gelen tarih temizlendi: tek baseline migration, `client` → `agent`/`host` adlandırması, panel `server/panel/` altında.

## Ortam ve nasıl çalıştırılır

**Gereken araçlar:** Go ≥ 1.27 (agent için ≥ 1.22), PostgreSQL ≥ 15, Node ≥ 22, `openssl`; dağıtım testleri için Docker.

```sh
# Server (veritabanı testleri TEST_DATABASE_URL'de geçici şema açar, sonra siler; ASLA üretim veritabanına yöneltme)
cd server && TEST_DATABASE_URL='postgres://…' go test ./... -race
# Agent ve kurulum script'i
cd agent && go test ./... -race && ./deploy/install_test.sh
# Panel
cd server/panel && npx tsc -b && npm test && npm run build
# Sürüm betikleri ve agent-server uçtan uca uyumluluk (DATABASE_URL geçici şemada çalışır)
scripts/release_test.sh
DATABASE_URL='postgres://…' scripts/compat_e2e.sh
```

Çalıştırma: `cd server && go run ./cmd/server` (https://localhost:8443; şemayı kendisi kurar), panel `cd server/panel && npm run dev`
(http://localhost:5173). CLI: `healthbeat-server migrate status|up|baseline N`. Ayrıntı: `README.md`, `docs/DEPLOYMENT.md`.

## Altyapı özeti

- **Server (`server/`)**: `config`, `db`, `migrate`, `authsvc`, `rbac`, `store`, `alertengine` (cpu/ram/disk[mount başına]/docker_restart
  [container başına]/offline/disk_missing; bildirim kuralları; e-posta arka plan kuyruğunda), `notify` (SMTP), `offlinemonitor`,
  `pullscheduler`, `retention`, `secretbox`, `tlsreload`, `ratelimit`, `httpapi`, `testdb`/`testsmtp`; migration'lar binary'ye gömülü.
- **Agent (`agent/`)**: yalnızca stdlib; `config`, `collector` (cpu, memory, disk, docker, envanter), `pusher`, `pullserver`;
  `deploy/` (`install.sh`, systemd unit), `packaging/` (`.deb`/`.rpm`).
- **Panel (`server/panel/`)**: React 19 + TypeScript + Vite; `src/api` (fetch sarmalayıcı + JWT yenileme), `src/pages` (saf mantık
  modülleri `*.ts` Node test koşucusuyla test edilir), `src/types/api.ts` (Go modelleriyle senkron tutulur). Kendi `go.mod`'u vardır
  (yalnızca `node_modules`'un Go araçlarına görünmemesi için).

## Bilinen sınırlar

- Access token'lar tek tek iptal edilemez (≤ 15 dk).
- Pull agent sertifikası varsayılan olarak doğrulanmaz (`PULL_CA_CERT_FILE` ile açılır).
- Docker container geçmişi tutulmaz (yalnızca son durum).
- Bildirim kanalı yalnızca e-posta; SMS/Slack/Discord/Telegram şemada hazır, uygulama yok. İki faktörlü doğrulama alanları yalnızca saklanır.
- Operatör organizasyon düzeyinde bir şey (iletişim kişileri, kurallar listesi) göremez; yalnızca atandığı sunucuların kurallarını okur.

## Doğrulanmadı

- GHCR'dan `docker compose pull`, release paketinin gerçek makinede kurulumu,
  gerçek systemd/SELinux/arm64 çalışma zamanı (ayrıntı: `docs/DISTRIBUTION.md` §10).

## Sıradaki işler

1. Ek bildirim kanalları (SMS, Slack, Discord, Telegram) ve iki faktörlü doğrulama.
