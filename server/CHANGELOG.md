# Server ve panel değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/) ilkelerini izler; sürümler [SemVer](https://semver.org/)'dir.
Server ve panel **birlikte** yayınlanır ve tek sürüm numarası taşır: `server/vX.Y.Z` etiketiyle (server, panel ve
certs-init imajları aynı sürümle GHCR'a gider; bkz. `docs/DISTRIBUTION.md`). Agent'tan bağımsızdır: agent'ın kendi
günlüğü `agent/CHANGELOG.md`. Her sürümün başlığı `## [X.Y.Z] - YYYY-AA-GG` biçimindedir:
`scripts/release-notes.sh server X.Y.Z` release notlarını buradan çıkarır. Panel değişiklikleri "Panel:" ile başlar.
Davranışı değiştirmeyen iç düzenlemeler, bölümün sonundaki "İç değişiklikler (davranış değişmedi)" başlığında listelenir
(bkz. `CONTRIBUTING.md` §3).

## [Yayınlanmamış]

## [2.0.0] - 2026-10-01

Ayarların ve bildirim kanallarının panele taşındığı, panel gezinmesinin yenilendiği sürüm. Agent sürümü değişmedi: 1.0.0
agent'lar bu server'la olduğu gibi çalışır (ingest protokolü aynı).

### Öne çıkanlar
- Çalışma zamanı ayarları panelde: 19 ortam değişkeni Ayarlar → Sistem Ayarları'na taşındı ve yeniden başlatmadan uygulanıyor.
- SMTP ayarı ve alert'leri alan sistem sahipleri panelden yönetiliyor; "Deneme gönder" ile doğrulanıyor.
- Yeni bildirim modeli: alert'ler sistem sahiplerine, organizasyon zincirinin ve sunucunun kurallarındaki ek alıcılara gidiyor;
  her alıcı ayrı ileti alıyor. Varsayılan alıcılar kaldırıldı.
- Panel gezinmesi yenilendi: sabit üst çubuk (sayfa başlığı, seviye başına açık alert sayıları, hesap menüsü), açılır gruplu
  sol menü, kartlı Ayarlar girişi, sabit alt çubuk (sürüm ve kayan sistem uyarıları).
- Profil sayfası: herkes kendi adını ve telefonunu değiştirebiliyor.
- Alert'ler seviyeye göre süzülüyor; dar ekranda menüler ve tablolar düzeltildi.

### Güncellemeden önce
- **Bu bir major sürümdür (2.0.0):** 19 ortam değişkeni artık okunmuyor ve bildirimlerin kime gittiği değişti. Agent'lar
  etkilenmez: agent 1.x bu server'la olduğu gibi çalışır (ingest protokolü aynı).
- **Yedek al.** Migration `000005` (ayar, kanal ve sistem sahibi tabloları) açılışta uygulanır. Geri dönüş `.down.sql` ya da
  yedekle olur; `.down.sql` panelden girilen ayarları, SMTP ayarını ve sistem sahiplerini siler (`docs/DISTRIBUTION.md` §8.3).
  Migration, `email` dışında bir kanala bağlı bildirim kuralı bulursa açıklayıcı bir hatayla durur (API bunları hiç kabul
  etmediği için beklenmez).
- **Güncellemeden sonra panelde Ayarlar → Sistem Ayarları'nı doldur:** e-posta kanalı (SMTP) **kapalı** başlar ve **sistem
  sahibi yoktur**; bunlar yapılana kadar alert bildirimleri kimseye gitmez (panel bunu alt çubuktaki uyarı şeridiyle söyler).
  Bildirim kanalları'nda
  SMTP ayarını gir, "Deneme gönder" ile doğrula ve kanalı aç; Sistem sahipleri'ni ekle; Panel adresi'ni ve gerekiyorsa agent
  sürüm politikasını, saklama sürelerini, oturum sürelerini, hız sınırlarını ve log ayarlarını gir.
- **`.env`'den taşınan değişkenleri sil:** `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`,
  `PANEL_BASE_URL`, `LATEST_AGENT_VERSION`, `MIN_SUPPORTED_AGENT_VERSION`, `METRICS_RETENTION_DAYS`,
  `AUDIT_RETENTION_DAYS`, `RESOLVED_ALERT_RETENTION_DAYS`, `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL`,
  `RATE_LIMIT_AUTH_FAILURES_PER_MINUTE`, `RATE_LIMIT_INGEST_PER_MINUTE`, `LOG_LEVEL`, `LOG_ERROR_BODY_BYTES`,
  `LOG_FILE_MAX_AGE_DAYS`, `LOG_FILE_MAX_TOTAL_MB`. Değerleri aktarılmaz; ortamda dolu kalanlar açılışta
  `<AD> is no longer read; manage it from the panel (Settings)` uyarısıyla listelenir. Geçersiz bir değer artık açılışı durdurmaz.
- **Bildirim kurallarını gözden geçir:** kural olmayan kapsamlarda artık süper adminlere ve organizasyon yöneticilerine
  otomatik e-posta gitmiyor; sunucu kuralı da organizasyon kurallarını geçersiz kılmıyor, ikisi birlikte uygulanıyor.

### Eklendi
- **Panel: Ayarlar sayfası** (`settings.view` okur, `settings.manage` değiştirir; varsayılan olarak yalnızca süper admin):
  sistem sahipleri, bildirim kanalları, agent sürüm politikası, veri saklama, oturum ve hız sınırları, panel adresi ve loglama.
  Değişiklikler yeniden başlatmadan uygulanır, varsayılandan farklı ayarlar işaretlenir ve "Varsayılana dön" ile geri alınır.
  API: `GET/PATCH /api/v1/settings`, `POST /api/v1/settings/reset`.
- **Sistem sahipleri:** her alert'in bildirimini alan kişiler ya da ortak adresler (panel kullanıcısı olmaları gerekmez).
  E-posta ve telefon bir kez girilir, sahip kanal başına almak isteyip istemediğini seçer. API: `/api/v1/notification-owners`.
- **Bildirim kanalları panelden yönetiliyor:** e-posta (SMTP) ayarı ve şifresi veritabanında (şifre `SECRETS_ENCRYPTION_KEY`
  ile şifreli, API'den asla okunmaz); ayarı eksik kanal açılamıyor; **Deneme gönder** kayıtlı ayarla, kanal kapalıyken de
  hemen bir e-posta yolluyor; kanal başına sistem sahiplerine hangi seviyeden itibaren gönderileceği seçiliyor (ör. e-posta
  uyarı, SMS yalnızca kritik). API: `GET /api/v1/notification-channels`, `PATCH …/{channel}`, `POST …/{channel}/test` (başarısız
  denemede `502`, `code: channel_test_failed`), kural ekranı için `GET …/options` (`notification.view`).
- **Panel: "bildirimler kimseye gitmiyor" uyarısı:** e-posta kanalı kapalıysa ya da e-posta alan bir sistem sahibi yoksa alt
  çubuktaki uyarı şeridinde görünür.
- **Panel: sabit üst çubuk.** Sayfa başlığı (geri okuyla) üstte sabit duruyor; sağında açık alert'lerin bilgi / uyarı / kritik
  sayıları (tıklanınca o seviyenin açık alert'leri) ve hesap menüsü (şifre değiştir, çıkış yap) var.
- **Panel: sabit alt çubuk.** Solda tek sürüm satırı (`HealthBeat Server X.Y.Z`), yanında kayan sistem uyarıları şeridi
  (üzerine gelince durur; "hareketi azalt" tercihinde kaymaz). Panel ve server sürümü farklıysa uyarı burada çıkıyor.
- **Profil sayfası:** herkes kendi görünen adını ve telefonunu değiştirebiliyor (hesap menüsü → Profil); e-posta ve rolü
  yalnızca bir yönetici değiştirir. API: `PATCH /api/v1/me` (`full_name`, `phone`; izin gerekmez, denetim kaydında
  `user.update_self`).
- **Alert'ler seviyeye göre süzülüyor:** `GET /api/v1/alerts?level=info|warning|critical`; panelde Alert'ler sayfasında seviye
  süzgeci (`?seviye=`). `GET /api/v1/dashboard/summary` yanıtına `open_info_alerts` eklendi.
- **Ayar değişiklikleri loglanıyor ve denetim kaydına yazılıyor:** `settings.update`, `settings.reset`,
  `notification_channel.update`, `notification_channel.test`, `notification_owner.create|update|delete` (eski ve yeni
  değerleriyle; şifre yalnızca "ayarlı mı" olarak). Panel: denetim kaydında "Ayarlar" ve "Bildirim" filtreleri.
- **Doğrulama hataları alan adıyla:** geçersiz bir ayar `400` ve `fields` ile ilgili alanı adlandırıyor; panel hatayı alanın
  altında, alanın biriminde (ör. dakika) gösteriyor.

### Değişti
- **Bildirimler sistem sahiplerine ve ek alıcılara gidiyor.** Alıcılar sistem sahipleri, sunucunun organizasyonunun ve
  bütün üst organizasyonlarının kuralları ve sunucunun kendi kurallarının toplamı; hiçbiri diğerini ezmiyor. **Varsayılan
  alıcılar kaldırıldı:** kural yoksa süper adminlere ve organizasyon yöneticilerine otomatik e-posta gitmiyor. Aynı adres
  tek ileti alıyor.
- **Her alıcı ayrı ileti alıyor:** bir alert e-postasının `To:` satırında artık tek alıcı var, alıcılar birbirini görmüyor.
  Bildirim kuyruğuna alıcı başına bir satır yazılıyor; bir adresin hatası yalnızca onun iletisini yeniden denetiyor.
  Panel: alert ayrıntısında bildirimler olay bazında gruplanıyor, her alıcının durumu ayrı görünüyor.
- **Bildirim kuralları yalnızca açık ve kişiye giden kanallara yazılabiliyor;** kanalı kapatılan kurallar silinmiyor ama
  çalışmıyor (atlanıp loglanıyor). Kural listelerinde `channel_enabled` alanı var; panel "Kanal kapalı — kural çalışmıyor"
  gösteriyor ve seçilemeyen kanalları "(ayar gerekli)" / "(kapalı)" diye işaretliyor.
- **Ayarlar ortamdan değil veritabanından okunuyor:** e-posta (SMTP), panel adresi, agent sürüm politikası, saklama süreleri,
  token süreleri, hız sınırları, log seviyesi, hata gövdesi loglama ve log dosyası sınırları (yukarıdaki 19 değişken).
  Varsayılanlar aynı kaldı; token süreleri artık sınırlı (erişim 1 dk – 24 saat, oturum 1 saat – 90 gün ve erişimden uzun).
- **"En güncel agent" elle yönetiliyor:** server'a gömülü değil; ilk kurulumda `1.0.0`, yeni bir agent yayınlanınca
  Ayarlar → Sistem Ayarları → Agent sürümleri'nden giriliyor. `scripts/release.sh agent` artık server'da bir sürüm kontrolü yapmıyor.
- **Log dosyası açılışta, ayarlar okunana kadar hiçbir dosya silmiyor** (panelde uzatılmış bir log geçmişi yeniden
  başlatmada varsayılan sınırla silinmesin diye). Açılış ve migration logları her zaman `info` seviyesinde.

- **Panel: sol menü yeniden düzenlendi.** Özet ve Alert'ler doğrudan bağlantı; Organizasyonlar ve Kullanıcılar açılır
  "Yönetim" grubunda; Ayarlar en altta sabit. Ayarlar artık bir giriş sayfası: Sistem Eşikleri, Denetim Kaydı ve Sistem
  Ayarları kartları (her biri kendi iznine göre görünür). Sistem ayarlarının adresi `/settings/system` oldu.
- `GET /api/v1/dashboard/summary` yanıtındaki `open_alerts` artık bilgi seviyesindeki açık alert'leri de sayıyor.
- **Panel: dar ekranda yana kayan menüler** (sekmeler, bölüm menüleri, süzgeçler) seçili öğeyi görünür alana getiriyor,
  görünmeyen öğe kalan kenarı solduruyor ve fare tekerleğiyle kayıyor.

### Düzeltildi
- Panel (mobil): bildirim kurallarında uzun e-posta adresi sayfayı yana kaydırmıyor; Sistem sahipleri kartında açıklama ile
  düğme üst üste binmiyor; Özet'teki sunucu araması kartın dışına taşmıyor; çok satırlı metin kutuları diğer alanlarla aynı
  yazı tipini kullanıyor.

### Kaldırıldı
- 19 ortam değişkeni (bkz. "Güncellemeden önce") ve `version.LatestAgent`.

### İç değişiklikler (davranış değişmedi)
- `internal/settings` (ayar ve kanal servisleri: bellekte kilitsiz okuma, abonelerle yeniden başlatmadan uygulama); rate
  limiter, token servisi, saklama işi ve log dosyası çalışırken değiştirilebilir; `notify.Notifier` tek alıcılı; `version.Compare`.
- Panel: geliştirme bağımlılıkları güncellendi — `oxlint` 1.86, `@types/node` 24.19.0.

## [1.1.0] - 2026-09-27

Güvenilirlik, gözlemlenebilirlik ve güvenlik sürümü. Agent sürümü değişmedi: 1.0.0 agent'lar bu server'la olduğu gibi
çalışır (ingest protokolü aynı).

### Öne çıkanlar
- Alert bildirimleri kalıcı kuyruktan, yeniden denemeyle gidiyor; server yeniden başlasa da kaybolmuyor. Panelde alert
  ayrıntısında her bildirimin kime, ne zaman gittiği görünüyor.
- Onaylanan alert "sustur ama izle": aynı olay için tekrar alert ve e-posta açılmıyor, eşik altına inince çözülüyor.
- İstek kimliği, yapılandırılmış log, kalıcı log dosyası; hata yanıtlarında makine-okur `code` ve `request_id`.
- `GET /readyz`, `DB_MAX_CONNS`, audit ve çözülmüş alert saklama süreleri (varsayılan sonsuz).
- Go 1.27 ve güncel bağımlılıklar; `pgx` ve `x/text`'teki iki güvenlik açığı kapandı.
- Panel menü ve düğmeleri izinlere göre gösteriyor; rol değişikliği sayfa yenilenince yansıyor.

### Güncellemeden önce
- **Yedek al.** Bu sürüm üç migration içerir (`000002` tek aktif alert, `000003` bildirim kuyruğu, `000004` indeks);
  varsayılan olarak açılışta uygulanır (`AUTO_MIGRATE`). Eski sürüme yalnızca `HB_VERSION`'ı değiştirerek dönülemez:
  `.down.sql` dosyalarıyla ya da yedekten (`docs/DISTRIBUTION.md` §8.3).
- **Server ile paneli birlikte güncelle.** Yeni panel yalnızca bu server'da olan uçları kullanır (`/metrics/latest`,
  `/alerts/:id/notifications`, `/me` izinleri).
- **Paneli kendi kurulumunla çalıştırıyorsan:** panel container'ı artık düz HTTP `8080` yerine TLS ile `8443` dinler ve
  sertifikayı server'la ortak `certs` volume'ünden okur. Önündeki yük dengeleyici ya da proxy buna göre ayarlanmalı.
  Depodaki `docker-compose.yml` zaten günceldir (`HB_PANEL_PORT` varsayılanı `443`).
- **Log metinlerine göre alarm kurduysan** log biçimi değişti (seviyeli, yapılandırılmış; bazı satırların adı değişti,
  aşağıda "Değişti").
- **API'yi panel dışında kullanıyorsan:** hata mesajlarındaki "çakışma: " / "bulunamadı: " öneki kalktı; `fields`
  anahtarları iç içe map'lerde anahtarı da içeriyor.

### Eklendi
- **`AUDIT_RETENTION_DAYS` ve `RESOLVED_ALERT_RETENTION_DAYS`:** denetim kayıtlarının ve çözülmüş alert'lerin saklanma
  süresi (gün). **Varsayılan `0` = sonsuza kadar sakla**; güncelleme hiçbir kaydı silmez. Ayarlanırsa metrik saklamayla
  aynı saatlik iş parti parti siler; açık ve onaylanmış alert'lere dokunulmaz. Docker Compose `.env`'den aktarıyor.
  Migration `000004` yalnızca bir indeks ekler (`alerts_resolved_at_idx`); geri dönüş: `000004_…down.sql`
  (`docs/DISTRIBUTION.md` §8.3).
- **`GET /readyz`:** veritabanına ping atar; ulaşılabiliyorsa `200`, değilse `503` (`code: database_unavailable`). Yük
  dengeleyici/orkestratör denetimi içindir; `/healthz` yalnızca sürecin ayakta olduğunu söylemeye devam ediyor.
- **`DB_MAX_CONNS`:** veritabanı bağlantı havuzunun üst sınırı (varsayılan değişmedi: 4 ile CPU sayısından büyüğü).
  Açılışta `database pool max_conns=…` loglanıyor. Docker Compose `.env`'den aktarıyor.
- **Alert'in bildirim geçmişi (`GET /alerts/:id/notifications`):** her bildirim olayı (açılma, seviye değişimi, çözülme)
  için kanal, durum (`sent` / `pending` / `failed`), deneme sayısı, oluşturulma ve gönderilme/vazgeçilme zamanı, bir
  sonraki deneme ve gönderilen konu/gövde. Alıcı adresleri ve son hata yalnızca `notification.view` izniyle döner
  (diğerleri `recipient_count` görür). İzin `alert.view`, kapsam alert'in sunucusu. Alert listeleri (`GET /alerts`) artık
  `notification_status` taşıyor: en az bir bildirimi gitmediyse `failed`, bekleyen varsa `pending`, hepsi gittiyse `sent`;
  bildirimi yoksa alan yok.
- **Panel: alert ayrıntısı ve "Bildirim" sütunu.** Alert'ler sayfasında ve sunucunun "Alert'ler" sekmesinde her satırda
  bildirim durumu rozeti ve "Ayrıntı" düğmesi; açılan yan panel alert bilgilerini ve bildirim geçmişini (kimlere, ne zaman
  gitti ya da neden gitmedi; içerik açılıp görülebilir) gösteriyor.
- **`TRUSTED_PROXIES`:** server'ın `X-Forwarded-For` başlığına güvendiği reverse proxy'ler (virgülle ayrılmış IP, CIDR ya da
  docker'daki `panel` gibi bir host adı; host adları 30 sn'de bir, çözülemedikleri sürece 2 sn'de bir ve tanınmayan bir
  eşten `X-Forwarded-For`'lu istek gelince hemen yeniden çözülür). İstemci IP'si yalnızca istek bunlardan
  birinden geldiğinde başlıktan okunur; boşsa (bare-metal varsayılanı) her zaman TCP eşidir. Docker Compose varsayılanı
  `panel`. `0.0.0.0/0` gibi herkese güvenen aralıklar reddedilir. Ayrıntı: `docs/DEPLOYMENT.md`, "Hız sınırları ve istemci IP'si".
- **`GET /me` rolün izinlerini de döndürüyor:** yanıta eklemeli `permissions` alanı (ör. `["alert.acknowledge", "host.view", …]`,
  sıralı); istemci bir işlemi göstermeden önce rol adına değil izne bakabilir. Diğer alanlar değişmedi.
- **`GET /hosts/:id/metrics/latest`:** host'un en son ham metrik örneği (`/metrics` dizisinin bir elemanıyla aynı biçim;
  host hiç rapor vermediyse `204`). Aynı izin (`host.view`) ve erişim kuralı.
- **İstek kimliği (`X-Request-ID`):** her yanıt bir istek kimliği taşır (istekte geçerli bir tane gelirse korunur); o isteğin
  bütün log satırlarında `request_id` olarak yer alır, ayrıca `user_id`/`host_id`/`ip`. Kullanıcının gördüğü bir hata logda
  kimlikle bulunabilir.
- **Hata alan isteklerin ayrıntısı loga yazılıyor:** `4xx`/`5xx` yanıtlarda query, izin listesindeki başlıklar, istek gövdesi ve
  hata yanıtı; en fazla `LOG_ERROR_BODY_BYTES` bayt (varsayılan `4096`, `0` = kapalı). Adında `password`/`token`/`secret`
  geçen alanlar `[REDACTED]` olur, `Authorization`/`Cookie` hiç yazılmaz. Gövdeler e-posta gibi kişisel veri içerebilir.
- **`LOG_LEVEL`** (`debug|info|warn|error`, varsayılan `info`) ve **`LOG_FORMAT`** (`text|json`, varsayılan `text`).
  Docker Compose ikisini ve `LOG_ERROR_BODY_BYTES`'ı `.env`'den aktarır. Ayrıntı: `docs/DEPLOYMENT.md`, "Loglama".
- **Hata yanıtlarında makine-okur `code` ve `request_id`:** her hata yanıtı artık `{"error", "code", "request_id"}` taşıyor
  (`fields` doğrulama hataları için ayrıldı). Kod durum kodundan gelir (`validation_failed`, `unauthorized`, `forbidden`,
  `not_found`, `conflict`, `rate_limited`, `internal`); mevcut özel kodlar (`password_change_required`, `reset_link_invalid`)
  aynen kaldı. Eklemeli: `error` alanı değişmedi. CORS `X-Request-ID`'yi açığa çıkarıyor (ayrı origin'deki panel okuyabilir).
  Biçim: `docs/MIMARI.md` §7, "Hata yanıtları".
- **Panel: sunucu hatalarında (5xx) hata kimliği gösteriliyor:** mesajın sonunda `(hata kimliği: …)`; kullanıcı onu
  yöneticiye iletir, yönetici logda o kimlikle ayrıntıyı bulur. Kullanıcının düzeltebileceği 4xx hatalarında gösterilmez.
- **Kalıcı log dosyası (`LOG_FILE`):** log stdout'a ek olarak günlük dosyalara yazılıyor (`server-YYYY-MM-DD.log`, önceki
  günler `.log.gz`); HTTP istek/yanıt satırları ve uygulama satırları aynı dosyada, `request_id` ile. Bugün dahil son
  `LOG_FILE_MAX_AGE_DAYS` gün (varsayılan 14) tutuluyor, toplam `LOG_FILE_MAX_TOTAL_MB` (varsayılan 1024) aşılırsa en eski
  günler siliniyor. Docker Compose'da varsayılan olarak açık ve yeni `logs` volume'ünde: `down` ve sürüm güncellemelerinden
  sonra kalıyor (önceden log yalnızca container'daydı, her güncellemede sıfırlanıyordu). Bare-metal'de varsayılan kapalı.
  Ayrıntı: `docs/DEPLOYMENT.md`, "Kalıcı log dosyası".
- **Panic kurtarma:** bir istekteki beklenmeyen hata artık bağlantıyı düşürmek yerine `500` ve `request_id`'li JSON yanıt
  döndürüyor, yığın izi loga yazılıyor. Arka plan işleri (pull scheduler, offline izleyici, retention, token temizliği,
  istemci IP çözücüsü) panic'lerse loglanıp 5 sn sonra yeniden başlatılıyor; alert e-postası ya da tek bir host'un poll'u
  panic'lerse yalnızca o iş düşüyor.

### Değişti
- **Panel: menü ve düğmeler rol yerine izinlere göre gösteriliyor.** Panel `GET /me`'deki izin listesini kullanıyor
  (önceden rol adlarıyla server'ın izin tablosunu elle kopyalıyordu); `role_permissions` değişirse panel de uyar. Rol ya
  da izin değişikliği artık yeniden giriş beklemeden sayfa yenilenince yansıyor. Alert "Onayla" düğmesi yalnızca
  `alert.acknowledge` iznine, sunucu ayarlarındaki bölümler kendi izinlerine (`host.update`, `host.delete`,
  `threshold.edit`, `notification.edit`) göre görünüyor.
- **Container taban imajları güncellendi:** server ve certs-init `alpine` 3.21 → 3.24; panel `nginx-unprivileged`
  1.27 → 1.31, panelin derleme aşaması `node` 22 → 24 (LTS). CI'daki panel işi de Node 24 kullanıyor.
- **Kapanışta arka plan işleri bekleniyor:** `SIGTERM`'de HTTP kapandıktan sonra pull zamanlayıcısı, offline izleyici,
  temizlik işleri ve bildirim işçilerinin durması beklenir (en çok 5 sn), veritabanı havuzu ancak sonra kapanır; yarıda
  kalan bir sorgu kapanmış havuza çarpıp hata loglamaz. Yeni log satırı: `background jobs stopped` (süre aşılırsa
  `background jobs did not stop in time` ve bitmeyen işlerin adı).
- **Server Go 1.27 ile derleniyor** (önceden 1.22; desteği bitmişti). Yan etki: istek gövdesi hatalarında `fields`
  anahtarı iç içe map'lerde anahtarı da içeriyor (`thresholds.warning_level` → `thresholds.cpu.warning_level`).
- **Alert değerlendirmesi rapor başına daha az sorgu çalıştırıyor:** sunucunun aktif alert'leri, eşikleri ve disk seçimi
  her kalem (metrik, mount, container) için ayrı ayrı değil, rapor başına bir kez okunuyor. Durum değişmeyen bir rapor
  artık mount ve container sayısından bağımsız olarak 3 sorgu (önceden 4 mount ve 5 container'la 26); alert davranışı aynı.
- **İş kuralı hata mesajlarında "çakışma: " / "bulunamadı: " öneki kalktı:** ör. `çakışma: bu e-posta zaten kullanımda`
  → `bu e-posta zaten kullanımda`. Durum kodları aynı. Bazı 404 metinleri netleşti (`üst organizasyon bulunamadı`,
  bildirim kuralında `organizasyon, sunucu, kullanıcı ya da iletişim kişisi bulunamadı`); sunucu eşiklerindeki İngilizce
  "warning_level and critical_level are both required" mesajı Türkçeleşti.
- **Bildirimler kalıcı kuyruktan, yeniden denemeyle gidiyor.** Alert e-postaları önceden bellekteki bir kuyruktaydı:
  server yeniden başlarsa ya da çökerse bekleyenler kayboluyor, SMTP hatasında yeniden denenmiyor, kuyruk dolunca yeni
  bildirimler atılıyordu. Artık bildirim alert değişikliğiyle aynı transaction'da yeni `notification_outbox` tablosuna
  yazılıyor (kanal başına bir satır; hangi olay için — açılma, seviye değişimi, çözülme — ve hangi seviyede gittiği ayrıca
  tutuluyor); işçi başarısızlıkta 30 sn'den başlayıp en çok 1 saate kadar geri çekilerek en çok
  10 kez deniyor, sonra vazgeçip ERROR logluyor. Bildirim kuyruğa yazılamazsa (veritabanı hatası) alert yine
  kaydediliyor, yalnızca o olayın bildirimi gitmiyor (ERROR loglanıyor); sonraki bildirimler (ör. "ÇÖZÜLDÜ") normal gidiyor. Şifre sıfırlama ve "şifreniz değiştirildi" e-postaları da aynı kuyruktan gidiyor:
  sıfırlama bağlantısı yalnızca şifreli (`SECRETS_ENCRYPTION_KEY`) saklanıyor ve gönderilince silinmiş oluyor, bağlantının
  süresiyle birlikte geçersiz oluyor; henüz gitmemişken yeni bağlantı istenirse eskisi gönderilmiyor. Alert bildirimleri
  alert'leri durdukça gövdesiyle saklanıyor (alert'in bildirim geçmişi); şifre e-postaları ve alert'i silinmiş (sunucusu
  silinen) bildirimler 30 gün sonra siliniyor. Log: `alert engine: send email` ve `password reset: send mail to user failed`
  yerine `notification delivery failed; will retry` (WARN) / `… giving up` (ERROR); `mail queue full` satırı kalktı.
  **Bu sürüm bir migration içerir (`000003`, yeni tablo):** eski sürüme yalnızca `HB_VERSION`'ı değiştirerek dönülemez;
  geri dönüş `000003_notification_outbox.down.sql` + geçmiş satırının silinmesiyle (bekleyen bildirimler de silinir) ya
  da güncelleme öncesi yedekle (`docs/DISTRIBUTION.md` §8.3).
- **Agent raporu alımındaki ikincil hata satırları birleşti:** push ve pull artık aynı satırları yazıyor, `host_id` ve
  `source=push|pull` alanlarıyla: `ingest: store docker containers` (önceden `ingest metrics: insert docker containers` /
  `pull scheduler: store docker containers`) ve `ingest: mark host online` (önceden `ingest metrics: mark online` /
  `pull scheduler: mark host online`). Bu metinleri arayan alarm kuralın varsa güncelle.
- **Çözülemeyen istek gövdesinde hatalı alan adlandırılıyor:** mesaj yine `geçersiz istek gövdesi`, ama sorun bir alana
  bağlanabiliyorsa yanıt artık `fields` taşıyor: yanlış türde değer (`{"interval_seconds": "tam sayı olmalı"}`) ya da
  bilinmeyen alan (`{"colour": "bilinmeyen alan"}`). Eklemeli: `error`, `code` ve durum kodları değişmedi. Şifre ve token
  uçları (`/auth/refresh`, `/auth/logout`, `/me/password`, `/auth/reset-password`, `/users/:id/hosts/by-organization`)
  çözülemeyen gövdeye eskisi gibi kendi "… zorunlu" mesajlarını veriyor. Beklenmeyen hataların log satırlarının bir kısmı
  işlem adıyla yeniden adlandırıldı (ör. `lookup host` → `get disk alerts: lookup host`; `failure` alanı kalktı).
- **Arka plan loglarına seviye ve alan:** alert motoru, pull scheduler, offline izleyici, retention, TLS yenileme ve istemci
  IP çözücüsünün satırları artık hepsi INFO değil: veritabanı hataları ve gönderilemeyen alert e-postaları `ERROR`,
  ulaşılamayan pull agent / dolan e-posta kuyruğu / uygulanmamış kanal `WARN` (önceden `LOG_LEVEL=warn`'da kayboluyordu).
  Metne gömülü değerler alanlara ayrıldı (`host_id`, `alert_id`, `metric`, `subject`, `mount`). Ingest sırasında yazılan
  alert motoru satırları o isteğin `request_id`'sini, pull poll'undakiler `poll-…` kimliğini taşıyor; gönderilemeyen bir
  alert e-postasının satırı alert'i açan isteğin kimliğini taşıyor. Pull agent'ın hata yanıtından loga en fazla 1 KB yazılıyor.
- **İstek satırında `duration` → `duration_ms`:** süre artık milisaniye (üç ondalık) olarak yazılıyor; önceden JSON'da
  nanosaniye tam sayısı, text'te `58.367201ms` gibi bir metindi. Bu alanı okuyan sorgu ya da alarm kuralın varsa güncelle.
- **Docker Compose: `docker compose logs`'un kopyasına boyut sınırı.** Docker'ın varsayılan `json-file` sürücüsü container
  logunu hiç döndürmüyordu; disk zamanla dolabilirdi. Artık `db`, `server`, `panel` ve `mailpit` için servis başına en fazla
  `HB_DOCKER_LOG_MAX_FILE` × `HB_DOCKER_LOG_MAX_SIZE` (varsayılan 3 × 10 MB, eskiler gzip'li) tutuluyor; daha eski satırlar
  `docker compose logs`'ta görünmez. Server'ın kalıcı geçmişi `logs` volume'ündeki dosyalarda (`LOG_FILE`). Yeni sınır
  container yeniden oluşturulunca (`docker compose up -d`) uygulanır.
- **Log biçimi değişti:** satırlar artık seviyeli ve yapılandırılmış (`time=… level=INFO msg="…" key=value`, ya da
  `LOG_FORMAT=json`). İstek satırında durum koduna göre seviye (`5xx` ERROR, `4xx` WARN); başarılı agent raporları
  (`POST /api/v1/metrics`) ve `/healthz` artık yalnızca `LOG_LEVEL=debug`'da görünüyor. Log metinlerini arayan betik ya da
  alarm kuralların varsa güncelle (ör. `WARNING: no super_admin exists` → `level=WARN msg="no super_admin exists …"`).
- **Panel: sunucu detayının "Genel" sekmesi artık yalnızca son okumayı indiriyor** (`/metrics/latest`). Önceden anlık kartlar
  için son 24 saatin bütün ham satırlarını (30 sn aralıkta ~2.900, 10 sn'de ~8.600 satır, her biri disk listesiyle) indirip
  yalnızca sonuncusunu kullanıyordu. Geçmiş grafiği ("Detay") değişmedi.
- **`GET /hosts/:id/metrics` artık hiçbir zaman kovalama/ortalama yapmıyor** — aralıktaki her ham örneği olduğu
  gibi döndürüyor (`max_points` parametresi kaldırıldı). Önceden geniş aralıklarda (varsayılan 24 saat) yanıtı ~1000
  noktaya sığdırmak için zaman kovalarına gruplayıp cpu/ram ortalaması alıyordu; bu, panelin "Genel" sekmesindeki
  anlık kartların bile kısa süreli bir tepe noktasını (ör. bir alert'i tetikleyen okuma) komşu örneklerle ortalanmış
  göstermesine yol açıyordu.
- **Alert çözüldüğünde de e-posta gönderiliyor** (önceden yalnızca açılma/yükselme bildirilirdi); bu, eşik altına dönme,
  seçili/raporlanan mount'tan kaybolma, "disk kayboldu"nun mount geri gelince çözülmesi, container'ın rapordan
  kaybolması ve host'un tekrar çevrimiçi olması dahil **her** çözülme yolunda geçerli. Konu satırı çözülmede
  seviye yerine "ÇÖZÜLDÜ" yazıyor, görsel olarak ayrılsın diye.
- **Alert seviyesi değiştiğinde (uyarı ↔ kritik, iki yönde de) o anki seviyenin TÜM alıcılarına tekrar mail gönderiliyor**,
  daha önce başka seviyede bilgilendirilmiş olsalar bile: uyarı mailini görüp "vaktim var" diyen biri durumun
  kritiğe döndüğünden, ya da tersine gereksiz endişelenmemesi için kritikten uyarıya düştüğünde habersiz kalmıyor.
  Yalnızca aynı seviyede kalmak yeni e-posta üretmiyor.
- **E-posta başlığı ve içeriği yeniden biçimlendirildi:** konu artık `[HealthBeat] -- <SEVİYE/ÇÖZÜLDÜ> / <Organizasyon> /
  <Title>(<IP>) - <açıklama>.` biçiminde; gövdede "Sunucu:" bölümü organizasyon, title, hostname ve IP'yi birlikte veriyor
  (150+ sunucu arasında yalnızca title yeterli tanımlayıcı değildi). Değerler iki ondalık ve virgül ayracıyla, birimiyle
  (yüzde ya da restart sayısı) yazılıyor; zamanlar `DD.MM.YYYY HH:MM:SS (UTC)` biçiminde açık dilim etiketiyle. Çözülme
  e-postaları ayrıca "Çözülme:" ve "Çözüm Süresi:" satırlarını taşıyor. `PanelBaseURL` ayarlıysa e-postaya alert'e
  doğrudan giden bir panel bağlantısı ekleniyor.
- **Panel artık TLS'ini kendisi sonlandırır, server'la AYNI sertifikayı okuyarak** (`certs` volume'ü artık panele de
  mount edilir; `nginx`, server'ın certs-init'inin ürettiği ya da operatörün sağladığı `cert.pem`/`key.pem`'i doğrudan
  okur — düz HTTP hiç sunulmaz). Panel imajı bu yüzden server'ın sertifika grubuna (GID 10001) eklendi. `HB_PANEL_PORT`
  varsayılanı `8080` → `443`; `HB_PANEL_BIND` yine `0.0.0.0` (artık güvenli, çünkü panel her zaman HTTPS). Ayrıntı:
  `docs/DEPLOYMENT.md`, "Gerçek sertifika kullanmak".

### Düzeltildi
- **Bağımlılıklardaki iki güvenlik açığı kapandı:** `pgx` v5.7.0 → v5.11.0 (GO-2026-5004, dolar tırnaklı metinlerde yer
  tutucu karışması) ve `golang.org/x/text` v0.22.0 → v0.42.0 (GO-2026-5970, bozuk girdide sonsuz döngü). Diğer Go
  bağımlılıkları da güncellendi (`x/crypto` v0.57.0, `golang-jwt` v5.3.1, `x/sync`, `puddle`).
- **Aramada `%` ve `_` artık joker değil:** sunucu, alert ve kullanıcı aramalarında (`?q=`) `web_1` yalnızca içinde
  `web_1` geçenleri buluyor (önceden `webx1` de eşleşiyordu), `%` her şeyi getirmiyor.
- **Eşleşmesiz kullanıcı araması `null` yerine `[]` döndürüyor** (`GET /users?q=…`).
- **Onaylanan alert artık "sustur ama izle":** önceden onaylanan bir alert hiç çözülmüyor, metrik hâlâ eşiğin üstündeyse bir
  sonraki raporda (≈30 sn içinde) aynı olay için yeni bir alert ve yeni bir e-posta açılıyordu. Artık onaylanan alert çözülene
  kadar aktif kalıyor: yeni alert/e-posta açılmıyor; eşik altına inince (ya da mount/container kaybolunca, sunucu tekrar rapor
  verince) çözülüyor ve "ÇÖZÜLDÜ" e-postası gidiyor. Seviye **yükselirse** (uyarı → kritik) e-posta gidiyor ve onay kalkıyor
  (alert yeniden açık); düşerse onay korunuyor. Migration `000002`, "tek aktif alert" kuralını onaylanmışları da kapsayacak
  şekilde değiştiriyor ve eski davranışın bıraktığı kopyalarda (aynı sunucu + tür + konu için onaylanmış eski olay + yeniden
  açılmış olay) en yenisi dışındakileri çözülmüş sayıyor (e-posta gönderilmez). Özet ekranındaki "açık alert" sayısı yine
  yalnızca onaylanmamışları sayıyor. **Bu sürüm bir migration içerir:** 1.0.x'e yalnızca `HB_VERSION`'ı değiştirerek
  dönülemez (eski server bilmediği migration'ı görünce açılmaz); geri dönüş `000002_alert_acknowledged_active.down.sql` +
  geçmiş satırının silinmesiyle ya da güncelleme öncesi yedekle (`docs/DISTRIBUTION.md` §8.3).
- **Belgeler: geri alma adımları düzeltildi.** `docs/DISTRIBUTION.md` §8.3 ve `docs/COMPATIBILITY.md` kural 6, eski
  server'ın yeni şemayla açılabileceğini söylüyordu; migration aracı bunu bilerek reddediyor. Artık iki doğru yol
  (`.down.sql` ile veriyi koruyarak ya da yedekten) anlatılıyor.
- **Panel üzerinden gelen bütün kullanıcılar tek IP görünüyordu:** Docker Compose'da panel `/api/`'yi server'a proxy'lediği
  için server her isteği panel container'ının IP'sinden geliyor sanıyordu. Sonuçları: tek bir kişinin hatalı girişleri
  (dakikada ~10) **herkesin** girişini ve oturum yenilemesini 429 ile engelleyebiliyordu (yenileme düşünce oturumlar
  kapanıyordu), şifre sıfırlama sınırı herkes için ortaktı ve denetim kaydındaki IP her zaman container'ın adresiydi.
  Artık panel güvenilir proxy (`TRUSTED_PROXIES=panel`) olarak tanımlı ve gerçek istemci IP'si kullanılıyor; server'a
  doğrudan bağlanan birinin gönderdiği sahte `X-Forwarded-For` yok sayılıyor.

### İç değişiklikler (davranış değişmedi)
- Panel: geliştirme bağımlılıkları güncellendi — TypeScript 6 → 7, `vite` 8.3.1, `lucide-react` 1.48, `oxlint` 1.85,
  `@types/node` 24.13.6.
- Arka plan döngüleri `internal/jobs` çalıştırıcısında toplandı; süresi dolmuş token temizliği `httpapi`'den
  `retention.TokenPurger`'a taşındı.
- `staticcheck`'in bulduğu kullanılmayan kod (`routeScope` tipi, bir test yardımcısı) silindi.
- Store'daki satır okuma döngüleri `pgx.CollectRows` üstündeki ortak `collect` / `collectIDs` yardımcılarına taşındı; SQL'e
  gömülü rol adları parametre oldu. Çözülemeyen sunucu envanteri artık sessizce yutulmuyor, `hosts: decode inventory`
  uyarısıyla loglanıyor.
- Kullanıcı okumaları şifre hash'ini seçmiyor; hash yalnızca giriş ve şifre değiştirmede ayrı metotlarla
  (`GetByEmailWithHash`, `GetByIDWithHash`) okunuyor. Organizasyonun erişim bilgisi modelden `organizationResponse`'a taşındı.
- Store iş kuralı hataları adlandırılmış nedenler oldu (`store.ErrEmailTaken`, `ErrHostTitleTaken`…; `ErrNotFound` ya da
  `ErrConflict`'i sarar); Türkçe metinler ve durum kodları `httpapi/store_errors.go`'da tek tabloda.
- Alert motoru bölündü (`engine.go` değerlendirme, `dispatch.go` kuyruk ve teslim, `message.go` bildirim metni) ve depolara
  dar arayüzlerle bağlandı; bildirimler kanal arayüzünden (`notify.Notifier`, e-posta için `notify.EmailChannel`) gidiyor.
  Yeni bir kanal (SMS, Slack…) yeni bir `Notifier`'dır. E-posta metni ve kuyruk davranışı aynı.
- Push ve pull alımı ortak `internal/ingest` servisine taşındı: `Decode` (çözme + doğrulama) ve `Service.Record` (metrik,
  container'lar, çevrimiçi işareti, alert motoru) iki yolun da tek kopyası.
- Erişim kapsamı `internal/access` paketinde toplandı: istek başına bir `Scope`, organizasyon/sunucu atamalarını istek içinde
  bir kez okur (önceden aynı istekte denetim başına bir sorgu); handler'lardaki kapsam fonksiyonları ve rol `switch`'leri
  onu kullanıyor. Rol izinleri 60 sn bellekte tutuluyor (`role_permissions`'ı elle değiştirmek en geç bu süre sonra etkili).
- Handler'lar ortak hata akışına taşındı: her handler hatasını döndürüyor (`handle`), yoldaki kimlik (`pathID`), 404/403/409
  yanıtları ve 500'ün loglanması tek yerde; istek gövdeleri `bind[T]` ile çözülüp tipin `Validate()` kuralıyla doğrulanıyor.
  Sunucu/organizasyon/eşik/kişi/kural için "yükle + yetki denetle" blokları yardımcılara (`viewableHost`, `managedHost`,
  `managedOrg`…) toplandı. Yanıt mesajları ve durum kodları aynı.
- Kod yorumlarındaki eskimiş atıflar düzeltildi: artık var olmayan migration'lar (`000013`, `000016`) ve `PROGRESS.md`
  kararları yerine gerekçe yorumun kendisine yazıldı; pull secret'ın "düz metin saklanır" ve docker_restart'ın "alert'e
  bağlanmadı" ifadeleri güncel davranışa göre düzeltildi.

## [1.0.0] - 2026-09-22

İlk kararlı sürüm. Şema tek bir baseline migration'dır (`000001_baseline`); tasarımı `docs/VERITABANI.md`'de.

### Eklendi
- **Organizasyon ağacı:** organizasyonlar üst şirket → alt şirketler diye iç içe olur (`parent_organization_id`, `address`).
  Bir yöneticiyi organizasyona atamak o organizasyonu ve altındaki tüm dalı verir; üst zinciri yalnızca adıyla (bağlam) görür,
  kardeş dalları göremez. Ağacı yalnızca süper admin değiştirir; döngü engellenir.
- **İletişim kişileri:** organizasyonun başvurulacak kişileri (departman, unvan, yönetici, telefon/e-posta); panel kullanıcısı olmaları gerekmez.
- **Bildirim kuralları:** alert'in kime, hangi kanaldan ve en az hangi seviyeden gideceği; kapsam organizasyon ya da sunucu,
  alıcı panel kullanıcısı ya da iletişim kişisi. Kapsamda kural varsa yalnızca kurallar, yoksa varsayılan alıcılar (süper adminler +
  ilgili organizasyon yöneticileri, e-posta, `warning` ve üstü). Alert yükselince yalnızca yeni seviyeyle kural eşiği aşılan alıcılar
  ilk kez bilgilendirilir. Kanal olarak şimdilik e-posta uygulanmıştır (şema SMS/Slack/Discord/Telegram'a hazır).
- **Hiyerarşik eşikler:** sunucuya özel → organizasyon → üst şirketler (en yakın) → genel varsayılan; mount ve container başına eşikler.
- **Alert'ler:** tetiklendiği andaki ölçüm ve eşiği, onaylayan kullanıcıyı saklar; `info` seviyesi; "disk kayboldu" ve "sunucu çevrimdışı" olayları.
- **Kullanıcı profili:** ad soyad, telefon, iki faktörlü doğrulama alanları (henüz uygulanmadı, yalnızca saklanır); giriş e-postayla.
- **Sunucu:** panelde görünen ad (`title`, organizasyon içinde benzersiz) ile makinenin bildirdiği hostname ayrı; aynı makine kimliğini
  bildiren sunucular için çift kayıt uyarısı.
- **Roller ve izinler veritabanında:** `roles`, `permissions`, `role_permissions`.
- Push (`X-Host-ID` + token) ve pull (TLS, paylaşılan secret, özel CA) modları; sürüm/protokol uyumluluğu (`docs/COMPATIBILITY.md`).
- E-posta ile şifre sıfırlama, yönetici tarafından açılan hesaplar için zorunlu ilk şifre değişimi, refresh token rotasyonu, hız sınırları, denetim kaydı (IP dahil).
- Metrik saklama süresi (`METRICS_RETENTION_DAYS`), açılışta otomatik migration, TLS sertifikası yeniden yükleme.
- Panel: organizasyon ağacı, iletişim kişileri, bildirim kuralları, eşik mirası, kullanıcı profili, sunucu ekleme sihirbazı,
  özet süzgeçleri, sunucu detay sayfası (genel, sistem, Docker, alert'ler, ayarlar), agent sürüm durumu.
- Docker Compose dağıtımı (`server`, `panel`, `db`, sertifika üretici; geliştirme için Mailpit profili).
