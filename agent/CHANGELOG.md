# Agent değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/) ilkelerini izler; sürümler [SemVer](https://semver.org/)'dir.
Agent'ın **kendi sürüm hattı** vardır: `agent/vX.Y.Z` etiketiyle yayınlanır (server ve panelden bağımsız; bkz.
`docs/DISTRIBUTION.md`). Her sürümün başlığı `## [X.Y.Z] - YYYY-AA-GG` biçimindedir: `scripts/release-notes.sh agent X.Y.Z`
release notlarını buradan çıkarır. Server ve panel: `server/CHANGELOG.md`. Uyumluluk kuralları: `docs/COMPATIBILITY.md`.
Davranışı değiştirmeyen iç düzenlemeler, bölümün sonundaki "İç değişiklikler (davranış değişmedi)" başlığında listelenir
(bkz. `CONTRIBUTING.md` §3).

## [Yayınlanmamış]

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
