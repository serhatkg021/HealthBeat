# HealthBeat — Geliştirme durumu

Bu dosya kısa tutulur: **şu anki durum, nasıl çalıştırılır, bilinen sınırlar ve sıradaki işler.** Tasarım kararları
`docs/MIMARI.md`'de, veritabanı `docs/VERITABANI.md`'de, geçmiş değişiklikler `agent/CHANGELOG.md` ve `server/CHANGELOG.md`'dedir.
Anlamlı bir iş bitince bu dosya güncellenir.

**Son güncelleme:** 2026-10-14 — Agent **1.1.0** sürümü hazırlandı (`agent/CHANGELOG.md`: öne çıkanlar ve
"Güncellemeden önce" notları): protokol 4, arka planda toplama, şifreli token, yeni kurulum sihirbazı, Türkçe kurulum.
Birleşince `main`'den `agent/v1.1.0` etiketi atılacak. Aynı gün server + panel **2.1.0** yayınlandı (`server/v2.1.0`):
protokol 4, bakım pencereleri, sadeleşen bildirim modeli, konuya göre panel, Sistem Araçları. Aynı gün: **DNS bilgisi** (agent, server ve panel; protokol 4'e eklendi, sürüm artmadı): agent
DNS yapılandırmasını 5 dakikada bir topluyor (systemd-resolved ya da doğrudan `resolv.conf`; sunucular, kullanılan sunucu,
alan adları, DNSSEC/DoT); server `system_state`'te saklıyor (migration yok). Yalnızca bilgi: ad çözme denemesi ve alert
yok. Envanter'in üst sırası "Makine ve işletim sistemi" ↔ "Ağ" oldu; DNS Ağ kartında. hbe2e'de test-1 ile denendi;
`compat_e2e.sh` yeni ve eski server/agent ile geçti. Aynı gün: Push sunucusunun **host id'si** panelde sunucu Ayarlar → Bağlantı ve kimlik kartında
kopyalanabilir duruyor (önceden yalnızca sunucu eklenirken gösteriliyordu); sihirbazın host id sorusu ve 401 mesajı bu
kartı gösteriyor. hbe2e'de test-1'de denendi. Aynı gün: **Agent kurulum sihirbazı yenilendi, kurulum betiği Türkçe** (yalnızca agent):
`install.sh wizard` / `healthbeat-agent-setup` 6 adımda soruyor ve kaydetmeden önce server'a bir deneme raporu gönderiyor
(yeni `-check-connection`); sertifika güvenilmiyor, adres ya da token yanlışsa ne yapılacağını soruyor. Diskler boyut ve
dolulukla seçilebilir listede (`-list-mounts`), var olan yapılandırmada mevcut değerler varsayılan. `install.sh`,
paket uyarıları, agent `-help` ve yapılandırma hataları Türkçe; çalışma logları İngilizce kaldı. Server ve protokol
değişmedi. hbe2e'de test-1 ile denendi: kendinden imzalı sertifikada CA verme ve doğrulamayı kapatma, yanlış token'da
401 menüsü, yeniden yapılandırmada Enter ile değerlerin korunması; sihirbazın yazdığı yapılandırmayla agent rapor gönderdi. Önceki: 2026-10-13 — **Agent token'ı `agent.json`'da şifreli** (agent, kurulum betiği ve panel metinleri): kurulum
API token'ı (pull'da secret'ı) makinenin kimliğine bağlı şifreli yazar (`enc:v1:…`, anahtar `/etc/machine-id`'den);
kopyalanan dosyadan token okunamaz. Düz değer çalışmaya devam eder (uyarı loglanır). Panelde kimlik bilgisi yenilenince
`healthbeat-agent-setup token` yalnızca token'ı değiştirir; panelin yenileme penceresi bu komutu söylüyor. Eski
kurulumlarda yükseltme düz token'ı algılayıp uyarır; `healthbeat-agent-setup encrypt` aynı token'ı soru sormadan
şifreler. Server ve
protokol değişmedi. hbe2e'de test-1 ile
denendi: şifreli token'la gönderim, panelden yenileme sonrası 401, `token` komutuyla geri gelme, başka makine
kimliğinde açık hata. Önceki: 2026-10-13 — **Panel tasarım toparlaması** (yalnızca panel): zamanlar her yerde kurulumun saat
diliminde ve tek biçimde (GG.AA.YYYY SS:DD:SS); düğme, rozet ve kart genişlikleri tutarlı. Sunucu sayfasının **Genel**
sekmesi bir özet oldu: Durum · Açık alert'ler · Agent, bildirim/kural özeti ve Alert kuralları konularına göre 9 kart (son
değer, durum çipi, açık alert, etkin kural sayısı; çevrimdışı sunucuda değerler renksiz ve "güncel değil"). Boş Docker
ve envanter tek açıklama kartında; alert tablolarında tekrar eden sütun ve rozetler kalktı. Kullanıcı ve kişi
tablolarında işlemler "⋯" menüsünde, iletişim kişisi ekleme pencerede. Telefonda sunucu ve alert listeleri iki satır;
dar ekranda başlık, tarih seçici ve formlar sığıyor. hbe2e'de protokol 4 agent'la (test-1) masaüstü, koyu tema ve
telefon görünümünde denendi. Önceki: 2026-10-13 — Bildirim → Kanallar kart düzeninde: E-posta özet kartı (ayar ve deneme gönderimi
pencerede), Slack, SMS ve Özel webhook "Yakında" kartları. Aynı gün: **Bildirim modeli sadeleşti** (server + panel): kanal "nasıl", bildirim kuralı "kime"
gider. "Sistem sahipleri" yerine bildirim kurallarında **Sistem** kapsamı (bütün sunucular; alıcı süper admin ya da sistem
kişisi); sistem sahipleri ile organizasyonların iletişim kişileri tek `contacts` tablosunda (organizasyonu boş = sistem
kişisi), Bildirim → Kişiler'de. Kanaldan sahip seviyesi kalktı. Kullanıcı alıcılar artık **gönderim anında da** erişime
göre süzülüyor (organizasyondan çıkarılan ya da rolü düşen biri o sunucuların bildirimini almıyor; önizlemede "erişimi
yok"). Migration `000009` yalnızca ekleme yapmıyor (tablo adı değişiyor, eski tablo ve sütun siliniyor); etkisi
`docs/COMPATIBILITY.md` §4'te. hbe2e'de mevcut veriyle migrate edilip panelde (açık/koyu, telefon) denendi. Önceki:
2026-10-11 — Sunucu sayfası: Ayarlar'daki geçerli alert kuralları konuya göre gruplu; Genel'de
"Aralık" kutusu Agent kutusuna katıldı. Aynı gün: telefonda sunucu sayfası başlığında durum rozeti yalnızca nokta; sunucu
adı artık görünüyor. Aynı gün: **"Bu alert kime gider?"** (server + panel): bir sunucu ve seviye için bildirimin şu an
kime gideceği, alıcının nereden geldiği ve kime neden gitmeyeceği; gerçek gönderimle aynı kurallarla hesaplanır
(`GET /api/v1/hosts/:id/notification-preview`). Bildirim kuralları sekmesinde ve sunucu sayfasında Ayarlar → Bildirimler'de;
"Bakım planla" sunucu başlığından Ayarlar → Bakım'a taşındı. hbe2e'de gerçek bir alert'in bildirimleriyle karşılaştırıldı.
Aynı gün: **Bakım pencereleri** (server + panel): tek seferlik ya da günlük / haftalık / aylık
tekrarlı pencereler, kapsam sunucular ve/veya organizasyonlar; bakımda alert'ler kaydediliyor, bildirim bakım bitince
(hâlâ açıksa) gidiyor; "Pencereyi bitir", "Bu tekrarı bitir", "Sıradaki tekrarı atla". Kurulum geneli **saat dilimi**
ayarı (tekrarlar ve e-postadaki saatler buna göre, ofsetiyle); panelin alt çubuğunda server saati. Migration `000008`.
hbe2e'de uçtan uca denendi (mailpit). Önceki: 2026-10-10 — Agent Go 1.27.2'ye geçti (Go 1.22'deki standart kütüphane açıkları); CI agent'ı da
staticcheck ve govulncheck ile denetliyor. Aynı gün: Server container'ında sağlık kontrolünün bıraktığı zombi süreçler giderildi: kontrolü
artık server binary'si yapıyor (`healthbeat-server healthcheck`). Aynı gün: **Sunucu sayfası konuya göre** (panel):
Performans 7 konuya ayrıldı (şu an kutuları, alert kuralları, eşik çizgili ve büyütülebilir grafikler, son rapor yan yana; menüde açık alert noktası), Envanter dört kart (Makine,
İşletim sistemi ve ağ, Saat, Bakım); sayfa açıklamaları kaldırıldı. Aynı gün: **Alert kuralları konuya göre**: üç kapsamda da kurallar 8 konuya gruplu (solda konu
menüsü), satırda Özelleştir / Devral / Geri al, kapsam başına tek Kaydet, devralınan değerin kaynağı yazılıyor; kural açıklamaları ⓘ düğmesinde. Kart ve form alanı açıklamaları panelin tamamında ⓘ düğmesinde. Önceki:
2026-10-08 — **Protokol 4: sistem sağlığı ve performans** (agent, server ve panel birlikte). Agent CPU/bellek
ayrıntısı, PSI, disk ve ağ G/Ç'si, TCP, systemd servisleri, sıcaklık, RAID, kapasite sınırları, süreçler, bekleyen güncellemeler
ve saat senkronu ayrıntısı topluyor (hepsi yetkisiz, sandbox gevşetilmeden). Server bunları ham saklıyor; disk gecikmesi,
sıcaklık, servis yeniden başlatma ve saat farkı eşikleri (süre koşuluyla, konu başına) ve 11 durum kuralı ile alert üretiyor;
yeni türler varsayılanda kapalı. Panelde Alert kuralları, Servisler, Performans, Envanter ve Genel sekmelerindeki "Yakında"
kartlarının yerini gerçek veri aldı. Migration `000007`. Bunlar "Yayınlanmamış"ta; server + panel **2.0.0** yayında. Ayrıca server Go 1.27.2'ye geçti
(Go 1.27.1 standart kütüphanesindeki güvenlik açıkları); staticcheck v0.8.1 Go 1.27.2'yi okuyamadığı için CI'da ve yerelde
geçici olarak Go 1.27.1 ile çalışıyor.

## Durum

Sürümler: **server + panel 2.1.0**, **agent 1.1.0** (ayrı hatlar; ikisi de ingest **protokol 4**'ü konuşur). Protokol 4 eski agent'lar ve server 2.0.0 ile uyumlu (`docs/COMPATIBILITY.md` §4). Monitoring, alert, bildirim kuralları, organizasyon ağacı, panel: tamam.
Geliştirme sürecinden gelen tarih temizlendi: tek baseline migration, `client` → `agent`/`host` adlandırması, panel `server/panel/` altında.

## Ortam ve nasıl çalıştırılır

**Gereken araçlar:** Go ≥ 1.27, PostgreSQL ≥ 15, Node ≥ 22, `openssl`; dağıtım testleri için Docker.

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
  süre koşulu `alert_pending`'de] ve durum kuralları; sistem + organizasyon zinciri + sunucu kuralları toplanır, kullanıcı
  alıcılar gönderimde erişime göre süzülür; alıcı başına kalıcı kuyruk), `notify` (SMTP),
  `settings` (panelden değişen ayarlar ve bildirim kanalları; bellekte, yeniden başlatmadan uygulanır), `maintenance` (bakım
  pencerelerinin tekrar hesabı; saf), `tz` (kurulumun saat dilimi, gömülü tzdata), `offlinemonitor`,
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
- Bakım bitince ertelenen bildirim en geç ~30 sn sonra gider (denetim aralığı). Bakım bütün alert türlerini kapsar; tür
  başına susturma yok. Bakımda açılıp çözülen alert'ler ve bakımda çözülen alert'in çözülmesi hiç bildirilmez.
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
2. Ek bildirim kanalları: webhook (Slack, Discord, Teams, Google Chat, genel JSON) her hedef bir `notification_channels`
   satırı olarak, kurallarda alıcısı kanalın kendisi; SMS. Ayrıca iki faktörlü doğrulama.
