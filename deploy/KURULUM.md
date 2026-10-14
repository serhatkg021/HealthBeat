# HealthBeat Server @VERSION@ — kurulum

Bu paket server + panel'i Docker Compose ile tek makineye kurmak için gereken dosyaları içerir; kaynak kod gerekmez.
İmajlar GHCR'dan çekilir (`@REGISTRY@`, sürüm `@VERSION@`). Tam belgeler: https://github.com/@REPO@/tree/server/v@VERSION@/docs
(`DEPLOYMENT.md` bölüm 6, `DISTRIBUTION.md` bölüm 8).

```
healthbeat/
  docker-compose.yml     düzenlemen gerekmez: portlar, adresler ve dizinler .env'den gelir
  .env.example           init-env.sh'ın .env'i üretirken kullandığı şablon (sürüm ve imaj kaynağı dolu)
  deploy/init-env.sh     .env üretir: rastgele sırlar ve geçici yönetici şifresi
  KURULUM.md, LICENSE
```

## 1. Gereksinimler

- Linux (amd64 ya da arm64), Docker Engine ve **Docker Compose v2.18 ya da üstü** (`docker compose version`), `openssl`.
- Portlar: 443 (panel, kullanıcılar) ve 8443 (API, push agent'lar); değiştirmek için `.env`'de `HB_PANEL_PORT`, `HB_API_PORT`.

## 2. Paketi doğrula

```sh
BASE=https://github.com/@REPO@/releases/download/server/v@VERSION@
curl -fLO $BASE/healthbeat-server_@VERSION@.tar.gz -O $BASE/SHA256SUMS -O $BASE/SHA256SUMS.asc
curl -fsSL https://raw.githubusercontent.com/@REPO@/main/docs/release-key.pub | gpg --import   # bir kez
gpg --verify SHA256SUMS.asc SHA256SUMS          # "Good signature"
sha256sum -c --ignore-missing SHA256SUMS        # "OK"
```

İmza ya da özet tutmazsa kurma.

## 3. Kurulum

```sh
tar xzf healthbeat-server_@VERSION@.tar.gz && cd healthbeat
./deploy/init-env.sh admin@sirket.com    # .env üretir; ekrandaki geçici şifreyi not al (ilk girişte değiştirilir)
```

`.env`'de gerekirse değiştir: `HB_TLS_HOSTS` (kendinden imzalı sertifika kullanılacaksa sunucunun adresi, ör.
`healthbeat.sirket.com`), `HB_PANEL_PORT`, `HB_API_PORT`, `HB_VOLUMES_DIR` (varsayılan `./volumes`). Sonra:

```sh
docker compose up -d
docker compose ps        # db, server, panel "healthy"; init "exited (0)"
```

Panel: `https://<adres>` (ya da `:HB_PANEL_PORT`). İlk girişten sonra `.env`'den `BOOTSTRAP_ADMIN_*` satırlarını sil; panelde
Bildirim → Kanallar'da e-postayı, Ayarlar'da panel adresini ve saat dilimini gir.

## 4. Kendi sertifikan

Veritabanı, sertifika ve loglar `volumes/` altındadır (`pgdata/`, `certs/`, `logs/`). Kendi (CA imzalı) sertifikanı
`cert.pem` (tam zincir, önce sunucu sertifikası) ve `key.pem` (şifresiz) olarak koy; tercihen ilk `up`'tan önce:

```sh
sudo mkdir -p volumes/certs
sudo cp /yol/fullchain.pem volumes/certs/cert.pem
sudo cp /yol/privkey.pem   volumes/certs/key.pem
docker compose up -d && docker compose restart panel
```

Sahiplik ve izinleri `init` adımı ayarlar. Sertifika kullanılamıyorsa (eksik, şifreli, eşleşmeyen anahtar) server
başlamaz: `docker compose logs init`. Yenilemede aynı komutlar.

## 5. Güncelleme

```sh
cd healthbeat
mkdir -p -m 700 backups      # önce yedek
(umask 077; docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists' > backups/backup-$(date +%F-%H%M).sql)
tar xzf ../healthbeat-server_<yeni>.tar.gz --strip-components=1    # yeni paketi bu dizinin üzerine aç
# .env içindeki HB_VERSION satırını <yeni> yap
docker compose pull && docker compose up -d --remove-orphans
```

Paket `.env` ve `volumes/` taşımaz; ikisi de korunur. `docker-compose.yml` her güncellemede yenisiyle değişir: kendi
ayarların varsa onları `docker-compose.override.yml`'a yaz (Compose onu kendiliğinden okur, paket ona dokunmaz).
Sürüm notlarındaki "Güncellemeden önce" bölümünü oku; yeni sürüm `.env.example`'a değişken eklediyse ihtiyacın olanı
`.env`'ye kopyala.

## 6. Yedek

`backups/` altındaki `pg_dump` çıktısı ve `.env` birlikte saklanır: `.env`'deki `SECRETS_ENCRYPTION_KEY` olmadan
veritabanındaki SMTP şifresi ve pull secret'lar çözülemez. İkisini de makine dışına kopyala.
