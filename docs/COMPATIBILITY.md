# HealthBeat — Sürüm uyumluluğu

Sahada onlarca/yüzlerce sunucuda agent çalışır. İki şey **her zaman** doğru olmalı:

- **Server güncellenince agent'ları tek tek güncellemek zorunda kalmayız.** Eski agent'lar yeni
  server'a metrik göndermeye devam eder.
- **Agent, server'dan önce güncellenirse metrikler kaybolmaz.** Yeni agent eski server'a çekirdek
  metriklerle (CPU/RAM/disk/Docker) düşer, server güncellenince kendiliğinden tam moda döner.

Bu belge bunu sağlayan sözleşmeyi, panelde nasıl izlendiğini ve yeni alan/özellik eklerken uyulacak
kuralları anlatır. Deploy sırası önerisi: **önce server, sonra agent'lar** (zorunlu değil, aşağıya bak).

## 1. Sözleşme

1. **Ingest yalnızca ekleme yapar.** `POST /api/v1/metrics` gövdesine (ve pull yanıtına) alan
   eklenebilir; alan silinmez, anlamı değişmez, **zorunlu alan eklenmez**.
2. **Server "liberal" kabul eder.** Bilinmeyen alanlar hata değildir (`model.ParseMetricsIngest`).
   Yalnızca çekirdek alanların (`cpu_usage_pct`, `ram_usage_pct`, `disk`, `docker_containers`) tipi
   bozuksa, aralık dışıysa ya da gövde bir JSON nesnesi değilse 400 döner. Panel API'leri (kullanıcı
   girdisi) ise **katı** kalır: bilinmeyen alan orada bir istemci hatasıdır.
3. **Eksik alan = "bilinmiyor".** Yeni alanlar `omitempty`'dir; 0/boş "bilinmiyor" demektir ve
   saklanan son bilinen değeri silmez (`store.Agents.MarkOnline`).
4. **Opsiyonel alanlar çekirdek metrikleri asla düşürmez.** Bozuk bir donanım listesi reddedilmez,
   temizlenir (`sanitizePhysicalDisks`).
5. **Sürüm bilgisi gövdeye değil başlığa konur.** Gövdedeki bilinmeyen alan, bu sözleşmeyi henüz
   bilmeyen eski bir server'da 400 üretirdi; başlıklar ise sessizce yok sayılır.
6. **Migration'lar yalnızca nullable/varsayılanlı sütun ekler**; server geri alınırsa eski kod yeni
   sütunları görmez ama çalışır. `down` migration'lar canlıda kullanılmaz.

## 2. Sürümler ve protokol

İki ayrı sayı vardır:

| | Ne | Nerede | Ne için |
| --- | --- | --- | --- |
| **Sürüm** | SemVer (`1.0.0`) | `agent/internal/version` (agent) ve `server/internal/version` (server + panel), **bağımsız** iki sürüm hattı | Panelde "hangi sunucuda hangi agent var", güncel/eski sınıflandırması |
| **Protokol** | Tamsayı (`2`) | aynı paketlerde `Protocol` | "Hangi alan kümesini konuşuyorum"; uyumluluk kararları buna göre |

Protokol geçmişi:

| Protokol | Anlamı |
| --- | --- |
| **1** | Sürüm/protokol başlığı göndermeyen istemciler (server bunu 1 sayar; ilk sürüm 1.0.0 agent'ı protokol 3 konuşur) |
| **2** | Donanım özeti (`cpu_cores`, `ram_total_mb`, `physical_disks`) + sürüm/protokol başlıkları |
| **3** | Makine envanteri (`host_info`) + disk girdilerinde `inodes_used_pct` |

**Ne zaman artırılır?** Ingest'e yeni bir alan kümesi eklenince **protokol** artar (ve sürümün
minor'ı); yalnızca hata düzeltmesi ise sürümün patch'i. Sürümü elle artır; `agent`/`server` içinde
`Version` değişkeni tek kaynaktır (`-ldflags "-X …version.Version=…"` ile de ezilebilir).

**İki bağımsız sürüm hattı:** agent (`agent/vX.Y.Z`) ile server + panel (`server/vX.Y.Z`) ayrı sürümlenir; biri çıkınca
diğerinin numarası değişmez. Uyumluluk sürüme değil **protokole** dayanır (yukarıdaki tablo): agent 1.x, protokolü
anlayan her server ile çalışır. Server'ın panelde "güncel agent" saydığı sürüm kendi sürümü değil, derlemenin bildiği
en güncel agent sürümüdür (`server/internal/version` içindeki `LatestAgent`; yeni bir agent yayınlanırken aynı commit'te
artırılır, `scripts/release.sh agent` bunu doğrular). Bkz. `docs/DISTRIBUTION.md`.

## 3. Başlıklar

| Yön | Başlık | Anlamı |
| --- | --- | --- |
| agent → server (push isteği) | `User-Agent: healthbeat-agent/1.0.0`, `X-HealthBeat-Protocol: 3` | agent sürümü ve protokolü |
| agent → server (pull yanıtı) | `X-HealthBeat-Agent-Version`, `X-HealthBeat-Protocol` | aynısı |
| server → agent (ingest yanıtı, 400 dahil) | `X-HealthBeat-Server-Version`, `X-HealthBeat-Protocol`, `X-HealthBeat-Latest-Agent` | server sürümü, anladığı en yüksek protokol, önerdiği agent sürümü |
| server → agent (pull isteği) | `User-Agent: healthbeat-server/…`, `X-HealthBeat-Server-Version` | server sürümü |

Başlık gelmezse: server bunu **protokol 1, sürüm bilinmiyor** (eski agent) sayar. Sürüm ve protokol
her istekte yazılır (son bilinen değer değil): sürüm bildirmeyen bir agent'a geri dönülürse panel
eski sürümü göstermeye devam etmez.

## 4. Senaryolar

| Agent | Server | Sonuç |
| --- | --- | --- |
| eski (protokol 1) | yeni | **Çalışır.** Yeni alanlar boş kalır; panel "eski agent" rozeti + güncelleme uyarısı gösterir |
| yeni | yeni, agent daha yeni alan gönderiyor | **Çalışır.** Bilinmeyen alanlar yok sayılır ve adları kaydedilir; panel "Server güncellenmeli" der |
| yeni | **eski** (bu sözleşmeden önceki, bilinmeyen alanı 400 ile reddeden) | **Çalışır, degraded.** Agent 400 alınca aynı döngüde yalnızca çekirdek alanlarla yeniden dener (metrik kaybolmaz), her 10 döngüde tam payload'ı yoklar; server güncellenince kendiliğinden düzelir |
| yeni | ağ/kimlik/5xx hatası | Geri dönüş **devreye girmez**: yalnızca 400 uyumsuzluk sayılır, diğer hatalar olduğu gibi bildirilir |
| pull, eski agent | yeni | Çalışır (yanıt gevşek ayrıştırılır) |
| server geri alındı | DB migration'ları ileride | Çalışır (yeni sütunlar nullable; eski kod onları seçmez) |

Ölçülmüş (bkz. §7): ilk yayımlanan agent, donanım özetinden önceki agent ve makinede kurulu gerçek
eski binary yeni server'a metrik yazar; yeni agent, eski server'a karşı çevrimiçi kalır ve metrik
yazar.

## 5. Panelde izleme

- **Sunucu sayfası:** "Agent" kutusu (sürüm + durum rozeti), güncellenmesi gerekiyorsa "Agent
  güncellenmeli" uyarısı, agent server'dan yeniyse "Server güncellenmeli" uyarısı, eski agent'ta
  donanım özeti için açıklayıcı boş durum.
- **Sunucu listeleri:** "Agent" sütunu (organizasyon sayfası ve Özet).
- **Özet:** "Agent güncellenmeli" sayacı ve "Yalnızca agent'ı güncellenmesi gereken sunucular"
  süzgeci (adreste `agentguncelle=1`).

Durumlar (`server/panel/src/pages/agentStatus.ts`):

| Durum | Ne zaman |
| --- | --- |
| **güncel** | sürüm ≥ `LATEST_AGENT_VERSION` (ya da politika tanımsız) |
| **güncelleme var** | `MIN_SUPPORTED_AGENT_VERSION` ≤ sürüm < `LATEST_AGENT_VERSION` |
| **desteklenmiyor** | sürüm < `MIN_SUPPORTED_AGENT_VERSION` |
| **eski agent** | sürüm bildirmiyor (protokol 1) |
| **bilinmiyor** | henüz rapor yok / sürüm okunamadı |

Politika **yalnızca bilgilendirir**; hiçbir agent sürümü yüzünden reddedilmez. Server ortamında:

| Değişken | Varsayılan | |
| --- | --- | --- |
| `LATEST_AGENT_VERSION` | derlemenin bildiği en güncel agent sürümü (`LatestAgent`; server'ın kendi sürümü **değil**) | panelin "güncel" saydığı agent sürümü |
| `MIN_SUPPORTED_AGENT_VERSION` | boş | bunun altı "desteklenmiyor" görünür; boşsa hiçbiri |

Geçersiz (SemVer olmayan) değer server'ın açılışta başlamasını engeller. Sürüm politikası
`GET /api/v1/meta` ile panele verilir.

## 6. Yeni bir ingest alanı eklerken

- [ ] Agent payload'ına `omitempty` ile ekle; server `MetricsIngestRequest`'e **isteğe bağlı** ekle.
- [ ] 0/boş "bilinmiyor" olsun; mevcut kaydı silmesin (`COALESCE`).
- [ ] Girdiyi temizle/sınırla (metin uzunluğu, liste boyutu, denetim karakterleri).
- [ ] Alan **çekirdek** değilse `MetricsPayload.Core()`'da çıkarılmalı (eski server'a geri dönüş). **İç içe nesnelerdeki**
      alanlar da (ör. `disk[]` girdisindeki `inodes_used_pct`) çıkarılmalı: bilinmeyen alanı reddeden eski server'lar
      iç içe alanı da reddeder. Testte "katı eski server" gerçek eski gövde tipiyle (yeni alansız) kurulmalı.
- [ ] Protokolü ve `Version`'ı artır (agent + server), §2'deki tabloya satır ekle.
- [ ] `server/testdata/payloads/` altına yeni sürümün gerçek gövdesini fixture olarak ekle
      (`vN_*.json`); `model` ve `httpapi` testleri her fixture'ın kabul edildiğini denetler.
- [ ] `scripts/compat_e2e.sh` ile eski agent sürümlerinin yeni server'da, yeni agent'ın eski
      server'da çalıştığını doğrula (§7).
- [ ] Panelde alan `optional` olsun; eski agent'ta boş durum metni göster.

## 7. Doğrulama araçları

| Araç | Ne denetler |
| --- | --- |
| `server/testdata/payloads/*.json` | Her agent sürümünün gövdesi + "gelecek" (bilinmeyen alanlı) gövde kabul edilir |
| `scripts/compat_e2e.sh` | Gerçek eski agent binary'leri (git ref'inden derlenir ya da kurulu binary) gerçek server'a metrik yazar; sürüm/protokol tutarlı; gelecekteki agent'ın bilinmeyen alanları kaydedilir |
| `scripts/compat_e2e.sh --server-ref REF` | **Yeni** agent'ın **eski** server'a karşı davranışı (çevrimiçi kalır, çekirdek metrikler yazılır) |

```sh
scripts/compat_e2e.sh                                          # ilk sürüm + donanım öncesi + şimdiki
scripts/compat_e2e.sh --binary kurulu=/usr/local/bin/healthbeat-agent
scripts/compat_e2e.sh --server-ref 02ea3a0 --ref atla=         # eski server × yeni agent
```

Script geçici bir şemada çalışır ve siler; `DATABASE_URL` (ya da `server/.env`) **asla üretim
veritabanına yöneltilmemeli**.

## 8. Kırıcı değişiklik gerekirse

Ekleme kuralına sığmayan bir değişiklik (alanın anlamı değişiyor, bir alan kalkıyor) için:
`/api/v2/metrics` gibi **paralel** bir uç nokta yayınla, eskisini en az bir destek penceresi boyunca
açık tut, `MIN_SUPPORTED_AGENT_VERSION` ile panelde uyar. Eski uç noktayı, panelde "güncellenmeli"
sayısı sıfıra inince kaldır.

## 9. Deploy sırası

Önerilen: **önce server, sonra agent'lar** — server yeni alanı tanır, agent'lar tam moda geçer.
Ters sırada (agent önce) de veri kaybı olmaz (§4), ama server güncellenene kadar yeni alanlar
gelmez ve agent günlüğünde "core metrics only" uyarısı görünür. Çok sunuculu bir dağıtımda önce
tek bir sunucuda (kanarya) deneyin. Adım adım süreç (paketler, imza, işletim sistemine göre komutlar,
geri alma): `docs/DISTRIBUTION.md`.

## 10. Bilinen sınırlar

- Bilinmeyen alan tespiti yalnızca **üst düzey** alanları görür; iç içe nesnelerdeki (örn.
  `disk[]` içindeki) yeni alanlar sessizce yok sayılır.
- Bu sözleşmeden **önceki** server sürümleri bilinmeyen alanı 400 ile reddeder; yeni agent onlara
  karşı degraded (çekirdek) modda çalışır. Bu server'ları güncellemek bu moddan çıkarır.
- Agent'ın kendini güncellemesi (otomatik güncelleme) **bilerek yoktur** (imzalı dağıtım kanalı
  gerektirir, bkz. `docs/AGENT.md` 7.2). Güncellemek için `install.sh upgrade` / `rollback` ve kanarya
  rehberi: `docs/AGENT.md` bölüm 7.
