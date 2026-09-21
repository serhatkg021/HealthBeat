# HealthBeat — Agent kurulumu

Agent, izlenen Linux sunucusuna kurulan küçük bir Go binary'sidir: CPU, RAM, disk ve
(isteğe bağlı) Docker container durumlarını toplar. **Karar vermez** — eşik kontrolü ve alert
üretimi server'dadır. İki çalışma modu vardır (agent bazında, **oluşturulurken** seçilir ve
sonradan değiştirilemez — bkz. bölüm 6.e):

| Mod | Trafik | Ne zaman |
| --- | --- | --- |
| **push** | Agent → server (giden HTTPS) | Agent'ın yalnızca *çıkış* izni varsa (NAT/güvenlik duvarı arkası) — **varsayılan öneri** |
| **pull** | Server → agent (gelen HTTPS) | Server'ın agent'a erişebildiği ağlarda; agent'ta bir port açılır |

> **Önerilen kurulum yolu paket (`.deb` / `.rpm`)'tir:** `apt install ./healthbeat-agent_X.Y.Z_amd64.deb`, sonra
> `sudo healthbeat-agent-setup` (bu belgedeki `install.sh` sihirbazının aynısı). Sürüm indirme/doğrulama, işletim
> sistemine göre kurulum, güncelleme ve geri alma: **[docs/DISTRIBUTION.md](DISTRIBUTION.md)**. Bu belgenin `install.sh`
> bölümleri **tarball yolunu** ve yapılandırma ayrıntılarını anlatır; paketle kurulu bir makinede
> `install.sh install/upgrade/rollback/uninstall` bilerek reddedilir (yalnızca `configure` çalışır).

Yalnızca **Linux** desteklenir (metrikler `/proc`, `/sys` ve `statfs` üzerinden okunur).

Yüzdelerin yanında agent makinenin **donanım özetini** de bildirir; panelde sunucu sayfasında görünür:
mantıksal **CPU çekirdek sayısı**, **toplam RAM** ve **fiziksel diskler** (ad, model, boyut, tür:
NVMe/SSD/HDD ve üzerindeki mount'lar). Bölümler üst diske, LVM/mdraid aygıtları altındaki fiziksel
disklere çözülür — bir mount birden çok diske düşebilir. NFS/tmpfs gibi diski olmayan mount'lar hiçbir
diskin altında listelenmez. Sınırlar: btrfs'in çok aygıtlı birimlerinde yalnızca mountinfo'da görünen
aygıt raporlanır; sanal makinelerde disk türü (SSD/HDD) sanal aygıtın bildirdiğine bağlıdır; konteyner
içinde çalışan agent diskleri keşfedemeyebilir (bu durumda panel önceki bilinen değeri gösterir).

## İçindekiler

1. Hızlı başlangıç (push, önerilen yol)
2. Adım adım kurulum
   - 2.1 Binary'yi derle
   - 2.2 Panelde agent'ı oluştur
   - 2.3 Kur (root gerekir)
   - 2.4 Kurulumdan sonra
3. Docker izleme (isteğe bağlı) — güvenlik uyarısı
4. Hangi diskler alert üretir?
5. Yapılandırma referansı (`/etc/healthbeat/agent.json`)
6. Sık yapılan hatalar ve düzeltmeler (a-g)
7. Yükseltme, geri alma ve kaldırma (`install.sh upgrade`, kanarya)
8. Servisin sertleştirmesi
9. Sorun giderme
10. Envanter (makine bilgisi)

## 1. Hızlı başlangıç (push, önerilen yol)

Çoğu kurulum budur: agent, server'a giden bağlantıyla veri gönderir, ekstra port/güvenlik
duvarı kuralı gerekmez. İlk üç adım **geliştirme makinende**, son ikisi **hedef sunucuda**
çalışır — ayrı makineler, ayrı kabuklar (aşağıdaki iki blok tek bir script değildir, elle
birbirine bağlanır):

**Geliştirme makinende:**

```sh
# 1) Binary'yi derle
cd agent && go build -o healthbeat-agent ./cmd/agent

# 2) Panelde sunucuyu oluştur: Organizasyonlar → (organizasyon) → Sunucu ekle → mod: push.
#    Panel bir AGENT ID ve bir API TOKEN gösterir — token yalnızca bir kez gösterilir, kopyala.

# 3) Binary'yi, install.sh'ı ve servis dosyasını hedef sunucuya kopyala
scp healthbeat-agent deploy/install.sh deploy/healthbeat-agent.service kullanici@sunucu:/tmp/
```

**Hedef sunucuda (root ile):**

```sh
cd /tmp

# 4) Kur — sihirbaz modu, her soruyu kısaca açıklayarak tek tek sorar (mod, adres,
#    token, Docker izleme, hangi diskler raporlanacak, ...) ve kurmadan önce bir
#    özet gösterip onay ister. Terminal gerektirir.
sudo ./install.sh wizard

# 5) Doğrula
sudo systemctl status healthbeat-agent
sudo journalctl -u healthbeat-agent -f
```

Panelde sunucu birkaç saniye içinde **online** görünmelidir. Bir şeyi yanlış girdiysen bölüm 6
("Sık yapılan hatalar ve düzeltmeler") sıfırdan kurmadan nasıl düzelteceğini anlatır.

**Script/otomasyon içinden çalıştıracaksan** (Ansible, CI, ...) sihirbaz uygun değil — tüm
değerleri tek komutla, sorusuz ver:

```sh
sudo HEALTHBEAT_API_TOKEN='<panelin gösterdiği token>' ./install.sh install --mode push \
     --server-url https://api.example.com --host-id <panelin gösterdiği uuid>
```

Docker container'larını da izlemek istiyorsan bu komuta `--docker` ekle (kararı burada ver —
sonradan eklemek de mümkün, bkz. bölüm 6.a); `wizard`'a da aynı şekilde ek bayraklar verilebilir,
verilen her bayrak için sihirbaz o soruyu atlar (`./install.sh --help`).

## 2. Adım adım kurulum

### 2.1 Binary'yi derle

```sh
cd agent
go build -o healthbeat-agent ./cmd/agent
# başka bir mimari için: GOOS=linux GOARCH=arm64 go build -o healthbeat-agent ./cmd/agent
```

### 2.2 Panelde agent'ı oluştur

Panelde **Organizasyonlar → (organizasyon) → Sunucu ekle**. Modu seç:

- **push**: panel bir **host id** ve **API token** gösterir. Token **yalnızca bir kez** gösterilir.
- **pull**: sunucunun IP'sini, dinleme portunu ve endpoint'i gir; panel bir **pull secret** gösterir
  (bir kez).

Token/secret'ı kaybedersen kurulumu baştan yapmana gerek yok — bkz. bölüm 6.c.

### 2.3 Kur (root gerekir)

`agent/deploy/` altındaki `install.sh`, `healthbeat-agent.service` ve derlediğin binary'yi
sunucuya kopyala.

**En kolayı `sudo ./install.sh wizard`** — soru sorarak seni yönlendirir, her alanı neden
istediğini kısaca açıklar (bölüm 1). Aşağıdakiler doğrudan `install` komutunu, tüm değerleri
bayrak/ortam değişkeniyle vererek (script/otomasyon için) kullanır; `wizard`'a da aynı bayraklar
verilebilir, verilen her alan için o soru atlanır.

Komutu çalıştırmadan önce iki şeyi bil:

- **Secret'lar komut satırı argümanı olarak verilmez** (`ps` çıktısında ve shell geçmişinde
  görünürdü): aşağıdaki örnekler ortam değişkenini kullanıyor; `--token-file/--secret-file` ya da
  etkileşimli soru da olur.
- Script yalnızca katı bir karakter kümesindeki değerleri kabul eder (URL, UUID, token,
  IP/CIDR, mount yolu); JSON'a enjeksiyon mümkün değildir. Push için `https://` zorunludur.

**Push:**

```sh
# Docker container'larını da izlemek istiyorsan komuta --docker ekle (bölüm 3).
sudo HEALTHBEAT_API_TOKEN='<panelin gösterdiği token>' ./install.sh install --mode push \
     --server-url https://api.example.com --host-id <uuid> --interval 30
```

**Pull:**

```sh
sudo HEALTHBEAT_PULL_SECRET='<panelin gösterdiği secret>' ./install.sh install --mode pull \
     --allowed-ips <HealthBeat server IP> --listen 0.0.0.0:9443
```

Pull modunda script kendinden imzalı bir TLS sertifikası üretir (kendi sertifikanı vermek için
`--tls-cert/--tls-key`). Server bu sertifikayı doğrulamaz; kimlik doğrulama **paylaşılan secret +
IP izin listesi** ile yapılır. Güvenlik duvarında server IP'sinden gelen TCP `9443`'e izin ver.

**Özel (kurum içi) CA:** server sertifikanı kendi CA'n imzalıyorsa `--insecure-skip-verify` yerine CA'yı ver:

```sh
sudo HEALTHBEAT_API_TOKEN='…' ./install.sh install --mode push \
     --server-url https://api.corp.example --host-id <uuid> --ca-cert /yol/corp-ca.crt
```

Yalnızca o CA güvenilir olur ve sertifikanın adresi de doğrulanır. Pull modunda agent sertifikasını
kendi CA'nla imzalatıp `--tls-cert/--tls-key` ile ver; server'da `PULL_CA_CERT_FILE` ayarlanırsa doğrulanır
(agent sertifikası, agent'ın IP'sini **IP SAN** olarak içermeli — bkz. `docs/DEPLOYMENT.md`).

Tüm seçenekler için `./install.sh --help`.

### 2.4 Kurulumdan sonra

- Mevcut bir yapılandırma **sessizce ezilmez**: `--force` gerekir ve önce yedeği alınır
  (`agent.json.bak.<tarih>` — bkz. bölüm 6).
- Servis, ayrıcalıksız bir `healthbeat` sistem kullanıcısıyla çalışır; yapılandırma
  `/etc/healthbeat/agent.json` (mod `0640`, `root:healthbeat`).
- **Doğrula:** `systemctl status healthbeat-agent` ve `journalctl -u healthbeat-agent -f`.
  Panelde sunucu birkaç saniye içinde **online** görünmelidir.

## 3. Docker izleme (isteğe bağlı) — güvenlik uyarısı

Container'ları izlemek için ajan `/var/run/docker.sock`'u okur. **Docker soketine erişim, o
sunucuda fiilen root yetkisi demektir.** Bu yüzden varsayılan olarak **kapalıdır**; açmak için
kurulum komutuna (bölüm 1 adım 4 / bölüm 2.3) `--docker` ekle (servis `docker` grubuna eklenir).
Docker'ı izlemeyen sunucularda açma. Docker yoksa/erişim yoksa ajan CPU/RAM/disk'i normal toplar,
container listesi boş olur.

Kurulumu bu kararı vermeden yaptıysan yeniden kurmana gerek yok: bölüm 6.a, `--docker`'ı
sonradan tek bir systemd drop-in'iyle nasıl ekleyip kaldıracağını anlatır.

## 4. Hangi diskler alert üretir?

Bir sunucuda 10 mount olabilir ama senin için yalnızca 2'si önemli olabilir. İki ayrı şey var:

1. **Agent hangi diskleri raporlar?** (`disk_mounts`) — grafikler ve panelde seçim listesi için.
2. **Hangi diskler alert üretir?** — **panelde, sunucu başına** seçilir.

Raporlanan bir disk, sen seçmedikçe (ya da hiçbir seçim yapmadıkça "tümü" modundayken) alert üretmez;
seçilmeyen diskin açık alert'i bir sonraki raporda kendiliğinden kapanır.

**Tüm gerçek diskleri raporlamak için** `disk_mounts: ["auto"]` (kurulumda `--disk-mounts auto`; belirli yollarla
karıştırılabilir: `/,auto`). `auto`, gerçek dosya sistemlerini `/proc/self/mountinfo`'dan keşfeder ve şunları **dışarıda
bırakır**: sanal dosya sistemleri (`proc`, `sysfs`, `cgroup`, …), bellek tabanlı olanlar (`tmpfs`), `overlay` (Docker) ve her zaman
%100 dolu olan salt okunur imajlar (`squashfs` — snap paketleri; tipik bir masaüstünde onlarca tane). Aynı aygıtın birden çok
yerden bağlanması (bind mount) tek disk sayılır. Ağ dosya sistemleri (NFS/CIFS) dahildir; yanıt vermeyen bir NFS mount'u
toplamayı **dondurmaz** (her mount en fazla 2 sn beklenir, takılanlar atlanır).

**Panelde seçim:** Sunucu detay sayfası → **Disk alert'leri**:

- **Raporlanan tüm diskler** — seçim yapılmamış; raporlanan her disk eşiği aşarsa alert üretir (varsayılan).
- **Yalnızca seçtiklerim** — agent'ın son raporladığı diskler doluluk yüzdeleriyle listelenir; alert istediklerini işaretle.
  Henüz raporlanmayan bir yolu elle de ekleyebilirsin. Hiçbiri işaretli değilse bu sunucu için disk alert'i **kapalıdır**.

Sunucuyu **eklerken** de (veri henüz gelmeden) "Alert üretecek diskler" alanına yollar yazılabilir (`/, /data`); boş bırakırsan
tümü. Yalnızca süper admin ve organizasyon admin değiştirebilir; operatör görüntüler.

Alert'ler **disk başına** ayrı ayrı açılır, yükselir ve kapanır; e-posta konusu diski söyler:
`[HealthBeat] WARNING disk alert: web-1 (mount /data)`. Eşikler (uyarı/kritik) tüm diskler için aynıdır.

**Bilinen sınırlar:** btrfs alt hacimleri (`/` ve `/home` aynı havuzda) ayrı mount göründüğünden ikisi de listelenir ve aynı
doluluğu gösterir — birini seçmemen yeterli. Seçili bir diskin **raporlarda kaybolması** (ör. unmount) şu an alert üretmez.

## 5. Yapılandırma referansı (`/etc/healthbeat/agent.json`)

Aynı JSON şeması iki modu da kapsar; `mode` hangi alanların zorunlu olarak doğrulanacağını seçer
(agent açılışta doğrular — eksik/yanlış bir alan varsa servis net bir hatayla başlamaz, bkz.
`journalctl -u healthbeat-agent`). Dosyayı **elle düzenleyip** `sudo systemctl restart
healthbeat-agent` demek resmî olarak desteklenir; her düzeltme için `install.sh`'ı yeniden
çalıştırman gerekmez (bkz. bölüm 6).

### Ortak (her iki modda)

| Alan | Açıklama |
| --- | --- |
| `mode` | `push` (varsayılan) veya `pull`. **Oluşturulduktan sonra değiştirilemez** (bölüm 6.e). |
| `disk_mounts` | Raporlanacak mount noktaları (varsayılan `["/"]`). Her girdi mutlak bir yol ya da **`"auto"`** olmalı (bölüm 4). Raporlamak tek başına alert üretmez. |

### Push modu

| Alan | Zorunlu | Açıklama |
| --- | --- | --- |
| `server_url` | evet | `https://host[:port]` — HealthBeat server'ının adresi |
| `host_id` | evet | Panelin sunucuyu oluştururken verdiği UUID |
| `api_token` | evet | Panelin **bir kez** gösterdiği token. Kaybolursa panelden yenile (bölüm 6.c) |
| `interval_seconds` | hayır | Gönderim aralığı, saniye (varsayılan 30) |
| `ca_cert_file` | hayır | Server sertifikasını imzalayan (özel) CA'nın PEM dosyası. Verilirse **yalnızca o CA** güvenilir; `insecure_skip_verify` ile birlikte olamaz |
| `insecure_skip_verify` | hayır | **Her** server sertifikasını kabul et (yalnızca geliştirme) |

Örnek (`install.sh --mode push --server-url https://api.example.com --host-id ... --interval 30`'un ürettiği dosya):

```json
{
  "mode": "push",
  "server_url": "https://api.example.com",
  "host_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "api_token": "0f1c8b6a2e9d4a7fb5c3d6e8a1b2c4d6",
  "interval_seconds": 30,
  "insecure_skip_verify": false,
  "disk_mounts": ["/", "/data"]
}
```

### Pull modu

| Alan | Zorunlu | Açıklama |
| --- | --- | --- |
| `listen_addr` | evet | Dinleme adresi, `host:port` (kurulumda varsayılan `0.0.0.0:9443`) |
| `pull_endpoint` | evet | Status yolu, `/` ile başlamalı (varsayılan `/api/v1/status`) |
| `pull_secret` | evet | Panelin **bir kez** gösterdiği secret. Kaybolursa panelden yenile (bölüm 6.c) |
| `allowed_server_ips` | evet | Poll etmesine izin verilen IP/CIDR'ler. Diğer adreslerin bağlantısı TLS'ten önce kapatılır |
| `tls_cert_file`, `tls_key_file` | evet | Sertifika ve anahtar dosya yolları (kurulumda yoksa kendinden imzalı üretilir) |

Örnek:

```json
{
  "mode": "pull",
  "listen_addr": "0.0.0.0:9443",
  "pull_endpoint": "/api/v1/status",
  "pull_secret": "k7UXHUc3XNqyVkpBFaKhCKRc2WHi6GXg",
  "allowed_server_ips": ["203.0.113.10"],
  "tls_cert_file": "/etc/healthbeat/tls/cert.pem",
  "tls_key_file": "/etc/healthbeat/tls/key.pem",
  "disk_mounts": ["/"]
}
```

## 6. Sık yapılan hatalar ve düzeltmeler

**Genel kural:** yalnızca **bir alanı** düzeltiyorsan (adres, token, disk listesi, ...)
`/etc/healthbeat/agent.json`'u elle düzenleyip `sudo systemctl restart healthbeat-agent` yeterli
— yeniden kurulum gerekmez. `install.sh`'ı `--force` ile yeniden çalıştırmak yalnızca binary'yi
değiştirirken, TLS sertifikasını yeniden üretmek istediğinde ya da birden fazla alanı aynı anda
değiştirirken gerekir (ve *tüm* config'i, verdiğin parametrelerle **sıfırdan yazar** — eksik
bıraktığın bir alan varsayılana döner).

### a) Kurulumda `--docker` unutuldu, sonradan nasıl eklenir?

Yeniden kurulum ya da mevcut token/secret'a dokunmak **gerekmez** — `--docker`'ın tek etkisi bir
systemd drop-in'idir (`install.sh` de içeride bunu yapar):

```sh
sudo install -d -m 0755 /etc/systemd/system/healthbeat-agent.service.d
sudo tee /etc/systemd/system/healthbeat-agent.service.d/docker.conf >/dev/null <<'EOF'
[Service]
SupplementaryGroups=docker
EOF
sudo systemctl daemon-reload
sudo systemctl restart healthbeat-agent
```

Geri almak (Docker izlemeyi kapatmak) için drop-in'i sil ve tekrar `daemon-reload` + `restart` yap:

```sh
sudo rm -rf /etc/systemd/system/healthbeat-agent.service.d
sudo systemctl daemon-reload
sudo systemctl restart healthbeat-agent
```

### b) Yanlış `server_url` / `host_id` / `listen_addr` / `allowed_server_ips` girildi

`agent.json`'da ilgili alanı düzelt, restart et:

```sh
sudo nano /etc/healthbeat/agent.json   # server_url, host_id, allowed_server_ips, ...
sudo systemctl restart healthbeat-agent
sudo journalctl -u healthbeat-agent -f  # hata varsa hemen görünür (config açılışta doğrulanır)
```

`install.sh --force` ile yeniden kurmak da işe yarar ama token/secret'ı (bölüm 6.c'ye bakmadıysan
elinde yoksa) tekrar vermen gerekir — tek bir adres düzeltmek için gereksiz.

### c) Token/secret unutuldu, yanlış girildi, ya da log'da `401` görüyorsun

1. Panelde ilgili sunucunun **detay sayfasına** git → **"Kimlik bilgisini yenile"**. Bu, eski
   token/secret'ı **anında geçersiz kılar** ve yenisini bir kez gösterir.
2. Yeni değeri `agent.json`'da `api_token` (push) ya da `pull_secret` (pull) alanına yaz.
3. `sudo systemctl restart healthbeat-agent`.

`install.sh`'ı yeniden çalıştırmana gerek yok — token/secret yalnızca bir JSON alanıdır. (Diğer
`401` nedeni: token doğru ama agent'ın kayıtlı host id'siyle uyuşmuyor — ikisini panelden
karşılaştır.)

### d) `disk_mounts` listesini değiştirmek istiyorsun

`agent.json`'da `disk_mounts` dizisini düzenle (mutlak yol ya da `"auto"`), restart et. Yanlış bir
değer (ne `/` ile başlıyor ne `auto`) agent'ı açılışta net bir hatayla durdurur, sessizce
yutulmaz. Unutma: bu yalnızca **raporlamayı** değiştirir — hangi diskin **alert** üreteceği ayrıca
panelden seçilir (bölüm 4).

### e) push'tan pull'a (ya da tersi) geçmek istiyorsun

`mode`, sunucu panelde **oluşturulurken** sabitlenir; API bunu sonradan güncellemeye izin vermez
(yalnızca hostname/IP/interval güncellenebilir). Yani mod değiştirmek = **yeni bir kayıt**:

1. Panelde **yeni modda** yeni bir sunucu kaydı oluştur (yeni host id + yeni token/secret alırsın).
2. Sunucuda `install.sh install --force --mode <yeni mod> ...` ile yeni kimlik bilgileriyle yeniden kur
   (ya da önce `./install.sh uninstall` ile temizle, sonra kur).
3. Panelde **eski** kaydı sil.

**Dikkat:** panelde bir sunucu kaydını silmek, o sunucuya ait **tüm metrik geçmişini, Docker
container kayıtlarını, eşik ayarlarını ve alert'lerini de siler** (veritabanında `ON DELETE
CASCADE`) — geri alınamaz. Geçmişi kaybetmek istemiyorsan eski kaydı silmeden önce gerekiyorsa
dışa aktar/not al.

### f) `install.sh` "already exists; pass --force to replace it" diyor

Bu bilerek konmuş bir koruma — mevcut config'i yanlışlıkla ezmemek için. `wizard` ile kuruyorsan
aynı durumda önce sorar (üzerine yaz/vazgeç); `install`'i bayraklarla çalıştırıyorsan gerçekten
yeniden kurmak istediğinde `--force` ekle. Her iki yol da yeni yazmadan önce otomatik yedek alır
(`/etc/healthbeat/agent.json.bak.<tarih>`). Yanlışlıkla ezdiysen o yedekten geri dönebilirsin:

```sh
sudo cp /etc/healthbeat/agent.json.bak.20260919120000 /etc/healthbeat/agent.json
sudo systemctl restart healthbeat-agent
```

### g) Yanlış organizasyona ya da yanlış makineye kuruldu

Yanlış makinedeki kurulumu temizle (`--purge` yapılandırmayı, sertifikaları ve `healthbeat`
kullanıcısını da siler):

```sh
sudo ./install.sh uninstall --purge
```

Panelde yanlış kaydı sil (bölüm 6.e'deki cascade-delete uyarısı geçerli), doğru organizasyonda
yeniden oluştur, doğru makinede baştan kur.

## 7. Yükseltme, geri alma ve kaldırma

Agent sürümünü `healthbeat-agent --version` ile görürsün (örn. `healthbeat-agent 1.2.0 (protocol 2)`);
panelde de sunucu sayfasında **Agent** kutusunda ve sunucu listelerinde görünür. Sürüm bildirmeyen
eski agent'lar panelde "eski agent" olarak işaretlenir, Özet'teki **Agent güncellenmeli** sayacı ve
süzgeci güncellenecek sunucuları listeler.

**Yükseltme zorunlu değildir:** eski agent'lar yeni server'a metrik göndermeye devam eder (yalnızca
yeni özellikler — donanım özeti gibi — gelmez), yeni agent de eski server'a çekirdek metriklerle
düşer. Sıra önerisi: **önce server, sonra agent'lar**. Ayrıntı ve kurallar: `docs/COMPATIBILITY.md`.

### 7.1 `install.sh upgrade` (önerilen yol)

Yalnızca **binary'yi** değiştirir; yapılandırmaya, sertifikalara ve systemd unit'ine dokunmaz.

```sh
# Yeni binary'yi ve install.sh'ı hedef sunucuya kopyala, sonra:
sudo ./install.sh upgrade --dry-run     # ne yapılacağını göster, hiçbir şeyi değiştirme
sudo ./install.sh upgrade               # yükselt
```

Yükseltme şunları yapar (sırayla) ve **ilk başarısızlıkta durur**:

1. Yeni binary sürümünü bildirmeli (`--version`); bildirmiyorsa (1.1.0 öncesi ya da bozuk) reddedilir.
2. Yeni binary **mevcut yapılandırmayı kabul etmeli** (`--check-config`); etmiyorsa hiçbir şey değişmeden reddedilir.
3. Aynı sürüm ise "zaten güncel" der ve çıkar; daha eski sürüme düşürmeyi reddeder (`--allow-downgrade` ile izin verilir).
4. Eski binary `healthbeat-agent.prev` olarak saklanır, yenisi **atomik** yerleştirilir (rename).
5. Servis **çalışıyorsa** yeniden başlatılır ve `--verify-seconds` (varsayılan 15) boyunca ayakta kalması,
   çökme döngüsüne girmemesi beklenir. Çökerse **eski binary otomatik geri konur**, servis yeniden başlatılır,
   çıkış kodu `1` olur. Servis **durmuşsa başlatılmaz**.

Sorun çıkmadıysa bile geri dönmek istersen:

```sh
sudo ./install.sh rollback              # .prev ile mevcut binary'yi değiştirir; tekrar çalıştırırsan geri döner
```

Diğer seçenekler: `--no-restart` (yalnızca binary'yi koy, yeniden başlatmayı sen yap), `--update-unit`
(systemd unit'ini de betikle gelen sürümle değiştirir; yerel düzenlemeler için normalde
`systemctl edit healthbeat-agent` drop-in'ini kullan, `upgrade` unit farklıysa yalnızca haber verir).

Push modunda server'a ulaşılamaması bir çökme değildir; doğrulama "süreç ayakta mı" diye bakar, "metrik
gitti mi" diye değil. Yükseltmeden sonra panelde sunucunun **Agent** kutusunun yeni sürümü gösterdiğini
ve metriklerin aktığını yine de kontrol et.

### 7.2 Çok sunuculu yükseltme: kanarya ve elle sıra

Yükseltme **bilerek elle** yapılır; kodla toplu/otomatik dağıtım yoktur. Her sunucuda aynı adımlar:

```sh
# 1) yeni binary'yi derle (bir kez) ve sürümü doğrula
cd agent && go build -o healthbeat-agent ./cmd/agent && ./healthbeat-agent --version

# 2) hedef sunucuda (normal bir terminalde; sudo parola sorabilir):
#    healthbeat-agent, install.sh ve healthbeat-agent.service aynı dizinde olsun
sudo ./install.sh upgrade --dry-run     # planı ve config denetimini gör, hiçbir şeyi değiştirme
sudo ./install.sh upgrade               # yükselt
```

Sıra: önce **tek bir sunucuda (kanarya)** uygula, panelde o sunucunun **Agent** kutusunun yeni sürümü
ve "güncel" rozetini gösterdiğini, CPU/RAM/disk verisinin aktığını birkaç dakika izle; sorun yoksa kalan
sunuculara tek tek (ya da kendi belirlediğin gruplarla) uygula. Her sunucuda çıktının `upgraded … -> X.Y.Z`
ile bittiğini gör: `upgrade` başarısızlıkta kendi geri almasını yapar ve sıfırdan farklı kodla çıkar, yani
"hata yok" demek çıktının sonunda `upgraded` görmek demektir. Özet ekranındaki **Agent güncellenmeli**
sayacı ilerlemeyi gösterir; hangi sürümün "güncel" sayıldığı `LATEST_AGENT_VERSION` (server ortamı) ile
belirlenir, yeni bir agent sürümü yayınlarken onu da güncelle (bkz. `docs/DEPLOYMENT.md`).

**Neden otomatik kendini güncelleme yok?** Agent'ın kendini indirip değiştirmesi, imzalı bir artifact
dağıtım kanalı ve imza doğrulaması gerektirir; aksi halde tedarik zinciri saldırısı için ideal bir kapı
olur (agent'lar sunucularda ayrıcalıklı yerlerde çalışır). Bu yüzden yükseltme, sen tetikleyene kadar
yapılmaz.

### 7.3 Elle yükseltme ve kaldırma

`install.sh upgrade` kullanamıyorsan (paketleme vb.):

```sh
# yükseltme: yeni binary'yi koy ve yeniden başlat (yapılandırma korunur)
sudo install -m 0755 healthbeat-agent /usr/local/bin/healthbeat-agent
sudo systemctl restart healthbeat-agent
healthbeat-agent --version            # doğrula

sudo ./install.sh uninstall            # binary ve servisi kaldırır, yapılandırmayı korur
sudo ./install.sh uninstall --purge    # yapılandırma, sertifikalar ve kullanıcıyı da siler
```

## 8. Servisin sertleştirmesi

`healthbeat-agent.service` en az ayrıcalıkla çalışır: `NoNewPrivileges`, boş capability kümesi,
`ProtectSystem=strict`, `PrivateTmp/PrivateDevices`, çekirdek koruma direktifleri, seccomp
filtresi (`@system-service`, `~@privileged`), yalnızca `AF_INET/AF_INET6/AF_UNIX`.
`systemd-analyze security` puanı **1.6 (OK)**.

Doğrulama durumu (dürüst kayıt): unit `systemd-analyze verify` ile temiz; seccomp/`MemoryDenyWriteExecute`
kümesi, `ProtectSystem=strict`, `ProtectHome=read-only`, `PrivateTmp` ve `RestrictNamespaces` ile
**gerçek ajan bir kullanıcı systemd örneğinde çalıştırılıp** metrik verdiği görüldü. Capability
düşürme, `Protect*Kernel*`, `PrivateDevices` gibi direktifler root'un systemd örneğini gerektirir ve
bu ortamda **denenemedi**; ilk gerçek kurulumda `systemctl status` ile kontrol et.
**`SystemCallFilter=~@resources` ekleme:** `systemd-analyze` önerse de ajanı çökertir.

## 9. Sorun giderme

| Belirti | Olası neden |
| --- | --- |
| Push: log'da `server returned 401` | Token/host id yanlış ya da credential döndürüldü (bölüm 6.c ile yenile) |
| Push: `x509: certificate signed by unknown authority` | Server sertifikasını özel bir CA imzalıyor: CA'yı `--ca-cert` / `ca_cert_file` ile ver (ya da halka açık CA'lı bir sertifika kullan). Yalnızca geliştirmede `insecure_skip_verify` |
| Push: `x509: certificate is valid for …, not …` | Sertifika, `server_url`'deki host için (DNS/IP SAN) düzenlenmemiş |
| Push: log'da `server rejected the full payload … switching to core metrics only` | Server bu agent'ın gönderdiği yeni alanları tanımıyor (agent server'dan yeni). Metrikler **kaybolmaz** (CPU/RAM/disk/Docker gider), yalnızca donanım özeti gibi yeni alanlar gelmez. Server'ı güncelle; agent her 10 döngüde tam payload'ı yoklar ve server güncellenince kendiliğinden düzelir |
| Push: log'da `server recommends agent X … update when convenient` | Server yeni bir agent sürümü öneriyor (`LATEST_AGENT_VERSION`); bilgi amaçlı, zorunlu değil |
| Push: `429 çok fazla istek` | Aynı IP'den çok fazla başarısız kimlik doğrulama; birkaç dakika bekle |
| Pull: panelde sunucu offline | Güvenlik duvarı, `allowed_server_ips`'de server IP'si yok ya da secret uyuşmuyor (bölüm 6.b/6.c) |
| Sunucu bir süre sonra **offline** alert'i veriyor | Ajan durmuş ya da ağ kopuk (push'ta `interval_seconds × 3` sessizlik) |
| `install.sh`: `... already exists; pass --force ...` | Bölüm 6.f |
| Kurulumda bir alan yanlış/eksik girildi | Bölüm 6 (a-g) — çoğu durumda yeniden kurulum gerekmez |

## 10. Envanter (makine bilgisi)

Agent 1.3.0 (protokol 3) yüzdelerin yanında makinenin **envanterini ve anlık durumunu** da bildirir; panelde sunucu
sayfasının **Sistem** sekmesinde (gruplanmış kartlar) görünür. Yalnızca **bilgi içindir**: alert üretmez.

**Ne bildirilir** (hepsi yetkisiz okunabilen kaynaklardan):

| Alan | Kaynak | Güncelleme |
| --- | --- | --- |
| İşletim sistemi (ad, sürüm) | `/etc/os-release` | saatte bir |
| Kernel sürümü, mimari | `/proc/sys/kernel/osrelease`, agent binary'sinin mimarisi | saatte bir |
| Hostname, saat dilimi, init sistemi | `os.Hostname`, `/etc/timezone` ya da `/etc/localtime`, `/run/systemd/system` | saatte bir |
| CPU modeli | `/proc/cpuinfo` | saatte bir |
| Sanallaştırma (fiziksel / sanal makine / konteyner) ve donanım üreticisi/modeli | `/sys/class/dmi/id/{sys_vendor,product_name,…}`, `hypervisor` bayrağı, `/run/systemd/container` | saatte bir |
| Güvenlik modülü (AppArmor / SELinux) | `/sys/fs/selinux/enforce`, `/sys/module/apparmor` | saatte bir |
| Makine kimliği **özeti** | `/etc/machine-id`'nin HMAC-SHA256 özeti | saatte bir |
| IP adresleri (arayüzle) | `/proc/net/fib_trie`, `/proc/net/route`, `/proc/net/if_inet6` | her raporda |
| Uptime, açılış zamanı, yük ortalaması, swap | `/proc/uptime`, `/proc/loadavg`, `/proc/meminfo` | her raporda |
| Saat senkronu, başarısız servis sayısı | `timedatectl show`, `systemctl --failed` (systemd varsa; 3 sn zaman aşımı) | 5 dakikada bir |
| Yeniden başlatma gerekiyor mu | `/run/reboot-required` (**yalnızca Debian/Ubuntu ailesi**; diğerlerinde "bilinmiyor") | 5 dakikada bir |
| Docker sürümü | Docker socket (`--docker` etkinse) | 5 dakikada bir |
| Disk başına inode doluluğu | `statfs` | her raporda |

**Ne bildirilmez (bilerek):** DMI seri numarası ve UUID'si (`/sys/class/dmi/id/product_serial`, `product_uuid` yalnızca root
okur), MAC adresleri, ham `/etc/machine-id`, kullanıcı hesapları ve oturumları, süreç listesi ve komut satırları, ortam
değişkenleri, dosya içerikleri, paket listesi, açık portlar, güvenlik duvarı kuralları, SMART disk sağlığı, bekleyen paket
güncellemesi sayısı. Bunların çoğu root gerektirir; **envanter için servisin sandbox'ı gevşetilmez** ve `healthbeat`
kullanıcısına ek yetki verilmez. Bir alan okunamazsa "bilinmiyor" olarak boş kalır (`false`/`0` ile karıştırılmaz).

**Sandbox uyumu (neden bazı yollar seçildi):** agent'ın systemd unit'i `ProtectClock`, `~@privileged` ve
`RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX` kullanır. Bu yüzden `adjtimex` çağrısı **süreci öldürürdü** (SIGSYS) ve
`net.Interfaces()` (netlink) çalışmaz; saat senkronu için `timedatectl` (systemd'ye D-Bus ile sorar), IP adresleri için
`/proc/net/*` kullanılır. Toplayıcılar unit'in sandbox direktifleriyle (`systemd-run --user`) çalıştırılıp doğrulandı.

**Ne gönderiyor, görmek için:** `healthbeat-agent --print-inventory` bu agent'ın raporlayacağı envanteri yapılandırma
gerekmeden JSON olarak yazdırır (ör. bir sunucuyu panele eklemeden önce).

**Panelde uyarılar:** panelde kayıtlı **hostname** agent'ın bildirdiğinden farklıysa (büyük/küçük harf ve alan adı eki yok
sayılır) ve panelde kayıtlı **IP** agent'ın bildirdiği adresler arasında yoksa ilgili satırda uyarı gösterilir. NAT/genel IP
arkasındaki bir push sunucusunda IP uyarısı beklenen bir durumdur; alert üretmez.

**Sınırlar:** mimari, agent binary'sinin çalıştığı mimaridir (64 bit çekirdekte 32 bit userland nadirdir); RHEL ailesinde
"yeniden başlatma gerekiyor" bilgisi yetkisiz güvenilir bir kaynak olmadığı için bilinmez; saat senkronu ve başarısız servis
sayısı systemd gerektirir; sanallaştırma tespiti DMI'a ve hipervizör bayrağına dayanır (bulut sağlayıcılarının çoğu tanınır,
tanınmayan bir hipervizör "sanal makine" olarak, satıcısız gösterilir).
