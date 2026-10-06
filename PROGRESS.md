# HealthBeat — Geliştirme durumu

Bu dosya kısa tutulur: **şu anki durum, nasıl çalıştırılır, bilinen sınırlar ve sıradaki işler.** Tasarım kararları
`docs/MIMARI.md`'de, veritabanı `docs/VERITABANI.md`'de, geçmiş değişiklikler `agent/CHANGELOG.md` ve `server/CHANGELOG.md`'dedir.
Anlamlı bir iş bitince bu dosya güncellenir.

**Son güncelleme:** 2026-10-06 — Panel yeni düzene geçti: menü İzleme / Alert Yönetimi / Yönetim gruplarına ayrıldı;
Sunucular, Alert kuralları ve Bildirim sayfaları tek yerde toplandı; sunucu sayfası altı sabit sekme; organizasyon ayarları
çarkla açılan pencerede; Denetim Kaydı Sistem Araçları'nda; Ctrl+K araması. Gelecek özelliklerin yerleri "Yakında · örnek
veri" olarak duruyor. Server + panel **2.0.0** yayında (2026-10-01); bunlar "Yayınlanmamış"ta.

## Durum

Sürümler: **server + panel 2.0.0**, **agent 1.0.0** (ayrı hatlar; 2.0.0 ingest protokolünü değiştirmedi). Monitoring, alert, bildirim kuralları, organizasyon ağacı, panel: tamam.
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
  [container başına]/offline/disk_missing; sistem sahipleri + toplanan kurallar; alıcı başına kalıcı kuyruk), `notify` (SMTP),
  `settings` (panelden değişen ayarlar ve bildirim kanalları; bellekte, yeniden başlatmadan uygulanır), `offlinemonitor`,
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
- Bildirim kanalı yalnızca e-posta; SMS/Slack/Discord/Telegram için model hazır (kanal satırı + gönderici eklenir), uygulama yok.
  İki faktörlü doğrulama alanları yalnızca saklanır.
- Server tek kopya çalışır: panelden yapılan ayar değişikliği başka server kopyalarına duyurulmaz.
- Operatör organizasyon düzeyinde bir şey (iletişim kişileri, kurallar listesi) göremez; yalnızca atandığı sunucuların kurallarını okur.
- Bildirim → İletişim kişileri tüm organizasyonları tek tek sorgular (server'da toplu uç nokta yok); organizasyon sayısı çok
  artarsa toplu bir uç nokta gerekir.

## Doğrulanmadı

- GHCR'dan `docker compose pull`, release paketinin gerçek makinede kurulumu,
  gerçek systemd/SELinux/arm64 çalışma zamanı (ayrıntı: `docs/DISTRIBUTION.md` §10).

## Sıradaki işler

1. Sistem Araçları şimdilik salt okunur; kuyrukta "yeniden dene / iptal", cache'te "önbelleği boşalt" ileride ele alınabilir.
   Log Analiz bir günü baştan sona tarar: çok büyük günlerde (yüzlerce MB) yavaşlar, 15 sn'de zaman aşımına uğrar.
2. Ek bildirim kanalları (SMS; Slack/Discord/Telegram yalnızca sistem sahiplerine giden ortak kanallar) ve iki faktörlü doğrulama.
3. Panelde "Yakında" olarak yeri hazır olan özellikler (her biri ayrı iş; çoğu agent protokol 4 gerektirir): sistem servisleri
   durumu, disk G/Ç (gecikme, hız, IOPS), ağ trafiği, sıcaklık, en çok kaynak kullanan süreçler, bekleyen güncellemeler,
   Docker sağlık durumu, yeni alert kuralı türleri, bakım pencereleri, "bu alert kime gider?" önizlemesi.
