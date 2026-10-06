# Agent değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/) ilkelerini izler; sürümler [SemVer](https://semver.org/)'dir.
Agent'ın **kendi sürüm hattı** vardır: `agent/vX.Y.Z` etiketiyle yayınlanır (server ve panelden bağımsız; bkz.
`docs/DISTRIBUTION.md`). Her sürümün başlığı `## [X.Y.Z] - YYYY-AA-GG` biçimindedir: `scripts/release-notes.sh agent X.Y.Z`
release notlarını buradan çıkarır. Server ve panel: `server/CHANGELOG.md`. Uyumluluk kuralları: `docs/COMPATIBILITY.md`.
Davranışı değiştirmeyen iç düzenlemeler, bölümün sonundaki "İç değişiklikler (davranış değişmedi)" başlığında listelenir
(bkz. `CONTRIBUTING.md` §3).

## [Yayınlanmamış]

### Eklendi
- **Protokol 4: sistem sağlığı ve performans verileri.** Hepsi yetkisiz okunur; okunamayan alan gönderilmez.
  - CPU'da iowait ve steal payı, G/Ç'de takılı süreç sayısı (`cpu_detail`).
  - Bellek ayrıntısı: kullanılabilir bellek, önbellek, swap'a yazma/okuma hızı ve OOM (bellek yetmediği için öldürülen
    süreç) sayacı (`memory_detail`).
  - Kaynak baskısı (PSI): CPU, bellek ve G/Ç'de bekleme yüzdesi (`pressure`; PSI'ı kapalı çekirdeklerde gönderilmez).
  - Yazılım RAID dizilerinin durumu (`raid`: temiz, bozuk, yeniden kuruluyor, eşitleniyor).
  - Disk girdilerinde salt okunur bağlı olup olmadığı (`read_only`).
  - Container'ların Docker healthcheck sonucu ve üst üste başarısız deneme sayısı; durmuş container'ların çıkış kodu ve
    bellek yetmediği için öldürülüp öldürülmediği.
  - systemd servislerinin durumu (`services`): ad, açıklama, durum, ne zamandan beri, yeniden başlatma sayısı, açılışta
    etkin mi. Tam liste açılışta ve 5 dakikada bir; aradaki raporlarda yalnızca sorunlu (çökmüş, başlatılıyor, yeniden
    başlatma döngüsünde) ve son rapordan beri durumu değişen servisler. Kurulu olmayan (`not-found`) ve `masked` servisler
    raporlanmaz.
  - Fiziksel disk başına G/Ç (`disk_io`): okuma/yazma IOPS ve bayt/sn, ortalama gecikme, meşguliyet yüzdesi, kuyruk
    derinliği (`/proc/diskstats`, iostat'ın formülleri).
  - Ağ arayüzü başına trafik (`net_io`): gelen/giden bit/sn, hata ve düşen paket sayısı; `lo`, `docker0`, `veth*`,
    `br-*` (container iç trafiği) hariç. TCP yeniden gönderim oranı, kurulu bağlantı ve TIME_WAIT sayısı (`tcp`).
  - Oranlar iki rapor arasındaki farktır: agent'ın ilk raporunda ve sayaç geri gittiğinde (yeniden açılış) gönderilmez.

### Değişti
- **Yavaş kaynaklar arka planda toplanıyor:** Docker ve envanterin komut gerektiren alanları (saat senkronu, başarısız
  servisler, Docker sürümü) kendi aralıklarıyla arka planda toplanıyor; rapor onları beklemiyor. Önceden toplama ve
  gönderim tek bir 15 sn'lik süreyi paylaşıyordu: çok container'lı makinelerde Docker toplaması süreyi doldurunca raporun
  tamamı (CPU, RAM, disk dahil) kayboluyordu. Gönderimin artık kendi 10 sn'lik süresi var. Pull modunda yanıt da yavaş
  kaynakları beklemeden dönüyor; agent sorgu aralığını gelen isteklerden öğreniyor (bkz. `docs/AGENT.md` §11).
- **Docker istatistikleri hızlandı:** daha önce görülmüş container'ların istatistiği tek örnekle (`one-shot`, Docker API
  1.41+) alınıyor ve CPU yüzdesi agent'ta hesaplanıyor; container başına ~2 sn'lik bekleme yalnızca ilk kez yapılıyor.
  Container inspect bilgisi durumu değişmedikçe önbellekten geliyor. Docker toplama hatası her döngüde değil, yalnızca
  durum değiştiğinde loglanıyor.

### Düzeltildi
- Pull modunda aynı anda gelen iki istek CPU ölçümünün önceki örneğini birlikte değiştirebiliyordu; CPU toplayıcısı artık
  eşzamanlı çağrılara karşı korumalı.
- Proje MIT lisansıyla yayınlanıyor: `.deb`/`.rpm` paketlerinin lisans alanı `MIT` oldu, `LICENSE` dosyası paketlere
  (`/usr/share/doc/healthbeat/LICENSE`) ve tarball'a eklendi.

### İç değişiklikler (davranış değişmedi)
- `install_test.sh`: senaryoların kök dizinleri `mktemp` ile açılıyor; `$RANDOM` adları arada çakışıp testi dengesizleştiriyordu.

## [1.0.0] - 2026-09-22

İlk kararlı sürüm.

### Eklendi
- **Metrik toplama:** CPU, RAM ve mount başına disk (kullanım yüzdesi, toplam/boş, inode doluluğu); isteğe bağlı Docker
  container durumu (CPU, RAM, restart sayısı, çalışma süresi). Karar vermez: eşik ve alert server'dadır.
- **Push ve pull modu:** push'ta agent server'a `X-Host-ID` + Bearer token ile HTTPS üzerinden gönderir; pull'da agent
  yalnızca izin verilen server IP'lerinden, paylaşılan secret ile ve TLS üzerinden sorgulanır.
- **Donanım özeti ve makine envanteri (protokol 3):** CPU çekirdeği, toplam RAM, fiziksel diskler ve mount'ları, işletim sistemi,
  kernel, hostname, IP adresleri, sanallaştırma, uptime, yük, swap, saat senkronu, başarısız servisler, güvenlik modülü, Docker
  sürümü. Yalnızca yetkisiz okunabilen bilgiler; `/etc/machine-id` yalnızca uygulamaya özgü özet olarak gider. `--print-inventory`
  bu envanteri yapılandırmasız JSON olarak yazdırır.
- **Sürüm uyumluluğu:** her istekte agent sürümü ve protokolü bildirilir; server bilinmeyen alanı reddetmez, agent eski bir
  server'da çekirdek alanlara geri döner (bkz. `docs/COMPATIBILITY.md`).
- **Kurulum ve dağıtım:** `.deb` / `.rpm` paketleri (amd64, arm64) ve tarball + `install.sh`; `install`, `upgrade` (otomatik
  geri alma korumalı), `rollback`, `uninstall`, `configure`; systemd unit'i sertleştirilmiş; imzalı (`SHA256SUMS.asc`) release.
- `--check-config`, `--version`.
