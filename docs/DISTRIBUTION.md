# HealthBeat — Dağıtım süreci

Bu belge, bir HealthBeat sürümünün **nasıl çıkarıldığını** ve agent'ın/server'ın **nasıl kurulup
güncellendiğini** tek bir standart olarak anlatır. Dağıtım **bilerek elle** yapılır: kodla toplu
dağıtım ya da agent'ın kendini güncellemesi yoktur (gerekçe: bölüm 9). Uyumluluk kuralları için
`docs/COMPATIBILITY.md`, agent'ın ayrıntılı yapılandırması için `docs/AGENT.md`, server ortamı için
`docs/DEPLOYMENT.md`.

## 1. Bir bakışta

- **İki bağımsız sürüm hattı** (ayrıntı bölüm 11): **agent** `agent/vX.Y.Z` etiketiyle (binary, `.deb`/`.rpm`, tarball) ve
  **server + panel** `server/vX.Y.Z` etiketiyle (server, panel, certs-init Docker imajları, tek sürüm numarası) yayınlanır. Biri
  çıkınca diğerinin numarası değişmez; panelde bir düzeltme yapmak agent'ı ("güncelleme var" diye) etkilemez. Her hattın
  kendi değişiklik günlüğü vardır: `agent/CHANGELOG.md`, `server/CHANGELOG.md`.
- **Tek kaynak:** agent tek bir **statik binary**'dir (CGO yok, dağıtıma özgü kütüphane bağımlılığı yok).
  Aynı binary tüm Linux dağıtımlarında çalışır; dağıtımlar arasındaki fark yalnızca **paket biçimi**dir.
- **İki kanal:** `.deb` / `.rpm` **paketi** (önerilen) ve **tarball + `install.sh`** (paket sistemi olmayan
  dağıtımlar için). Aynı sunucuda ikisini karıştırma (bölüm 6).
- **Her sürümde** GitHub Release'te şunlar bulunur: paketler, tarball'lar, ham binary'ler, `SHA256SUMS`,
  `SHA256SUMS.asc` (GPG imzası) ve sürüm notları.
- **Kurulum iki adımdır:** paketi kur → `sudo healthbeat-agent-setup` ile yapılandır. Paket yapılandırma
  yazmaz (sunucuya özel: host id ve token panelden gelir).
- **Sıra:** önce server, sonra agent'lar; önce bir **kanarya** sunucu, sonra kalanlar (bölüm 5).

## 2. Destek matrisi

Mimari: `amd64` ve `arm64`.

| Dağıtım | Sürümler | Paket | Test edildi |
| --- | --- | --- | --- |
| Ubuntu | 22.04, 24.04 | `.deb` | ✅ paket testleri (Docker imajı) |
| Ubuntu | 20.04 | `.deb` | ⚠️ aynı paket; ayrıca denenmedi |
| Debian | 12 | `.deb` | ✅ paket testleri |
| Debian | 11 | `.deb` | ⚠️ aynı paket; ayrıca denenmedi |
| RHEL / Rocky / AlmaLinux | 9 | `.rpm` | ✅ Rocky 9 |
| RHEL / Rocky / AlmaLinux | 8 | `.rpm` | ✅ AlmaLinux 8 |
| CentOS Stream | 9 | `.rpm` | ⚠️ Rocky 9 ile aynı paket; ayrıca denenmedi |
| Amazon Linux | 2023 | `.rpm` | ✅ paket testleri |
| SUSE / openSUSE, Fedora | | `.rpm` / tarball | ⚠️ denenmedi |
| Diğer systemd'li Linux | | tarball | ⚠️ denenmedi |
| **CentOS 7 / RHEL 7** | | — | ❌ **desteklenmez** (ömrü doldu; systemd 219, unit'teki sertleştirme direktiflerini tanımaz) |
| Alpine (OpenRC), macOS, Windows | | — | ❌ desteklenmez |

"Test edildi" yalnızca **paketin kurulup yapılandırıldığını, yükseltildiğini ve kaldırıldığını** kapsar
(konteynerde; bölüm 10'daki sınıra bak). Servisin gerçekten systemd altında kalkması gerçek bir makinede
denendi (Ubuntu 24.04); RHEL ailesinde ve SELinux `enforcing` altında **denenmedi** — RHEL ailesinde ilk
gerçek kurulumu kanarya olarak yap.

## 3. Artifact'lar ve doğrulama

Bir **agent** sürümünde (`agent/vX.Y.Z`) şunlar yayınlanır:

| Dosya | Ne için |
| --- | --- |
| `healthbeat-agent_X.Y.Z_amd64.deb`, `_arm64.deb` | Ubuntu / Debian |
| `healthbeat-agent-X.Y.Z-1.x86_64.rpm`, `.aarch64.rpm` | RHEL / Rocky / Alma / Amazon Linux |
| `healthbeat-agent_X.Y.Z_linux_amd64.tar.gz`, `_arm64.tar.gz` | tarball: binary + `install.sh` + unit + belgeler |
| `healthbeat-agent_X.Y.Z_linux_amd64`, `_arm64` | ham binary |
| `SHA256SUMS`, `SHA256SUMS.asc` | özet listesi ve GPG imzası |
| `RELEASE_NOTES.md` | `agent/CHANGELOG.md`'den çıkarılan sürüm notları |

Çıktı **yeniden üretilebilirdir** (aynı commit'ten iki kez üretince `SHA256SUMS` birebir aynı çıkar).

**Kurmadan önce doğrula** (hedef sunucuda ya da yönetici makinesinde):

```sh
V=X.Y.Z
BASE=https://github.com/<sahip>/<repo>/releases/download/agent/v$V
curl -fLO $BASE/SHA256SUMS -O $BASE/SHA256SUMS.asc -O $BASE/healthbeat-agent_${V}_amd64.deb

gpg --import release-key.pub                 # yayıncının açık anahtarı (repoda docs/release-key.pub; bir kez); parmak izini yayıncıdan ayrıca doğrula
gpg --verify SHA256SUMS.asc SHA256SUMS       # "Good signature" görmelisin
sha256sum -c --ignore-missing SHA256SUMS     # indirdiğin dosya için "OK" görmelisin
```

İmza ya da özet tutmazsa **kurma**; dosyayı yeniden indir, sürmezse yayıncıya bildir. (Repo özelse `curl`
yerine `gh release download agent/vX.Y.Z` kullan.)

## 4. İlk kurulum (yeni sunucu)

Önce panelde sunucuyu ekle (**Organizasyonlar → organizasyon → Sunucu ekle**). Panel bir **host id** ve bir
**API token** gösterir; **token yalnızca bir kez** gösterilir, kopyala.

### 4.1 Ubuntu / Debian

```sh
sudo apt install ./healthbeat-agent_X.Y.Z_amd64.deb     # arm64 sunucuda _arm64.deb
sudo healthbeat-agent-setup                              # adım adım sorar (mod, adres, host id, token, diskler, Docker)
healthbeat-agent --version                               # X.Y.Z
sudo systemctl status healthbeat-agent                   # active (running)
```

### 4.2 RHEL / Rocky / AlmaLinux / Amazon Linux

```sh
sudo dnf install ./healthbeat-agent-X.Y.Z-1.x86_64.rpm  # arm64 sunucuda .aarch64.rpm
sudo healthbeat-agent-setup
healthbeat-agent --version
sudo systemctl status healthbeat-agent
```

RHEL ailesinde **SELinux** `enforcing` ise ve servis başlamazsa `sudo ausearch -m avc -ts recent` ile
reddedilen erişimi bak; bu, henüz doğrulanmamış bir alandır (bölüm 10) — bulduğunu bildir.

### 4.3 Tarball (paket sistemi olmayan dağıtım)

```sh
tar xzf healthbeat-agent_X.Y.Z_linux_amd64.tar.gz && cd healthbeat-agent_X.Y.Z_linux_amd64
sudo ./install.sh wizard                                  # ya da: sudo ./install.sh install --mode push ...
```

### 4.4 Etkileşimsiz kurulum (betik/otomasyon yazan kişi için)

`healthbeat-agent-setup` `install.sh` ile aynı bayrakları alır; token hiçbir zaman komut satırında verilmez:

```sh
export HEALTHBEAT_API_TOKEN="…"        # ya da --token-file /yol/token
sudo -E healthbeat-agent-setup --mode push --server-url https://hb.example.com --host-id <uuid> \
     --interval 30 --disk-mounts auto --ca-cert /yol/ca.pem
```

`--force` olmadan var olan yapılandırmanın üzerine yazılmaz. Tüm seçenekler: `healthbeat-agent-setup --help`.

## 5. Güncelleme (yeni sürüm dağıtımı)

**Zorunlu değildir:** eski agent'lar yeni server'a çalışmaya devam eder (`docs/COMPATIBILITY.md`). Panelde
**Özet → "Agent güncellenmeli"** ve sunucu sayfasındaki **Agent** kutusu hangi sunucuların eski olduğunu gösterir.

### 5.1 Sıra

1. **Server** (bölüm 8). 2. **Kanarya:** tek bir sunucu. 3. Panelde kanaryayı doğrula: Agent kutusu yeni sürüm +
"güncel", CPU/RAM/disk verisi akıyor (birkaç dakika bekle). 4. Kalan sunucular, **tek tek** (ya da kendi
belirlediğin gruplarla); her sunucudan sonra sayaç düşmeli.

### 5.2 Komutlar (yeni artifact'ı indirip doğruladıktan sonra)

| Kurulum türü | Yükseltme | Sürümü doğrula |
| --- | --- | --- |
| Ubuntu / Debian paketi | `sudo apt install ./healthbeat-agent_X.Y.Z_amd64.deb` | `healthbeat-agent --version` |
| RHEL ailesi paketi | `sudo dnf install ./healthbeat-agent-X.Y.Z-1.x86_64.rpm` (kuruluysa yükseltir) | `healthbeat-agent --version` |
| Tarball | `sudo ./install.sh upgrade --dry-run` → `sudo ./install.sh upgrade` | çıktı `upgraded … -> X.Y.Z` ile biter |

- **Paket yükseltmesi** yapılandırmaya, kullanıcıya ve `docker.conf` drop-in'ine dokunmaz. Servis etkin ve
  yapılandırılmışsa paket yeniden başlatır. `apt`/`dnf` standart davranışıdır: **yeni sürüm çökerse otomatik
  geri alma yoktur** — Geri alma (bölüm 5.3) ile dön.
- **Tarball yükseltmesi** (`install.sh upgrade`) ise yeni binary'yi ve mevcut yapılandırmayı önce doğrular,
  çökme durumunda otomatik geri alır (`docs/AGENT.md` bölüm 7.1).
- Yükseltmeyi **gerçek bir terminalde** çalıştır: `sudo` parola soruyorsa terminalsiz (ör. `ssh host 'sudo …'`)
  çalışmaz.

### 5.3 Geri alma

| Kurulum türü | Komut |
| --- | --- |
| Ubuntu / Debian | `sudo apt install --allow-downgrades ./healthbeat-agent_<eski>_amd64.deb` |
| RHEL ailesi | `sudo dnf downgrade ./healthbeat-agent-<eski>-1.x86_64.rpm` |
| Tarball | `sudo ./install.sh rollback` |

Eski sürümün paketini elinde tut (GitHub Release'te kalır). Geri alma yapılandırmaya dokunmaz.

## 6. Tarball kurulumundan pakete geçiş

Aynı sunucuda **tek kanal** kullan. Bir makine tarball ile kuruluysa (`/usr/local/bin/healthbeat-agent`,
`/etc/systemd/system/healthbeat-agent.service`) pakete geçerken:

```sh
sudo apt install ./healthbeat-agent_X.Y.Z_amd64.deb      # paket uyarı verir: eski kurulum hâlâ duruyor
sudo systemctl stop healthbeat-agent
sudo rm -f /usr/local/bin/healthbeat-agent /usr/local/bin/healthbeat-agent.prev /etc/systemd/system/healthbeat-agent.service
sudo systemctl daemon-reload && sudo systemctl start healthbeat-agent
```

`/etc/healthbeat/agent.json` korunur (aynı dosya). Eski unit'i silmezsen `/etc/systemd/system`'deki kopya
paketin unit'ini **gölgeler** ve eski binary çalışmaya devam eder. Paketle kurulu bir makinede `install.sh`'ın
`install/upgrade/rollback/uninstall` eylemleri bilerek reddedilir; yalnızca `configure` çalışır.

## 7. Kaldırma

| | Komut | Yapılandırma |
| --- | --- | --- |
| Ubuntu / Debian | `sudo apt remove healthbeat-agent` | kalır |
| Ubuntu / Debian (tam) | `sudo apt purge healthbeat-agent` | **silinir** (config, sertifikalar, kullanıcı) |
| RHEL ailesi | `sudo dnf remove healthbeat-agent` | **kalır** (kimlik bilgisi içerir; rpm'de "purge" yoktur, elle sil: `sudo rm -rf /etc/healthbeat`) |
| Tarball | `sudo ./install.sh uninstall [--purge]` | `--purge` ile silinir |

Sunucuyu panelden de silmeyi unutma (Sunucu sayfası → Ayarlar); aksi halde çevrimdışı alert'i üretir.

## 8. Server dağıtımı

Server, panel ve sertifika-init **Docker imajları** olarak, `server/vX.Y.Z` etiketiyle ve **tek bir sürüm numarasıyla**
yayınlanır (amd64 + arm64); GitHub Releases'te "HealthBeat Server X.Y.Z" olarak sürüm notlarıyla görünür. Agent'ın
sürümünden bağımsızdır (bölüm 11):

```
ghcr.io/<sahip>/healthbeat-server:X.Y.Z      ghcr.io/<sahip>/healthbeat-panel:X.Y.Z      ghcr.io/<sahip>/healthbeat-certs-init:X.Y.Z
```

(`<sahip>`: GitHub kullanıcı/organizasyon adı, küçük harf. Docker Hub da kullanılıyorsa aynı adlar `docker.io/<namespace>/…`.)
`docker-compose.yml` imaj adlarını `HB_REGISTRY` ve `HB_VERSION` ile alır; ikisi boşsa yerelde derlenen
imajlar (`healthbeat/…:local`) kullanılır.

### 8.1 İlk kurulum (yayınlanmış sürümle)

Depo/paketler **özelse** (GHCR paketleri de varsayılan olarak özeldir) sunucuda bir kez oturum aç: GitHub'da
`read:packages` yetkili bir token (Settings → Developer settings → Personal access tokens) oluştur ve
`echo "$TOKEN" | docker login ghcr.io -u <github-kullanıcı> --password-stdin` çalıştır. Paketleri herkese açarsan gerekmez.

```sh
./deploy/init-env.sh                                   # .env üretir (rastgele sırlar); bir kez
# .env içine ekle:  HB_REGISTRY=ghcr.io/<sahip>   HB_VERSION=X.Y.Z
docker compose pull && docker compose up -d
```

### 8.2 Güncelleme

```sh
# 1) VERİTABANI YEDEĞİ (migration'lar ileri doğrudur; geri dönüş yedekten yapılır)
docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists' > backup-$(date +%F)-oncesi.sql
# 2) yeni sürüm: .env içindeki HB_VERSION satırını X.Y.Z yap, sonra
docker compose pull && docker compose up -d
# 3) doğrula
docker compose ps                                       # hepsi healthy
docker compose logs server | grep -E "migrat|listening"  # "now at 0000NN" ve "listening"
```

Server açılışta bekleyen migration'ları uygular (`AUTO_MIGRATE`). Panel giriş yapılınca eski sekmeyi yenile.

### 8.3 Geri alma

Yalnızca kod: `HB_VERSION`'ı eski sürüme çevirip `docker compose up -d` — migration'lar **eklemeli**dir (yalnızca
nullable sütun), eski server yeni sütunları görmez ama çalışır (`docs/COMPATIBILITY.md` kural 6). Veri sorunu
varsa yedeği geri yükle:
`docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < backup-….sql`.

## 9. Bilerek yapmadıklarımız

- **Toplu/otomatik dağıtım yok** (ssh döngüsü, Ansible rolü vb.): dağıtım elle ve kontrollü yapılır.
- **Agent kendini güncellemez.** Bu, imzalı bir dağıtım kanalı ve imza doğrulaması ister; aksi halde sunucularda
  ayrıcalıklı çalışan agent'lar için tedarik zinciri saldırısına kapı olur.
- **APT/YUM deposu yok.** Paketler GitHub Release'ten indirilip yerel dosyadan kurulur. Filo büyürse
  (`apt upgrade` ile güncelleme istenirse) bir depo eklenebilir; paket ve imza altyapısı hazırdır.

## 10. Doğrulama sınırı (dürüst kayıt)

`agent/packaging/test_packages.sh` her paketi Docker'da **6 dağıtım imajında** (Ubuntu 22.04/24.04, Debian 12,
Rocky 9, AlmaLinux 8, Amazon Linux 2023) kurar ve şunları doğrular: dosya yerleşimi ve izinler, sistem kullanıcısı,
unit'in tarball unit'iyle yalnızca `ExecStart` yolunda farklı olması, `healthbeat-agent-setup` ile yapılandırma
(izinler, agent'ın config'i kabul etmesi, token'ın yazdırılmaması), yükseltmede config'in korunması, kaldırma, yeniden
kurulum, `purge`, eski tarball kurulumu için uyarı. Şunlar **doğrulanmadı**:

- **Gerçek systemd:** konteynerde systemd çalışmaz; servisin enable/start/restart'ı yalnızca Ubuntu 24.04 gerçek makinede
  (tarball yoluyla) denendi. Paketin `postinst` restart'ı ve RHEL ailesindeki servis davranışı gerçek VM ister.
- **SELinux (`enforcing`):** RHEL ailesinde Docker socket / `/proc` erişimi engellenebilir; bu makinede test edilemez.
- **arm64 çalışma zamanı:** arm64 paketler derlenir ve üst verisi doğrulanır, bu makinede çalıştırılamaz.
- **İki hatlı iş akışı (`agent/v*`, `server/v*`):** `actionlint` temiz, `scripts/release_test.sh` (28 kontrol) ve betiklerin mutasyon testi geçti; **gerçek etiketlerle GitHub'da çalıştırıldı** (`agent/v1.0.0`: imzalı release, imza ve özetler bağımsız anahtar halkasında doğrulandı; `server/v1.0.0`: üç imaj GHCR'a gitti, release oluştu). Not: aynı adlı GHCR paketleri başka bir repoya bağlıysa `denied: permission_denied: read_package` ile itme reddedilir; eski paketi silmek ya da yeni repoya *Manage Actions access* ile yazma izni vermek gerekir.
- **GHCR ve paket kurulumu:** bir sunucuda **GHCR'dan `docker compose pull`** ile imaj çekme, release'ten indirilen bir **paketin gerçek
  bir makinede kurulumu** ve Docker Hub yolu (kullanılmıyor) henüz doğrulanmadı.
- Ubuntu 20.04, Debian 11, CentOS Stream 9 ve diğer türevler ayrıca denenmedi (aynı paket/statik binary).

## 11. Sürüm çıkarma (yayıncı için)

### 11.1 Bir kez: GitHub ve imza kurulumu

```sh
# 1) İmza anahtarı (ed25519). Anahtarın parolasını güvenli yerde sakla.
gpg --quick-generate-key "HealthBeat Release <adres@example.com>" ed25519 sign 2y
gpg --list-secret-keys --keyid-format long            # KEYID'yi not et
# 2) Açık anahtarı repoya koy (kurulum yapanlar doğrulamak için içe aktarır) ve parmak izini ayrıca duyur
gpg --armor --export KEYID > docs/release-key.pub
# 3) Özel anahtarı GitHub secret'ına koy (Settings > Secrets and variables > Actions):
gpg --armor --export-secret-keys KEYID                # çıktıyı GPG_PRIVATE_KEY secret'ına yapıştır
#    GPG_PASSPHRASE secret'ı: anahtarın PAROLASI. Anahtar parolalıysa zorunlu (eksikse imzalama "No passphrase given" ile düşer);
#    parolasız anahtarda ekleme. Parolalı anahtar önerilir (yerel anahtar halkasını korur).
```

İmaj yayını GHCR'a `GITHUB_TOKEN` ile gider (ek ayar yok). Docker Hub da istenirse:
`vars.DOCKERHUB_NAMESPACE` (değişken) ve `secrets.DOCKERHUB_USERNAME` / `secrets.DOCKERHUB_TOKEN`.
GHCR paketlerini herkese açmak istiyorsan ilk yayından sonra paket ayarlarından görünürlüğü değiştir.

### 11.2 İki sürüm hattı

| | **Agent** | **Server + panel** |
| --- | --- | --- |
| Etiket | `agent/vX.Y.Z` | `server/vX.Y.Z` |
| Sürüm kaynağı | `agent/internal/version/version.go` (`Version`) | `server/internal/version/version.go` (`Version`) **ve** `server/panel/package.json` + `server/panel/package-lock.json` (aynı numara; `cd server/panel && npm version X.Y.Z --no-git-tag-version`) |
| Ayrıca | `server/internal/version/version.go` içindeki `LatestAgent` **aynı commit'te** X.Y.Z yapılır | `LatestAgent` yayınlanmış gerçek bir agent sürümü olmalı (`agent/v…` etiketi var mı diye bakılır) |
| Değişiklik günlüğü | `agent/CHANGELOG.md` → `## [X.Y.Z] - YYYY-AA-GG` | `server/CHANGELOG.md` → `## [X.Y.Z] - YYYY-AA-GG` (panel maddeleri "Panel:" ile başlar) |
| Yayınlananlar | binary, tarball, `.deb`/`.rpm`, `SHA256SUMS` + GPG imzası → GitHub Release | `healthbeat-server`, `-panel`, `-certs-init` imajları (`X.Y.Z`, `X.Y`, `latest`) → GHCR → GitHub Release (notlar + imaj adları) |
| Yerel deneme | `scripts/release.sh agent X.Y.Z --allow-dirty` (imzasız; `dist/agent-vX.Y.Z/`) | `scripts/release.sh server X.Y.Z --check` (yalnızca sürüm tutarlılığı) ya da tam: testler + panel build + notlar (`dist/server-vX.Y.Z/`) |
| CI | yalnızca agent işleri | server + panel işleri |

**Hangi hat ne zaman?** Yalnızca panel ya da server değiştiyse **yalnızca server hattı** çıkar (agent'lar dokunulmaz, panelde
"güncelleme var" görünmez). Agent'ı değiştiren bir iş (yeni alan, düzeltme) **agent hattını** çıkarır; ingest'e yeni alan
eklendiyse protokol/`COMPATIBILITY.md` kuralları geçerlidir ve genellikle bir server sürümü de gerekir (server yeni alanı işlesin diye).
Server önce, agent sonra dağıtılır (bölüm 5). İki hat aynı gün çıkarsa sıra: **agent etiketi önce** (server'ın `LatestAgent`'ı gerçek bir etikete dayanmalı).

**Agent sürümü için kontrol listesi:**

- [ ] `docs/COMPATIBILITY.md` §6 (yeni ingest alanı kontrol listesi) uygulandı; `scripts/compat_e2e.sh` yeşil.
- [ ] `agent/internal/version/version.go` `Version` artırıldı (yeni bir alan kümesi eklendiyse `Protocol` da) **ve** `server/internal/version/version.go` `LatestAgent` aynı numara yapıldı.
- [ ] `agent/CHANGELOG.md`'de `## [X.Y.Z] - YYYY-AA-GG` bölümü yazıldı (release notları buradan çıkar).
- [ ] Testler yeşil: `go test` (agent), `agent/deploy/install_test.sh`, `agent/packaging/test_packages.sh`.
- [ ] Yerel deneme: `scripts/release.sh agent X.Y.Z --allow-dirty`.
- [ ] Commit'lendi, `main`'a alındı.
- [ ] `git tag agent/vX.Y.Z && git push origin agent/vX.Y.Z` → Actions: CI (agent) → artifact + imza + GitHub Release.
- [ ] Release sayfasında dosyaları ve `SHA256SUMS.asc` imzasını (`gpg --verify`) kontrol et.
- [ ] Kanarya sunucuda bölüm 5'e göre dene; sonra kalanlara.

**Server + panel sürümü için kontrol listesi:**

- [ ] `server/internal/version/version.go` `Version`, `server/panel/package.json` ve `server/panel/package-lock.json` sürümü artırıldı (üçü de aynı). Yeni bir ingest alanı/protokol eklendiyse `Protocol`.
- [ ] `server/CHANGELOG.md`'de `## [X.Y.Z] - YYYY-AA-GG` bölümü yazıldı.
- [ ] Testler yeşil: `go test` (server `-race`, `TEST_DATABASE_URL` ile), panel `npm test`; `scripts/compat_e2e.sh` (ingest'e dokunulduysa).
- [ ] `scripts/release.sh server X.Y.Z --check` temiz. Migration eklendiyse ileri yönlü/eklemeli olduğu doğrulandı (`COMPATIBILITY.md` kural 6).
- [ ] Commit'lendi, `main`'a alındı.
- [ ] `git tag server/vX.Y.Z && git push origin server/vX.Y.Z` → Actions: CI (server + panel) → sürüm tutarlılığı → imajlar GHCR'a → GitHub Release.
- [ ] Release sayfasında notları, GHCR'da `X.Y.Z`/`X.Y`/`latest` etiketlerini kontrol et; server dağıtımı bölüm 8'e göre (yedek al, `HB_VERSION=X.Y.Z`).
- [ ] Panelde kenar çubuğu altındaki **Panel X.Y.Z · Server X.Y.Z** satırı iki sürümün eşit olduğunu göstermeli (farklıysa uyarı çıkar).

Önsürüm etiketi (`agent/vX.Y.Z-rc.1`, `server/vX.Y.Z-rc.1`) release'i "prerelease" işaretler ve `latest` imaj etiketini almaz.

## 12. Sorun giderme

| Belirti | Çözüm |
| --- | --- |
| `apt install ./x.deb`: "Download is performed unsandboxed as root" uyarısı | Zararsız: yerel dosya `_apt` kullanıcısı tarafından okunamıyor. Dosyayı `/tmp`'ye kopyala ya da yoksay. |
| Kurulum sonrası "NOT configured yet" | Beklenen: `sudo healthbeat-agent-setup` çalıştır. |
| `healthbeat-agent-setup`: "no agent binary is installed" | Paket kurulu değil (ya da tarball kurulumu); önce paketi kur. |
| `install.sh`: "was installed from a package" | Paketle kurulu makinede `install/upgrade` reddedilir; `apt`/`dnf` kullan (bölüm 5). |
| Paket yükseltmesinden sonra servis eski sürümü çalıştırıyor | Eski tarball kurulumu paketi gölgeliyor: bölüm 6. |
| `dnf`: `groupadd: command not found` | Minimal imajda `shadow-utils` yok; paket bunu bağımlılık olarak ister, `dnf install` kendisi kurar (`rpm -i` kurmaz). |
| Servis başlamıyor (RHEL) | `journalctl -u healthbeat-agent -n 50`; SELinux ise `sudo ausearch -m avc -ts recent`. |
| Panelde "eski agent" görünüyor | Sürüm bildirmeyen (1.1.0 öncesi) agent; bölüm 5 ile güncelle. |
