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
- **`TRUSTED_PROXIES`:** server'ın `X-Forwarded-For` başlığına güvendiği reverse proxy'ler (virgülle ayrılmış IP, CIDR ya da
  docker'daki `panel` gibi bir host adı; host adları 30 sn'de bir, çözülemedikleri sürece 2 sn'de bir ve tanınmayan bir
  eşten `X-Forwarded-For`'lu istek gelince hemen yeniden çözülür). İstemci IP'si yalnızca istek bunlardan
  birinden geldiğinde başlıktan okunur; boşsa (bare-metal varsayılanı) her zaman TCP eşidir. Docker Compose varsayılanı
  `panel`. `0.0.0.0/0` gibi herkese güvenen aralıklar reddedilir. Ayrıntı: `docs/DEPLOYMENT.md`, "Hız sınırları ve istemci IP'si".
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
- **Panic kurtarma:** bir istekteki beklenmeyen hata artık bağlantıyı düşürmek yerine `500` ve `request_id`'li JSON yanıt
  döndürüyor, yığın izi loga yazılıyor. Arka plan işleri (pull scheduler, offline izleyici, retention, token temizliği,
  istemci IP çözücüsü) panic'lerse loglanıp 5 sn sonra yeniden başlatılıyor; alert e-postası ya da tek bir host'un poll'u
  panic'lerse yalnızca o iş düşüyor.

### Değişti
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
