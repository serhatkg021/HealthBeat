#!/usr/bin/env bash
# init.sh testleri. Ana makineye hiçbir şey kurulmaz: imaj deploy/init'ten derlenir, her senaryo atılabilir bir
# dizini container'a bağlayıp init.sh'ı root olarak (compose'daki gibi) çalıştırır; sonuç ana makinede denetlenir.
# Test sertifikaları imajın kendi openssl'iyle üretilir (süresi dolmuş sertifika için -not_after gerekir).
#
#   ./init_test.sh          (needs: docker)
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

IMG="healthbeat-init:test"
WORK="$(mktemp -d)"
# Dosyalar container'da 10001'e verildiği için temizlik de container'da yapılır.
trap 'docker run --rm -v "$WORK:/w" --entrypoint sh "$IMG" -c "rm -rf /w/*" >/dev/null 2>&1; rm -rf "$WORK"' EXIT
FAILS=0 PASSES=0

pass() { PASSES=$((PASSES + 1)); }
fail() { FAILS=$((FAILS + 1)); echo "FAIL: $*"; }
owner_mode() { stat -c '%u:%g %a' "$1" 2>/dev/null; }
# owner_mode_in: ana makinenin giremediği (750, 10001'e ait) dizinlerin içi container'dan okunur.
owner_mode_in() { docker run --rm -v "$WORK:/w" --entrypoint stat "$IMG" -c '%u:%g %a' "/w/${1#"$WORK"/}" 2>/dev/null; }

echo "building the init image..."
docker build -q -t "$IMG" . >/dev/null || { echo "cannot build the image"; exit 2; }

# Sertifika üretimi (container içinde, /w altında). gen_cert DOSYA_ADI [openssl req ek argümanları]
GEN='gen() { n="$1"; shift; openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -subj /CN=own.example -addext subjectAltName=DNS:own.example -keyout "/w/certs/$n.key" -out "/w/certs/$n.crt" "$@" 2>/dev/null; }'

# run_case AD KURULUM [ORTAM...] -> CASE, OUT, RC. KURULUM, init.sh'tan önce container'da çalışan sh kodudur.
run_case() {
  local name="$1" setup="$2"; shift 2
  CASE="$WORK/$name"
  mkdir -p "$CASE"
  local envs=() e
  for e in "$@"; do envs+=(-e "$e"); done
  OUT="$(docker run --rm -v "$CASE:/w" -e HB_CERTS_DIR=/w/certs -e HB_LOGS_DIR=/w/logs "${envs[@]}" \
    --entrypoint sh "$IMG" -c "set -e; mkdir -p /w/certs; $GEN; ${setup:-:}; exec /usr/local/bin/init.sh" 2>&1)"
  RC=$?
}
expect_ok() { [[ $RC -eq 0 ]] && pass || fail "$1: rc=$RC, output: $OUT"; }
expect_fail() { # expect_fail AÇIKLAMA BEKLENEN_PARÇA
  if [[ $RC -ne 0 && "$OUT" == *"$2"* ]]; then pass; else fail "$1: rc=$RC, output: $OUT"; fi
}
expect_out() { [[ "$OUT" == *"$2"* ]] && pass || fail "$1: output does not contain '$2': $OUT"; }
expect_pair_perms() {
  [[ "$(owner_mode "$CASE/certs/cert.pem")" == "10001:10001 644" ]] && pass || fail "$1: cert.pem is $(owner_mode "$CASE/certs/cert.pem")"
  [[ "$(owner_mode "$CASE/certs/key.pem")" == "10001:10001 640" ]] && pass || fail "$1: key.pem is $(owner_mode "$CASE/certs/key.pem")"
}

# ------------------------------------------------------------------ boş dizin: kendinden imzalı üretilir
run_case fresh 'mkdir -p /w/logs' HB_TLS_HOSTS=hb.example.test,10.0.0.5
expect_ok "fresh install"
expect_out "fresh install reports the generated certificate" "kendinden imzalı sertifika üretildi"
expect_pair_perms "fresh install"
SAN="$(openssl x509 -in "$CASE/certs/cert.pem" -noout -ext subjectAltName 2>/dev/null)"
[[ "$SAN" == *"DNS:hb.example.test"* && "$SAN" == *"IP Address:10.0.0.5"* ]] && pass || fail "self-signed SAN misses HB_TLS_HOSTS: $SAN"
[[ "$(owner_mode "$CASE/logs")" == "10001:10001 750" ]] && pass || fail "logs dir is $(owner_mode "$CASE/logs")"

# Yeniden çalıştırma: var olan çifte dokunulmaz.
BEFORE="$(sha256sum "$CASE/certs/cert.pem")"
OUT="$(docker run --rm -v "$CASE:/w" -e HB_CERTS_DIR=/w/certs -e HB_LOGS_DIR=/w/logs "$IMG" 2>&1)"
RC=$?
expect_ok "second run"
[[ "$(sha256sum "$CASE/certs/cert.pem")" == "$BEFORE" ]] && pass || fail "second run regenerated the certificate"
expect_out "second run checks the existing pair" "sertifika hazır"

# ------------------------------------------------------------------ kendi sertifikan: root'a ait, 600
run_case own 'gen own; mv /w/certs/own.crt /w/certs/cert.pem; mv /w/certs/own.key /w/certs/key.pem; chmod 600 /w/certs/cert.pem /w/certs/key.pem'
expect_ok "own certificate"
expect_pair_perms "own certificate"
openssl x509 -in "$CASE/certs/cert.pem" -noout -subject 2>/dev/null | grep -q own.example && pass || fail "own certificate was replaced"
expect_out "own certificate is reported" "CN=own.example"

# Zincir (sunucu sertifikası + ara sertifika) kabul edilir.
run_case chain 'gen own; gen ca; cat /w/certs/own.crt /w/certs/ca.crt > /w/certs/cert.pem; mv /w/certs/own.key /w/certs/key.pem'
expect_ok "certificate chain"

# ------------------------------------------------------------------ kullanılamayan çiftler: hata, dokunulmaz
run_case only_cert 'gen own; mv /w/certs/own.crt /w/certs/cert.pem'
expect_fail "only cert.pem" "birlikte olmalı"
[[ ! -e "$CASE/certs/key.pem" ]] && pass || fail "a key was generated next to a lone cert.pem"

run_case only_key 'gen own; mv /w/certs/own.key /w/certs/key.pem'
expect_fail "only key.pem" "birlikte olmalı"

run_case empty_key 'gen own; mv /w/certs/own.crt /w/certs/cert.pem; : > /w/certs/key.pem'
expect_fail "empty key.pem" "birlikte olmalı"

run_case mismatch 'gen a; gen b; mv /w/certs/a.crt /w/certs/cert.pem; mv /w/certs/b.key /w/certs/key.pem'
expect_fail "key of another certificate" "anahtarı değil"

run_case leaf_last 'gen own; gen ca; cat /w/certs/ca.crt /w/certs/own.crt > /w/certs/cert.pem; mv /w/certs/own.key /w/certs/key.pem'
expect_fail "chain with the server certificate last" "anahtarı değil"

run_case encrypted 'gen own; mv /w/certs/own.crt /w/certs/cert.pem; openssl pkey -in /w/certs/own.key -aes256 -passout pass:secret -out /w/certs/key.pem'
expect_fail "encrypted key" "şifresiz"

run_case garbage 'gen own; echo "not a certificate" > /w/certs/cert.pem; mv /w/certs/own.key /w/certs/key.pem'
expect_fail "garbage cert.pem" "cert.pem okunamadı"

run_case bad_hosts '' 'HB_TLS_HOSTS=ok.example,bad;host'
expect_fail "invalid HB_TLS_HOSTS" "geçersiz değer"

# ------------------------------------------------------------------ süre: yalnızca uyarı
run_case expired 'gen own -not_before 20200101000000Z -not_after 20200201000000Z; mv /w/certs/own.crt /w/certs/cert.pem; mv /w/certs/own.key /w/certs/key.pem'
expect_ok "expired certificate is a warning"
expect_out "expired certificate warning" "süresi dolmuş"
expect_pair_perms "expired certificate"

run_case expiring 'gen own -days 10; mv /w/certs/own.crt /w/certs/cert.pem; mv /w/certs/own.key /w/certs/key.pem'
expect_ok "expiring certificate"
expect_out "expiring certificate warning" "30 günden az"

# ------------------------------------------------------------------ loglar
run_case logs 'mkdir -p /w/logs; echo x > /w/logs/server-2026-01-01.log; chmod 600 /w/logs/server-2026-01-01.log'
expect_ok "logs with a root-owned file"
[[ "$(owner_mode_in "$CASE/logs/server-2026-01-01.log")" == 10001:10001* ]] && pass || fail "old log file is $(owner_mode_in "$CASE/logs/server-2026-01-01.log")"

run_case no_logs ''
expect_ok "no logs dir mounted"
[[ "$OUT" != *"log dizini"* ]] && pass || fail "reported a logs dir that does not exist: $OUT"

echo "init_test: $PASSES passed, $FAILS failed"
[[ $FAILS -eq 0 ]]
