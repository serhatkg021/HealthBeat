#!/usr/bin/env bash
# Geriye dönük uyumluluk uçtan uca testi (docs/COMPATIBILITY.md): ESKİ agent sürümleri, bu
# çalışma ağacındaki server'a karşı gerçekten metrik gönderebiliyor mu?
#
#   scripts/compat_e2e.sh                                   # yalnızca şimdiki çalışma ağacı (+ gelecek-agent)
#   scripts/compat_e2e.sh --ref v1_0=agent/v1.0.0           # yayımlanmış bir agent etiketini derleyip dene
#   scripts/compat_e2e.sh --binary kurulu=/usr/local/bin/healthbeat-agent
#   scripts/compat_e2e.sh --server-ref server/v1.0.0 --ref yok=   # ESKİ server'a karşı YENİ agent
#
# Her sürüm için: derle (git ref'i geçici bir dizine açılır), agent'ı panel API'siyle oluştur,
# birkaç saniye çalıştır, sonra "çevrimiçi oldu mu, metrik satırı yazıldı mı, agent hata
# günlüğü yok mu" denetler. Server geçici bir şemada (hbcompat_*) çalışır ve sonunda silinir;
# DATABASE_URL (ya da server/.env) asla üretim veritabanına yöneltilmemeli.
#
# Çıkış kodu: hepsi geçerse 0, biri kalırsa 1.
set -euo pipefail
export LC_ALL=C

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Docker container'ları çok olan bir makinede ilk push birkaç saniye sürebilir; pencere bu yüzden geniş.
RUN_SECONDS="${RUN_SECONDS:-15}"

# Sürüm tanımları: ad|tür|değer (tür: ref | binary | tree). "tree" = mevcut çalışma ağacı.
# Varsayılan yalnızca şimdiki ağaçtır; yeni bir sürüm yayımlandıkça önceki etiketler --ref ile eklenir.
VARIANTS=()
CUSTOM=0
SERVER_REF=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref)    VARIANTS+=("${2%%=*}|ref|${2#*=}"); CUSTOM=1; shift 2 ;;
    --binary) VARIANTS+=("${2%%=*}|binary|${2#*=}"); CUSTOM=1; shift 2 ;;
    --server-ref) SERVER_REF="$2"; CUSTOM=1; shift 2 ;;
    -h|--help) sed -n 2,14p "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
VARIANTS+=("simdiki|tree|")

if [[ -z "${DATABASE_URL:-}" && -f "$ROOT/server/.env" ]]; then
  set -a; . "$ROOT/server/.env"; set +a
fi
[[ -n "${DATABASE_URL:-}" ]] || { echo "DATABASE_URL yok (server/.env ya da ortam)" >&2; exit 2; }
command -v psql >/dev/null || { echo "psql gerekli" >&2; exit 2; }

WORK="$(mktemp -d)"
SCHEMA="hbcompat_$$"
SERVER_PID=""
AGENT_PID=""
PASS=0
FAIL=0

cleanup() {
  [[ -n "$AGENT_PID" ]] && kill "$AGENT_PID" 2>/dev/null || true
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  psql "$DATABASE_URL" -qc "DROP SCHEMA IF EXISTS $SCHEMA CASCADE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

if [[ -n "$SERVER_REF" ]]; then
  echo "==> server derleniyor (git ref $SERVER_REF: ESKİ server)"
  mkdir -p "$WORK/src-server"
  git -C "$ROOT" archive "$SERVER_REF" server | tar -x -C "$WORK/src-server"
  (cd "$WORK/src-server/server" && go build -o "$WORK/hb-server" ./cmd/server)
else
  echo "==> server derleniyor (çalışma ağacı)"
  (cd "$ROOT/server" && go build -o "$WORK/hb-server" ./cmd/server)
fi

psql "$DATABASE_URL" -qc "CREATE SCHEMA $SCHEMA"
PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
ADMIN_PW="Compat-$(openssl rand -hex 8)-Aa1"
NEW_PW="Compat-$(openssl rand -hex 8)-Bb2"
(
  cd "$ROOT/server"
  export DATABASE_URL="${DATABASE_URL}&search_path=$SCHEMA" HTTP_ADDR="127.0.0.1:$PORT"
  export BOOTSTRAP_ADMIN_EMAIL=compat@healthbeat.local BOOTSTRAP_ADMIN_PASSWORD="$ADMIN_PW"
  exec "$WORK/hb-server" >"$WORK/server.log" 2>&1
) &
SERVER_PID=$!
for _ in $(seq 1 40); do
  curl -sk "https://127.0.0.1:$PORT/api/v1/me" -o /dev/null 2>/dev/null && break
  kill -0 "$SERVER_PID" 2>/dev/null || { echo "server başlamadı:"; tail -20 "$WORK/server.log"; exit 1; }
  sleep 0.25
done

API="https://127.0.0.1:$PORT/api/v1"
api() { curl -sk -H 'Content-Type: application/json' "$@"; }
TOKEN="$(api -X POST "$API/auth/login" -d "{\"email\":\"compat@healthbeat.local\",\"password\":\"$ADMIN_PW\"}" | json "d['access_token']")"
TOKEN="$(api -X POST "$API/me/password" -H "Authorization: Bearer $TOKEN" \
  -d "{\"current_password\":\"$ADMIN_PW\",\"new_password\":\"$NEW_PW\"}" | json "d['access_token']")"
ORG="$(api -X POST "$API/organizations" -H "Authorization: Bearer $TOKEN" -d '{"name":"compat"}' | json "d['id']")"

check() { # ad, koşul-açıklaması, sonuç(0/1)
  if [[ "$3" -eq 0 ]]; then echo "     ok    $1: $2"; else echo "     FAIL  $1: $2"; return 1; fi
}

run_variant() {
  local name="$1" kind="$2" value="$3" bin=""
  echo "==> $name ($kind ${value:-çalışma ağacı})"
  case "$kind" in
    ref)
      mkdir -p "$WORK/src-$name"
      git -C "$ROOT" archive "$value" agent | tar -x -C "$WORK/src-$name"
      (cd "$WORK/src-$name/agent" && go build -o "$WORK/agent-$name" ./cmd/agent) || { echo "     FAIL  derlenemedi"; return 1; }
      bin="$WORK/agent-$name" ;;
    tree)
      (cd "$ROOT/agent" && go build -o "$WORK/agent-$name" ./cmd/agent)
      bin="$WORK/agent-$name" ;;
    binary)
      [[ -x "$value" ]] || { echo "     FAIL  binary bulunamadı: $value"; return 1; }
      cp "$value" "$WORK/agent-$name"; bin="$WORK/agent-$name" ;;
  esac

  local created id tok
  created="$(api -X POST "$API/hosts" -H "Authorization: Bearer $TOKEN" \
    -d "{\"organization_id\":\"$ORG\",\"title\":\"compat-$name\",\"ip\":\"127.0.0.1\",\"mode\":\"push\",\"interval_seconds\":10}")"
  id="$(echo "$created" | json "d['id']")"; tok="$(echo "$created" | json "d['api_token']")"
  python3 - "$WORK/cfg-$name.json" "$id" "$tok" "$PORT" <<'EOF'
import json, sys
path, cid, tok, port = sys.argv[1:5]
json.dump({"mode": "push", "server_url": "https://127.0.0.1:" + port, "host_id": cid, "api_token": tok,
           "interval_seconds": 10, "insecure_skip_verify": True, "disk_mounts": ["/"]}, open(path, "w"))
EOF
  chmod 600 "$WORK/cfg-$name.json"

  "$bin" -config "$WORK/cfg-$name.json" >"$WORK/log-$name.txt" 2>&1 &
  AGENT_PID=$!
  sleep "$RUN_SECONDS"
  kill "$AGENT_PID" 2>/dev/null || true; wait "$AGENT_PID" 2>/dev/null || true; AGENT_PID=""

  local got rc=0 metrics
  got="$(api "$API/hosts/$id" -H "Authorization: Bearer $TOKEN")"
  metrics="$(psql "$DATABASE_URL" -Atc "SELECT count(*) FROM $SCHEMA.metrics WHERE host_id = '$id'")"

  check "$name" "durum çevrimiçi" "$([[ "$(echo "$got" | json "d['status']")" == online ]]; echo $?)" || rc=1
  check "$name" "metrik satırı yazıldı ($metrics)" "$([[ "$metrics" -ge 1 ]]; echo $?)" || rc=1
  check "$name" "agent günlüğünde push hatası yok (kendi kapatmamdan gelen context canceled hariç)" "$(grep 'push metrics:' "$WORK/log-$name.txt" | grep -qv 'context canceled'; [[ $? -ne 0 ]]; echo $?)" || rc=1
  # Sürüm tutarlılığı: sürüm bildirmeyen agent protokol 1 sayılır; bildiren en az 2. Çalışma
  # ağacındaki agent, kendi kaynağındaki sürümü bildirmeli.
  local av ap want_ver
  av="$(echo "$got" | json "d.get('agent_version') or ''")"; ap="$(echo "$got" | json "d.get('agent_protocol') or 0")"
  if [[ -n "$SERVER_REF" ]]; then
    # ESKİ server: sürüm alanlarını hiç bilmez. Yeni agent tam payload'ı 400 ile reddedilince
    # çekirdek metriklere düşmeli (Compat) ve metrikler yine de yazılmalı.
    if [[ "$kind" == tree ]]; then
      check "$name" "eski server'da çekirdek metriklere düştü (agent günlüğü)" "$(grep -q 'core metrics only' "$WORK/log-$name.txt"; echo $?)" || rc=1
    fi
  elif [[ -z "$av" ]]; then
    check "$name" "sürüm bildirmiyor => protokol 1 (agent_protocol=$ap)" "$([[ "$ap" -eq 1 ]]; echo $?)" || rc=1
  else
    check "$name" "sürüm bildiriyor => protokol >= 2 (agent_version=$av, agent_protocol=$ap)" "$([[ "$ap" -ge 2 ]]; echo $?)" || rc=1
  fi
  if [[ "$kind" == tree && -z "$SERVER_REF" ]]; then
    want_ver="$(sed -n 's/^var Version = "\(.*\)"$/\1/p' "$ROOT/agent/internal/version/version.go")"
    check "$name" "bildirilen sürüm kaynakla aynı ($want_ver)" "$([[ "$av" == "$want_ver" ]]; echo $?)" || rc=1
  fi
  if [[ "$kind" == tree && -z "$SERVER_REF" ]]; then
    hk="$(echo "$got" | json "((d.get('host_info') or {}).get('kernel') or {}).get('release') or ''")"
    check "$name" "makine envanteri (host_info) alındı (kernel: ${hk:-yok})" "$([[ -n "$hk" ]]; echo $?)" || rc=1
  fi
  echo "$got" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print('     bilgi  cpu_cores=%s ram_total_mb=%s physical_disks=%s agent_version=%s agent_protocol=%s unsupported_fields=%s os=%s' % (tuple(
    (d.get(k) if k != 'physical_disks' else len(d.get(k) or [])) for k in
    ('cpu_cores', 'ram_total_mb', 'physical_disks', 'agent_version', 'agent_protocol', 'unsupported_fields')) + (((d.get('host_info') or {}).get('os') or {}).get('pretty_name'),)))"
  if [[ $rc -ne 0 ]]; then echo "     --- agent günlüğü:"; sed 's/^/     | /' "$WORK/log-$name.txt" | tail -8; fi
  return $rc
}

for v in "${VARIANTS[@]}"; do
  IFS='|' read -r n k val <<<"$v"
  [[ "$k" == ref && -z "$val" ]] && continue  # "--ref ad=" yalnızca varsayılanları bastırır
  if run_variant "$n" "$k" "$val"; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi
done

# Gelecekteki bir agent'ın (bu server'ın tanımadığı alanlar) gövdesi: reddedilmemeli, ama tanımadığı
# alanlar panelde "server güncellenmeli" diye görünebilsin diye kaydedilmeli.
if [[ -z "$SERVER_REF" ]]; then
  echo "==> gelecek-agent (testdata/payloads/v99_future.json, çalışma ağacındaki server'a)"
  fut="$(api -X POST "$API/hosts" -H "Authorization: Bearer $TOKEN" \
    -d "{\"organization_id\":\"$ORG\",\"title\":\"compat-future\",\"ip\":\"127.0.0.1\",\"mode\":\"push\",\"interval_seconds\":10}")"
  fid="$(echo "$fut" | json "d['id']")"; ftok="$(echo "$fut" | json "d['api_token']")"
  code="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "$API/metrics" -H "Authorization: Bearer $ftok" -H "X-Host-ID: $fid" \
    -H 'Content-Type: application/json' -H 'X-HealthBeat-Protocol: 99' -H 'User-Agent: healthbeat-agent/9.9.9' \
    --data-binary "@$ROOT/server/testdata/payloads/v99_future.json")"
  fgot="$(api "$API/hosts/$fid" -H "Authorization: Bearer $TOKEN")"
  rc=0
  check gelecek-agent "bilinmeyen alanlı gövde kabul edildi (HTTP $code)" "$([[ "$code" == 204 ]]; echo $?)" || rc=1
  check gelecek-agent "bilinmeyen alan adları kaydedildi" \
    "$([[ "$(echo "$fgot" | json "','.join(d.get('unsupported_fields') or [])")" == "agent_notes,gpu,load_average" ]]; echo $?)" || rc=1
  check gelecek-agent "sürüm/protokol kaydedildi (9.9.9 / 99)" \
    "$([[ "$(echo "$fgot" | json "str(d.get('agent_version'))+'/'+str(d.get('agent_protocol'))")" == "9.9.9/99" ]]; echo $?)" || rc=1
  if [[ $rc -eq 0 ]]; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi
fi

echo
echo "sonuç: $PASS geçti, $FAIL kaldı"
[[ $FAIL -eq 0 ]]
