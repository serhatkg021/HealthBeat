# Server ve panel değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/) ilkelerini izler; sürümler [SemVer](https://semver.org/)'dir.
Server ve panel **birlikte** yayınlanır ve tek sürüm numarası taşır: `server/vX.Y.Z` etiketiyle (server, panel ve
certs-init imajları aynı sürümle GHCR'a gider; bkz. `docs/DISTRIBUTION.md`). Agent'tan bağımsızdır: agent'ın kendi
günlüğü `agent/CHANGELOG.md`. Her sürümün başlığı `## [X.Y.Z] - YYYY-AA-GG` biçimindedir:
`scripts/release-notes.sh server X.Y.Z` release notlarını buradan çıkarır. Panel değişiklikleri "Panel:" ile başlar.
Davranışı değiştirmeyen iç düzenlemeler, bölümün sonundaki "İç değişiklikler (davranış değişmedi)" başlığında listelenir
(bkz. `CONTRIBUTING.md` §3).

## [Yayınlanmamış]

### Eklendi
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
