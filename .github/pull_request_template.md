## Ne değişti ve neden

<!-- Kısaca; ilgili sorun/karar varsa belirt. -->

## Kapsam ve sürüm etkisi

- [ ] **agent** (`agent/`) → agent hattı etkilenir · `agent/CHANGELOG.md` "Yayınlanmamış" güncellendi
- [ ] **server / panel** (`server/`, `server/panel/`) → server hattı etkilenir · `server/CHANGELOG.md` "Yayınlanmamış" güncellendi (panel maddeleri "Panel:" ile)
- [ ] Yalnızca belge / CI (sürüm çıkarmaz)
- [ ] Görünür etkisi yok (davranışı değiştirmeyen refactor) → madde "Yayınlanmamış"ın sonundaki `### İç değişiklikler (davranış değişmedi)` başlığında (`CONTRIBUTING.md` §3)

## Kontrol

- [ ] Testler eklendi/güncellendi ve yerelde geçiyor (komutlar: `CONTRIBUTING.md` §7)
- [ ] **Sözleşme** (ingest alanı/protokol) değiştiyse: agent + server + `server/testdata/payloads/` + `compat_e2e.sh` bu PR'da; `docs/COMPATIBILITY.md` §6 uygulandı
- [ ] **Migration** varsa yalnızca ileri yönlü ve eklemeli (yeni tablo / nullable sütun)
- [ ] Panel değiştiyse tarayıcıda görsel olarak kontrol edildi (ekran görüntüsü aşağıda)
- [ ] `PROGRESS.md`'ye not düşüldü (doğrulanmayan kısımlar dahil)

## Doğrulanmayanlar

<!-- Denenemeyen ya da yalnızca birim testiyle kapsanan kısımlar; yoksa "yok". -->
