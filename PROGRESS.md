# HealthBeat — Geliştirme durumu

Bu dosya kısa tutulur: **şu anki durum, nasıl çalıştırılır, bilinen sınırlar ve sıradaki işler.** Tasarım kararları
`docs/MIMARI.md`'de, veritabanı `docs/VERITABANI.md`'de, geçmiş değişiklikler `agent/CHANGELOG.md` ve `server/CHANGELOG.md`'dedir.
Anlamlı bir iş bitince bu dosya güncellenir.

**Son güncelleme:** 2026-10-10 — **Sunucu sayfası konuya göre** (panel): Performans 7 konuya ayrıldı (şu an kutuları, alert
kuralları, eşik çizgili ve büyütülebilir grafikler, son rapor yan yana; menüde açık alert noktası), Envanter dört kart (Makine,
İşletim sistemi ve ağ, Saat, Bakım); sayfa açıklamaları kaldırıldı. Aynı gün: **Alert kuralları konuya göre**: üç kapsamda da kurallar 8 konuya gruplu (solda konu
menüsü), satırda Özelleştir / Devral / Geri al, kapsam başına tek Kaydet, devralınan değerin kaynağı yazılıyor; kural açıklamaları ⓘ düğmesinde. Önceki:
2026-10-08 — **Protokol 4: sistem sağlığı ve performans** (agent, server ve panel birlikte). Agent CPU/bellek
ayrıntısı, PSI, disk ve ağ G/Ç'si, TCP, systemd servisleri, sıcaklık, RAID, kapasite sınırları, süreçler, bekleyen güncellemeler
ve saat senkronu ayrıntısı topluyor (hepsi yetkisiz, sandbox gevşetilmeden). Server bunları ham saklıyor; disk gecikmesi,
sıcaklık, servis yeniden başlatma ve saat farkı eşikleri (süre koşuluyla, konu başına) ve 11 durum kuralı ile alert üretiyor;
yeni türler varsayılanda kapalı. Panelde Alert kuralları, Servisler, Performans, Envanter ve Genel sekmelerindeki "Yakında"
kartlarının yerini gerçek veri aldı. Migration `000007`. Bunlar "Yayınlanmamış"ta; server + panel **2.0.0** yayında. Ayrıca server Go 1.27.2'ye geçti
(Go 1.27.1 standart kütüphanesindeki güvenlik açıkları); staticcheck v0.8.1 Go 1.27.2'yi okuyamadığı için CI'da ve yerelde
geçici olarak Go 1.27.1 ile çalışıyor.

## Durum

Sürümler: **server + panel 2.0.0**, **agent 1.0.0** (ayrı hatlar; 2.0.0 ingest protokolünü değiştirmedi). Yayınlanmamış kod
ingest **protokol 4**'ü konuşur (agent ve server); eski agent'lar ve yayımlanmış server'larla uyumlu (`docs/COMPATIBILITY.md` §4). Monitoring, alert, bildirim kuralları, organizasyon ağacı, panel: tamam.
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
  [container başına]/offline/disk_missing; protokol 4: disk gecikmesi/sıcaklık/servis yeniden başlatma/saat farkı [konu başına,
  süre koşulu `alert_pending`'de] ve durum kuralları; sistem sahipleri + toplanan kurallar; alıcı başına kalıcı kuyruk), `notify` (SMTP),
  `settings` (panelden değişen ayarlar ve bildirim kanalları; bellekte, yeniden başlatmadan uygulanır), `offlinemonitor`,
  `pullscheduler`, `retention`, `secretbox`, `tlsreload`, `ratelimit`, `httpapi`, `testdb`/`testsmtp`; migration'lar binary'ye gömülü.
- **Agent (`agent/`)**: yalnızca stdlib; `config`, `collector` (cpu, memory, disk, docker, envanter; protokol 4: G/Ç ve ağ oranları,
  PSI, RAID, servisler, sıcaklık, süreçler, güncellemeler, kapasite, saat senkronu), `report` (hızlı kaynaklar rapor anında,
  yavaşlar arka planda), `pusher`, `pullserver`;
  `deploy/` (`install.sh`, systemd unit), `packaging/` (`.deb`/`.rpm`).
- **Panel (`server/panel/`)**: React 19 + TypeScript + Vite; `src/api` (fetch sarmalayıcı + JWT yenileme), `src/pages` (saf mantık
  modülleri `*.ts` Node test koşucusuyla test edilir), `src/types/api.ts` (Go modelleriyle senkron tutulur). Kendi `go.mod`'u vardır
  (yalnızca `node_modules`'un Go araçlarına görünmemesi için).

## Bilinen sınırlar

- Access token'lar tek tek iptal edilemez (≤ 15 dk).
- Pull agent sertifikası varsayılan olarak doğrulanmaz (`PULL_CA_CERT_FILE` ile açılır).
- Docker container geçmişi tutulmaz (yalnızca son durum). Protokol 4'ün anlık durumları (sıcaklık, RAID, süreçler, güncellemeler,
  saat senkronu) da geçmişsizdir; zaman serisi yalnızca CPU/bellek ayrıntısı, PSI, TCP, disk ve ağ G/Ç'sindedir.
- Bekleyen güncellemeler yalnızca apt ailesinde (Debian/Ubuntu) sayılır; timesyncd ayrıntısı systemd 240+ ister; sıcaklık
  yalnızca fiziksel makinede gelir.
- Bildirim kanalı yalnızca e-posta; SMS/Slack/Discord/Telegram için model hazır (kanal satırı + gönderici eklenir), uygulama yok.
  İki faktörlü doğrulama alanları yalnızca saklanır.
- Server tek kopya çalışır: panelden yapılan ayar değişikliği başka server kopyalarına duyurulmaz.
- Operatör organizasyon düzeyinde bir şey (iletişim kişileri, kurallar listesi) göremez; yalnızca atandığı sunucuların kurallarını okur.
- Bildirim → İletişim kişileri tüm organizasyonları tek tek sorgular (server'da toplu uç nokta yok); organizasyon sayısı çok
  artarsa toplu bir uç nokta gerekir.

## Doğrulanmadı

- GHCR'dan `docker compose pull`, release paketinin gerçek makinede kurulumu,
  gerçek systemd/SELinux/arm64 çalışma zamanı (ayrıntı: `docs/DISTRIBUTION.md` §10).

## Doğrulanacak (protokol 4)

- Uçtan uca deneme (hbe2e) yapıldı: gerçek agent, alert'lerin açılıp kapanması, süre koşulu ve bildirimler doğrulandı;
  bulunan gösterim kusurları düzeltildi. Kalan: düzeltmelerin ve koyu temanın, dar ekranın panelde elle kontrolü.

## Sıradaki işler

1. Sistem Araçları şimdilik salt okunur; kuyrukta "yeniden dene / iptal", cache'te "önbelleği boşalt" ileride ele alınabilir.
   Log Analiz bir günü baştan sona tarar: çok büyük günlerde (yüzlerce MB) yavaşlar, 15 sn'de zaman aşımına uğrar.
2. Ek bildirim kanalları (SMS; Slack/Discord/Telegram yalnızca sistem sahiplerine giden ortak kanallar) ve iki faktörlü doğrulama.
3. Panelde "Yakında" olarak yeri hazır olan özellikler: bakım pencereleri ve "bu alert kime gider?" önizlemesi.
4. Panel: sunucu ayarlarındaki salt okunur "Geçerli alert kuralları" da konuya göre gruplanabilir; Genel sekmesinin üstündeki
   Mod/Aralık kutuları sadeleşebilir. Çok dar ekranda (~420 px) sunucu sayfası başlığında ad görünmüyor (durum rozeti ve
   alert sayaçları yer kaplıyor).
5. Server container'ında healthcheck'in bıraktığı zombi süreçler (busybox `wget` → `ssl_client`; PID 1 Go binary'si onları
   toplamıyor): imaja init (tini) ya da healthcheck'i server binary'siyle yapmak — ayrı iş.
