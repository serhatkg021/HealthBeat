#!/usr/bin/env bash
# release.sh / release-notes.sh'in sürüm-tutarlılık kurallarını geçici bir git deposunda dener (derleme, paket ve
# test çalıştırmaz: yalnızca `--check` yolu). Çalıştır: scripts/release_test.sh
set -uo pipefail
export LC_ALL=C
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0

fixture() {
  local d="$1"
  mkdir -p "$d"/{scripts,agent/internal/version,server/internal/version,server/panel}
  cp "$SRC/scripts/release.sh" "$SRC/scripts/release-notes.sh" "$d/scripts/"
  printf 'package version\n\nvar Version = "1.3.1"\n' >"$d/agent/internal/version/version.go"
  printf 'package version\n\nvar Version = "1.4.0"\n\nvar LatestAgent = "1.3.1"\n' >"$d/server/internal/version/version.go"
  printf '{\n  "name": "panel",\n  "version": "1.4.0",\n  "dependencies": { "x": { "version": "9.9.9" } }\n}\n' >"$d/server/panel/package.json"
  printf '{\n  "name": "panel",\n  "version": "1.4.0",\n  "packages": { "": { "version": "1.4.0" } }\n}\n' >"$d/server/panel/package-lock.json"
  printf '# Agent\n\n## [Yayınlanmamış]\n\n## [1.3.1] - 2026-01-02\n\n### Eklendi\n- agent notu\n\n## [1.0.0] - 2026-01-01\n\n- eski agent notu\n' >"$d/agent/CHANGELOG.md"
  printf '# Server\n\n## [Yayınlanmamış]\n\n## [1.4.0] - 2026-01-03\n\n### Eklendi\n- server notu\n\n## [1.0.0] - 2026-01-01\n\n- eski server notu\n' >"$d/server/CHANGELOG.md"
  (cd "$d" && git init -q && git config user.email t@t && git config user.name t && git add -A && git commit -q -m init)
}

# run <dir> <args...>: çıktıyı $OUT, çıkış kodunu $RC'ye koyar.
run() { local d="$1"; shift; OUT="$(cd "$d" && scripts/release.sh "$@" 2>&1)"; RC=$?; }
ok() { # ok <ad> : son run başarılı olmalı
  if [[ $RC -eq 0 ]]; then pass=$((pass+1)); else fail=$((fail+1)); echo "FAIL: $1 (exit $RC)"; echo "$OUT" | sed 's/^/      /'; fi
}
bad() { # bad <ad> <beklenen alt metin> : son run başarısız olmalı ve mesaj metni içermeli
  if [[ $RC -ne 0 && "$OUT" == *"$2"* ]]; then pass=$((pass+1)); else fail=$((fail+1)); echo "FAIL: $1 (exit $RC, want failure mentioning '$2')"; echo "$OUT" | sed 's/^/      /'; fi
}
reset() { (cd "$1" && git checkout -q -- . && git clean -fdq -e none); }
mutate() { # mutate <dir> <dosya> <sed ifadesi...>
  local d="$1" f="$2"; shift 2
  sed -i "$@" "$d/$f"
}

D="$TMP/repo"; fixture "$D"
(cd "$D" && git tag agent/v1.3.1)   # agent 1.3.1 yayınlanmış: server'ın LatestAgent'ı için kanıt

# --- agent hattı
run "$D" agent 1.3.1 --check;                                    ok "agent: tutarlı sürüm geçer"
run "$D" agent 1.3.2 --check;                                    bad "agent: kaynak sürümü ≠ istenen" "agent/internal/version/version.go says '1.3.1', not 1.3.2"
mutate "$D" server/internal/version/version.go 's/LatestAgent = "1.3.1"/LatestAgent = "1.3.0"/'
run "$D" agent 1.3.1 --check --allow-dirty;                      bad "agent: LatestAgent güncellenmemiş" "LatestAgent is '1.3.0', not 1.3.1"; reset "$D"
mutate "$D" agent/CHANGELOG.md 's/^## \[1.3.1\]/## [1.3.9]/'
run "$D" agent 1.3.1 --check --allow-dirty;                      bad "agent: changelog bölümü yok" "changelog has no '## [1.3.1]' section"; reset "$D"
mutate "$D" agent/CHANGELOG.md -e 's/^- agent notu$//' -e 's/^### Eklendi$//'
run "$D" agent 1.3.1 --check --allow-dirty;                      bad "agent: boş changelog bölümü" "changelog has no '## [1.3.1]' section"; reset "$D"

# --- server + panel hattı
run "$D" server 1.4.0 --check;                                   ok "server: tutarlı sürüm geçer (LatestAgent/vX.Y.Z etiketiyle bulunur)"
run "$D" server 1.5.0 --check;                                   bad "server: kaynak sürümü ≠ istenen" "server/internal/version/version.go says '1.4.0', not 1.5.0"
mutate "$D" server/panel/package.json 's/"version": "1.4.0"/"version": "0.0.0"/'
run "$D" server 1.4.0 --check --allow-dirty;                     bad "server: server/panel/package.json sürümü farklı" "server/panel/package.json version is '0.0.0'"; reset "$D"
mutate "$D" server/panel/package-lock.json '0,/"version": "1.4.0"/s//"version": "1.3.0"/'
run "$D" server 1.4.0 --check --allow-dirty;                     bad "server: package-lock.json sürümü farklı" "server/panel/package-lock.json version is '1.3.0'"; reset "$D"
mutate "$D" server/internal/version/version.go 's/LatestAgent = "1.3.1"/LatestAgent = "9.9.9"/'
run "$D" server 1.4.0 --check --allow-dirty;                     bad "server: yayınlanmamış agent sürümü LatestAgent olamaz" "no agent release tag (agent/v9.9.9) exists"; reset "$D"
mutate "$D" server/internal/version/version.go 's/LatestAgent = "1.3.1"/LatestAgent = "latest"/'
run "$D" server 1.4.0 --check --allow-dirty;                     bad "server: LatestAgent SemVer olmalı" "is not SemVer"; reset "$D"
(cd "$D" && git tag -d agent/v1.3.1 >/dev/null)
run "$D" server 1.4.0 --check;                                   bad "server: hiçbir agent etiketi yoksa reddedilir" "no agent release tag"
(cd "$D" && git tag agent/v1.3.1)

# --- çalışma ağacı ve etiket
mutate "$D" agent/CHANGELOG.md 's/agent notu/agent notu (değişti)/'
run "$D" agent 1.3.1 --check;                                    bad "kirli çalışma ağacı reddedilir" "uncommitted changes"
run "$D" agent 1.3.1 --check --allow-dirty;                      ok "kirli ağaç --allow-dirty ile geçer"; reset "$D"
(cd "$D" && git commit -q --allow-empty -m sonraki)          # etiketler artık eski commit'i gösterir
run "$D" agent 1.3.1 --check --require-tag;                      bad "--require-tag: HEAD etiketsiz" "HEAD is not tagged agent/v1.3.1"
(cd "$D" && git tag -f agent/v1.3.1 >/dev/null)
run "$D" agent 1.3.1 --check --require-tag;                      ok "--require-tag: HEAD agent/v1.3.1 etiketinde"
run "$D" server 1.4.0 --check --require-tag;                     bad "--require-tag: agent etiketi server hattını karşılamaz" "HEAD is not tagged server/v1.4.0"
(cd "$D" && git tag server/v1.4.0)
run "$D" server 1.4.0 --check --require-tag;                     ok "--require-tag: HEAD server/v1.4.0 etiketinde"
(cd "$D" && git tag -d agent/v1.3.1 >/dev/null && git tag v1.3.1)
run "$D" agent 1.3.1 --check --require-tag;                      bad "--require-tag: hat öneksiz etiket (v1.3.1) agent hattını karşılamaz" "HEAD is not tagged agent/v1.3.1"

# --- kullanım hataları
run "$D" 1.3.1 --check;                                          bad "hat adı zorunlu (eski çağrı biçimi)" "usage: release.sh agent|server"
run "$D" both 1.3.1 --check;                                     bad "bilinmeyen hat" "usage: release.sh agent|server"
run "$D" agent 1.3 --check;                                      bad "geçersiz sürüm" "usage: release.sh agent|server"
run "$D" agent 1.3.1 --check --bogus;                            bad "bilinmeyen seçenek" "unknown option '--bogus'"
run "$D" agent 1.3.1 --check --arches "riscv";                   bad "desteklenmeyen mimari" "unsupported architecture 'riscv'"

# --- sürüm notları: her hat kendi günlüğünden okur, birbirine karışmaz
n="$(cd "$D" && scripts/release-notes.sh agent 1.3.1)"
[[ "$n" == *"agent notu"* && "$n" != *"server notu"* && "$n" != *"eski agent notu"* ]] && pass=$((pass+1)) || { fail=$((fail+1)); echo "FAIL: agent notları yalnızca 1.3.1 bölümü ve yalnız agent günlüğü"; echo "$n"; }
n="$(cd "$D" && scripts/release-notes.sh server 1.4.0)"
[[ "$n" == *"server notu"* && "$n" != *"agent notu"* && "$n" != *"eski server notu"* ]] && pass=$((pass+1)) || { fail=$((fail+1)); echo "FAIL: server notları yalnızca 1.4.0 bölümü ve yalnız server günlüğü"; echo "$n"; }
(cd "$D" && scripts/release-notes.sh agent 9.9.9 >/dev/null 2>&1) && { fail=$((fail+1)); echo "FAIL: olmayan sürümün notu hata vermeli"; } || pass=$((pass+1))
(cd "$D" && scripts/release-notes.sh 1.3.1 >/dev/null 2>&1) && { fail=$((fail+1)); echo "FAIL: hat adı olmadan not istenemez"; } || pass=$((pass+1))

echo "release_test: $pass geçti, $fail başarısız"
[[ $fail -eq 0 ]]
