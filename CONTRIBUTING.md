# Katkı ve dal düzeni

Kısa özet: **`main` her zaman yayınlanabilir; her iş kısa ömürlü bir dalda yapılır ve Pull Request ile `main`'a
girer; sürüm etiketleri yalnızca `main`'dan atılır.** (GitFlow'daki gibi `develop` dalı yoktur.)

## 1. Dal düzeni

| Dal | Ne için | Ömrü |
| --- | --- | --- |
| `main` | Yayınlanabilir tek hat. Doğrudan commit atılmaz (PR ile girer). | kalıcı |
| `<tür>/<kapsam>-<konu>` | Bir iş: `feat/panel-sifre-sifirlama`, `fix/server-migration-sirasi`, `docs/dagitim-rehberi` | **1–3 gün**; birleşince silinir |
| `release/agent-1.0`, `release/server-1.0` | **Yalnızca** eski bir sürüme acil düzeltme gerektiğinde, ilgili etiketten açılır (bölüm 5) | düzeltme yayınlanınca silinir |

- **Türler:** `feat`, `fix`, `refactor`, `docs`, `ci`, `chore`. **Kapsamlar:** `agent`, `server`, `panel`, `docs`, `ci`.
- Dalı **küçük tut**: bir dal bir konuyu çözer. Büyük bir yeniden yapılandırma (çok dosya, çok dizin) tek dalda bekletilmez;
  `main` üzerinden parçalara bölünüp her parça ayrı PR'la girer. Uzun yaşayan dal, `main` ilerledikçe çakışma borcu biriktirir.
- Dalını **her gün** güncelle: `git fetch && git rebase origin/main`.

## 2. Commit iletileri

İlk kelime kapsamı söyler (Türkçe, mevcut geçmişle uyumlu): `Panel: …`, `Server: …`, `Agent: …` (dizin adı `agent/`),
`Docs: …`, `CI: …`. Kapsam iki işe yarar: hangi **sürüm hattının** çıkacağı ve değişiklik günlüğünün nereye yazılacağı buradan belli olur.

## 3. Pull Request

- PR açıklaması `.github/pull_request_template.md` kontrol listesini izler.
- **CI yeşil olmadan birleştirme.** `ci.yml` her PR'da çalışır (agent, server, panel, sürüm betikleri).
- **Hangi hat?** `agent/` değiştiyse **agent** hattı, `server/` ve `server/panel/` değiştiyse **server + panel** hattı etkilenir
  (bkz. `docs/DISTRIBUTION.md` §11.2). Her PR, etkilediği hattın değişiklik günlüğüne (`agent/CHANGELOG.md` ya da
  `server/CHANGELOG.md`, "Yayınlanmamış" bölümü) bir madde ekler; panel maddeleri "Panel:" ile başlar.
- **Sözleşme değişikliği (ingest alanı/protokol) tek PR'dadır:** agent tarafı, server tarafı, `server/testdata/payloads/` ve
  `scripts/compat_e2e.sh` birlikte gelir (`docs/COMPATIBILITY.md` §6 kontrol listesi). Dağıtım sırası yine server, sonra agent.
- **Migration'lar yalnızca ileri yönlü ve eklemelidir** (yeni tablo / nullable sütun); eski server yeni şemayla çalışabilmeli.
- **`PROGRESS.md`** her işin sonuna eklenir; aynı anda açık iki PR aynı dosyanın sonunu değiştirdiği için çakışma çıkarsa
  iki bölümü de koru.
- Birleştirme biçimi: **squash** (PR başlığı commit iletisi olur; geçmiş temiz kalır). Birleşince dalı sil.

## 4. Sürüm çıkarma

Sürümler yalnızca `main`'dan, etiketle çıkar: `agent/vX.Y.Z` ya da `server/vX.Y.Z`. Adımlar ve kontrol listeleri:
`docs/DISTRIBUTION.md` §11. Etiketi atmadan önce `scripts/release.sh <hat> X.Y.Z --check` çalıştır.

## 5. Eski sürüme acil düzeltme (release dalı)

Yalnızca `main`'daki yayınlanmamış değişiklikleri **göndermek istemediğinde**:

```sh
git switch -c release/agent-1.0 agent/v1.0.0      # düzeltilecek sürümün etiketinden aç
git cherry-pick <düzeltme-commit'i>                # düzeltme önce main'da (PR ile) yapılır, buraya alınır
# agent/…/version.go Version = 1.0.1 ve server/…/version.go LatestAgent = 1.0.1 (aynı commit), agent/CHANGELOG.md bölümü
git tag agent/v1.0.1 && git push origin release/agent-1.0 agent/v1.0.1
```

Sonra `LatestAgent` ve değişiklik günlüğü değişikliğinin `main`'a da işlendiğini doğrula; dalı sil. `main` zaten yayınlanabilirse
release dalı açma: doğrudan `main`'dan etiketle.

## 6. GitHub ayarları (bir kez)

Settings → Branches → `main` için: PR zorunlu, durum kontrolleri (CI) zorunlu, doğrudan push kapalı. Settings → Tags: `agent/*`
ve `server/*` etiketlerini yalnızca yöneticiler oluşturabilsin. **Not:** özel (private) repolarda bu koruma kuralları GitHub
planına bağlıdır; kullanılamıyorsa bu belgedeki kurallar disiplinle uygulanır.

## 7. Testleri yerelde çalıştırma

```sh
cd server && TEST_DATABASE_URL=postgres://… go test ./... -race     # veritabanı testleri geçici şemada çalışır
cd agent && go test ./... -race && ../agent/deploy/install_test.sh
cd server/panel && npx tsc -b && npm test && npm run build
scripts/release_test.sh                                            # sürüm betikleri
scripts/compat_e2e.sh                                              # ingest'e dokunulduysa
```
