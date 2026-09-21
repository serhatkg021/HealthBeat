# HealthBeat

Sunucuların **CPU, RAM, disk** ve **Docker container** durumunu izleyen; eşik aşımında
**e-posta ile alert** üreten, agent–server bir monitoring sistemi. Tüm karar mekanizması
(eşik kontrolü, alert, bildirim) **server** tarafındadır; agent yalnızca veri toplar.

```mermaid
flowchart LR
    C1[Agent<br/>push] -- HTTPS --> S
    S -- HTTPS poll --> C2[Agent<br/>pull]
    S[Go server<br/>+ PostgreSQL] --> M[SMTP / e-posta]
    P[Panel<br/>React SPA] -- HTTPS + CORS --> S
```

| Bileşen | Klasör | Teknoloji |
| --- | --- | --- |
| API server, alert motoru, scheduler | `server/` | Go, PostgreSQL |
| Agent (push / pull) | `agent/` | Go (yalnızca stdlib) |
| Panel (dashboard, yönetim) | `server/panel/` | React, TypeScript, Vite |
| Şema | `server/migrations/` | SQL — server binary'sine gömülü, açılışta otomatik uygulanır |
| Dokümanlar | `docs/` | [Mimari](docs/MIMARI.md) · [Veritabanı](docs/VERITABANI.md) · [Server dağıtımı](docs/DEPLOYMENT.md) · [Sürüm dağıtım süreci](docs/DISTRIBUTION.md) · [Agent kurulumu](docs/AGENT.md) · [Sürüm uyumluluğu](docs/COMPATIBILITY.md) |

Roller: **Süper Admin**, **Organizasyon Admin**, **Operatör** (izinler `role_permissions` tablosundan). Organizasyonlar bir
ağaçtır; alert alıcıları organizasyon/sunucu bazında bildirim kurallarıyla belirlenir.
Mimari ve tasarım kararları: [`docs/MIMARI.md`](docs/MIMARI.md) · veritabanı: [`docs/VERITABANI.md`](docs/VERITABANI.md).
Geliştirme durumu ve açık işler: [`PROGRESS.md`](PROGRESS.md).

## Hızlı başlangıç (geliştirme)

Gereksinimler: Go ≥ 1.22, **PostgreSQL ≥ 15**, Node ≥ 22, `openssl`.

```sh
# 1) Veritabanı (boş bir veritabanı yeterli: şema server açılırken kendiliğinden kurulur)
createdb healthbeat        # (uygun bir PostgreSQL rolüyle)
export DATABASE_URL='postgres://USER:PASS@localhost:5432/healthbeat?sslmode=disable'

# 2) Server yapılandırması: server/.env (gitignore'da)
cd server
mkdir -p certs && openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj "/CN=localhost" \
    -keyout certs/key.pem -out certs/cert.pem
cat > .env <<ENV
DATABASE_URL=$DATABASE_URL
TLS_CERT_FILE=certs/cert.pem
TLS_KEY_FILE=certs/key.pem
JWT_ACCESS_SECRET=$(openssl rand -base64 48)
JWT_REFRESH_SECRET=$(openssl rand -base64 48)
SECRETS_ENCRYPTION_KEY=$(openssl rand -base64 32)
BOOTSTRAP_ADMIN_EMAIL=admin@example.com          # ilk süper admin (ilk açılıştan sonra silinebilir)
BOOTSTRAP_ADMIN_PASSWORD='en-az-12-karakterlik-bir-sifre'
ENV
go run ./cmd/server              # https://localhost:8443  (şemayı da kurar)
#   go run ./cmd/server migrate status   # migration durumu

# 3) Panel (başka bir terminalde)
cd server/panel && npm install && npm run dev     # http://localhost:5173  (/api/v1 → https://localhost:8443)
```

İlk giriş: veritabanı boşsa hiç kullanıcı yoktur; ilk süper admin, server açılırken
`BOOTSTRAP_ADMIN_EMAIL` ve `BOOTSTRAP_ADMIN_PASSWORD` (en az 12 karakter) ortam değişkenlerinden
oluşturulur — bunları `server/.env`'e ekleyip server'ı başlat. Bu hesapla panele girince **yeni bir
şifre belirlemen istenir**; sonra bu iki değişkeni `.env`'den silebilirsin. Agent kurulumu için
[docs/AGENT.md](docs/AGENT.md).

Tüm ortam değişkenleri: [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#2-server-yapılandırması-ortam-değişkenleri).

## Testler

```sh
# Server (veritabanı gerektiren testler TEST_DATABASE_URL yoksa atlanır)
cd server
set -a && . ./.env && set +a
TEST_DATABASE_URL="$DATABASE_URL" go test ./... -race

# Agent ve kurulum script'i
cd ../agent && go test ./... -race && ./deploy/install_test.sh

# Panel
cd ../server/panel && npx tsc -b && npm test && npm run build
```

Veritabanı testleri, `TEST_DATABASE_URL`'deki veritabanında **geçici bir şema** açar ve bitince
siler; mevcut tablolara dokunmaz. Yine de bunu **asla üretim veritabanına yöneltme**.

## Durum

Sürüm **1.0.0**: monitoring, alert ve panelin tamamı çalışır durumda. Açık işler ve bilinen sınırlamalar için
[`PROGRESS.md`](PROGRESS.md).
