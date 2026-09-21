#!/usr/bin/env bash
# install.sh testleri. Ana makineye hiçbir şey kurulmaz: her çalıştırma atılabilir bir
# dizinle --root kullanır. Üretilen yapılandırma sonra GERÇEK agent binary'sine
# (../cmd/agent'tan derlenir) verilir; onu kabul etmeli ve pull modunda üretilen
# sertifikayla gerçekten TLS üzerinden servis vermelidir.
#
#   ./install_test.sh          (needs: go, openssl, curl, python3)
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

WORK="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$WORK"' EXIT
FAILS=0 PASSES=0

pass() { PASSES=$((PASSES + 1)); }
fail() { FAILS=$((FAILS + 1)); echo "FAIL: $*"; }
check() { local desc="$1"; shift; if "$@" >/dev/null 2>&1; then pass; else fail "$desc"; fi; }
mode_of() { stat -c '%a' "$1"; }

echo "building the agent..."
( cd .. && go build -o "$WORK/healthbeat-agent" ./cmd/agent ) || { echo "cannot build the agent"; exit 2; }
BIN="$WORK/healthbeat-agent"

TOKEN="AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"
SECRET="Zy9XwVuTsRqPoNmLkJiHgFeDcBa_-0987654321zzzz"
UUID="123e4567-e89b-12d3-a456-426614174000"

# run_install ARGS... -> OUT'u (stdout+stderr) ve RC'yi ayarlar; ROOT önceden ayarlanmadıkça her çağrı taze bir kök alır.
new_root() { ROOT="$WORK/root.$RANDOM"; mkdir -p "$ROOT"; }
run_install() {
  OUT="$(./install.sh install --root "$ROOT" --binary "$BIN" --no-start "$@" </dev/null 2>&1)"
  RC=$?
}
expect_rejected() { # expect_rejected "açıklama" "beklenen mesaj parçası" ARGS...
  local desc="$1" frag="$2"; shift 2
  new_root; run_install "$@"
  if [[ $RC -ne 0 && "$OUT" == *"$frag"* && ! -e "$ROOT/etc/healthbeat/agent.json" ]]; then pass
  else fail "$desc (rc=$RC, config written: $([[ -e $ROOT/etc/healthbeat/agent.json ]] && echo yes || echo no), output: $OUT)"; fi
}

# ------------------------------------------------------------------ push: sorunsuz yol
new_root
HEALTHBEAT_API_TOKEN="$TOKEN" run_install --mode push --server-url https://api.example.com/ --host-id "$UUID" --interval 15 --disk-mounts /,/data --insecure-skip-verify
[[ $RC -eq 0 ]] && pass || fail "push install failed: $OUT"
CONF="$ROOT/etc/healthbeat/agent.json"
check "config is valid JSON" python3 -c "import json,sys; json.load(open('$CONF'))"
check "config has the expected fields" python3 - <<PY
import json
c = json.load(open("$CONF"))
assert c["mode"] == "push" and c["server_url"] == "https://api.example.com"  # sondaki eğik çizgi kırpıldı
assert c["host_id"] == "$UUID" and c["api_token"] == "$TOKEN" and c["interval_seconds"] == 15
assert c["disk_mounts"] == ["/", "/data"] and c["insecure_skip_verify"] is True
PY
[[ "$(mode_of "$CONF")" == 640 ]] && pass || fail "config mode is $(mode_of "$CONF"), want 640 (it holds a credential)"
[[ "$(mode_of "$ROOT/etc/healthbeat")" == 750 ]] && pass || fail "config dir mode is $(mode_of "$ROOT/etc/healthbeat"), want 750"
[[ "$(mode_of "$ROOT/usr/local/bin/healthbeat-agent")" == 755 ]] && pass || fail "binary mode"
[[ "$(mode_of "$ROOT/etc/systemd/system/healthbeat-agent.service")" == 644 ]] && pass || fail "unit mode"
[[ "$OUT" != *"$TOKEN"* ]] && pass || fail "the API token was printed by the installer"
[[ ! -e "$ROOT/etc/systemd/system/healthbeat-agent.service.d" ]] && pass || fail "docker drop-in present without --docker"

# Gerçek agent bu yapılandırmayı kabul etmeli ve çalışmaya devam etmeli (yalnızca server'a ulaşamayacak).
timeout 2 "$BIN" -config "$CONF" >"$WORK/push.log" 2>&1; rc=$?
[[ $rc -eq 124 ]] && pass || fail "the real agent rejected the generated push config (rc=$rc): $(head -3 "$WORK/push.log")"

# ------------------------------------------------------------------ girdiler ve seçenek biçimleri
new_root; printf '%s\n' "$TOKEN" >"$WORK/token"
run_install --mode push --server-url https://h.example.com:8443 --host-id "$UUID" --token-file "$WORK/token"
[[ $RC -eq 0 ]] && grep -q "\"$TOKEN\"" "$ROOT/etc/healthbeat/agent.json" && pass || fail "--token-file: $OUT"
python3 -c "import json;c=json.load(open('$ROOT/etc/healthbeat/agent.json'));assert c['interval_seconds']==30 and c['disk_mounts']==['/'] and c['insecure_skip_verify'] is False" && pass || fail "defaults (interval 30, mount /, verify on)"

new_root; run_install --mode push --server-url https://h.example.com --host-id "$UUID" --docker <<<"" 2>/dev/null
HEALTHBEAT_API_TOKEN="$TOKEN" run_install --mode push --server-url https://h.example.com --host-id "$UUID" --docker
[[ $RC -ne 0 && "$OUT" == *"no 'docker' group"* || -f "$ROOT/etc/systemd/system/healthbeat-agent.service.d/docker.conf" ]] && pass || fail "--docker: $OUT"

# disk_mounts: "auto" (her gerçek dosya sistemi) tek başına ve açık yollarla karışık; gerçek agent bunları kabul eder
for mounts in auto /,auto; do
  new_root; HEALTHBEAT_API_TOKEN="$TOKEN" run_install --mode push --server-url https://a.example.com --host-id "$UUID" --disk-mounts "$mounts"
  exp="$(python3 -c "print(repr('$mounts'.split(',')))")"
  python3 -c "import json;assert json.load(open('$ROOT/etc/healthbeat/agent.json'))['disk_mounts']==$exp" && pass || fail "--disk-mounts $mounts not written as $exp: $OUT"
  timeout 2 "$BIN" -config "$ROOT/etc/healthbeat/agent.json" >"$WORK/auto.log" 2>&1; rc=$?
  [[ $rc -eq 124 ]] && pass || fail "the real agent rejected disk_mounts $mounts (rc=$rc): $(head -2 "$WORK/auto.log")"
done

# ------------------------------------------------------------------ reddedilenler
export HEALTHBEAT_API_TOKEN="$TOKEN"
expect_rejected "plain http server url"      "invalid --server-url"  --mode push --server-url http://api.example.com --host-id "$UUID"
expect_rejected "url with a path"            "invalid --server-url"  --mode push --server-url https://api.example.com/v1 --host-id "$UUID"
expect_rejected "url injecting JSON"         "invalid --server-url"  --mode push --server-url 'https://a.com","x":"' --host-id "$UUID"
expect_rejected "malformed host id"        "invalid --host-id"   --mode push --server-url https://a.example.com --host-id not-a-uuid
expect_rejected "interval 0"                 "--interval"            --mode push --server-url https://a.example.com --host-id "$UUID" --interval 0
expect_rejected "interval text"              "--interval"            --mode push --server-url https://a.example.com --host-id "$UUID" --interval fast
expect_rejected "mount with a quote"         "invalid disk mount"    --mode push --server-url https://a.example.com --host-id "$UUID" --disk-mounts '/data"x'
expect_rejected "relative mount"             "invalid disk mount"    --mode push --server-url https://a.example.com --host-id "$UUID" --disk-mounts data
expect_rejected "AUTO in capitals is not auto" "invalid disk mount"    --mode push --server-url https://a.example.com --host-id "$UUID" --disk-mounts AUTO
expect_rejected "invalid mode"               "--mode must be"        --mode carrier-pigeon
expect_rejected "unknown option"             "unknown option"        --mode push --frobnicate
expect_rejected "option without value"       "needs a value"         --mode push --server-url
HEALTHBEAT_API_TOKEN='short' expect_rejected "short token"   "invalid API token"  --mode push --server-url https://a.example.com --host-id "$UUID"
HEALTHBEAT_API_TOKEN='aaaaaaaaaaaaaaaaaaaaaaaa"; rm -rf /; "' expect_rejected "token with shell/JSON metacharacters" "invalid API token" --mode push --server-url https://a.example.com --host-id "$UUID"
( unset HEALTHBEAT_API_TOKEN; expect_rejected "no token, not interactive" "API token missing" --mode push --server-url https://a.example.com --host-id "$UUID" )
new_root; OUT="$(./install.sh install --root "$ROOT" --binary "$WORK/nope" --mode push --server-url https://a.example.com --host-id "$UUID" </dev/null 2>&1)"; RC=$?
[[ $RC -ne 0 && "$OUT" == *"binary not found"* ]] && pass || fail "missing binary: $OUT"
OUT="$(./install.sh install --root "$WORK" 2>&1 </dev/null)"; [[ "$OUT" == *"Mode"* || "$OUT" == *"missing"* ]] && pass || fail "no mode, non-interactive: $OUT"
OUT="$(./install.sh explode 2>&1)"; [[ "$OUT" == *"unknown action"* ]] && pass || fail "unknown action: $OUT"
[[ "$(id -u)" -eq 0 ]] || { OUT="$(./install.sh install --mode push 2>&1 </dev/null)"; [[ "$OUT" == *"must run as root"* ]] && pass || fail "non-root without --root should refuse: $OUT"; }

# mevcut yapılandırma: reddet, sonra --force ile yedekle
new_root; run_install --mode push --server-url https://a.example.com --host-id "$UUID"
first_ok=$RC
run_install --mode push --server-url https://b.example.com --host-id "$UUID"
[[ $first_ok -eq 0 && $RC -ne 0 && "$OUT" == *"already exists"* ]] && grep -q a.example.com "$ROOT/etc/healthbeat/agent.json" && pass || fail "existing config must not be overwritten silently: $OUT"
run_install --mode push --server-url https://b.example.com --host-id "$UUID" --force
[[ $RC -eq 0 ]] && grep -q b.example.com "$ROOT/etc/healthbeat/agent.json" && ls "$ROOT"/etc/healthbeat/agent.json.bak.* >/dev/null 2>&1 && pass || fail "--force should replace the config and keep a backup: $OUT"

# ------------------------------------------------------------------ pull: üretilen sertifika + GERÇEK agent TLS üzerinden
PORT=$((20000 + RANDOM % 20000))
new_root
HEALTHBEAT_PULL_SECRET="$SECRET" run_install --mode pull --listen "127.0.0.1:$PORT" --allowed-ips 127.0.0.1,::1,10.0.0.0/8
[[ $RC -eq 0 ]] && pass || fail "pull install failed: $OUT"
PCONF="$ROOT/etc/healthbeat/agent.json"
python3 - <<PY && pass || fail "pull config contents"
import json
c = json.load(open("$PCONF"))
assert c["mode"] == "pull" and c["listen_addr"] == "127.0.0.1:$PORT" and c["pull_endpoint"] == "/api/v1/status"
assert c["pull_secret"] == "$SECRET" and c["allowed_server_ips"] == ["127.0.0.1", "::1", "10.0.0.0/8"]
assert c["tls_cert_file"] == "/etc/healthbeat/tls/cert.pem" and c["tls_key_file"] == "/etc/healthbeat/tls/key.pem"
PY
[[ "$(mode_of "$ROOT/etc/healthbeat/tls/key.pem")" == 640 ]] && pass || fail "private key mode is $(mode_of "$ROOT/etc/healthbeat/tls/key.pem"), want 640"
check "generated certificate parses" openssl x509 -in "$ROOT/etc/healthbeat/tls/cert.pem" -noout
[[ "$OUT" != *"$SECRET"* ]] && pass || fail "the pull secret was printed by the installer"

# Yapılandırma gerçek /etc yollarına işaret eder; test çalıştırması için onları hazırlık köküne yönlendir.
sed "s#/etc/healthbeat#$ROOT/etc/healthbeat#g" "$PCONF" >"$WORK/pull-test.json"
"$BIN" -config "$WORK/pull-test.json" >"$WORK/pull.log" 2>&1 &
AGENT=$!
up=0; for _ in $(seq 1 50); do curl -sk -o /dev/null "https://127.0.0.1:$PORT/api/v1/status" 2>/dev/null && { up=1; break; }; sleep 0.1; done
[[ $up -eq 1 ]] && pass || fail "the real agent did not come up with the generated config/certificate: $(head -3 "$WORK/pull.log")"
code=$(curl -sk -o "$WORK/status.json" -w '%{http_code}' -H "Authorization: Bearer $SECRET" "https://127.0.0.1:$PORT/api/v1/status")
[[ "$code" == 200 ]] && python3 -c "import json;d=json.load(open('$WORK/status.json'));assert 0<=d['cpu_usage_pct']<=100 and 'disk' in d" && pass || fail "status with the right secret: HTTP $code"
code=$(curl -sk -o /dev/null -w '%{http_code}' -H "Authorization: Bearer wrong-wrong-wrong-wrong" "https://127.0.0.1:$PORT/api/v1/status")
[[ "$code" == 401 ]] && pass || fail "wrong secret should be 401, got $code"
# Go, TLS portuna gelen düz HTTP'ye istemciye HTTPS kullanmasını söyleyen bir 400 ile yanıt verir; önemli olan hiçbir metriğin dönmemesidir.
code=$(curl -s -o "$WORK/plain.txt" -w '%{http_code}' --max-time 2 -H "Authorization: Bearer $SECRET" "http://127.0.0.1:$PORT/api/v1/status")
[[ "$code" != 200 ]] && ! grep -q cpu_usage_pct "$WORK/plain.txt" && pass || fail "metrics were served over plain HTTP (HTTP $code)"
kill "$AGENT" 2>/dev/null; wait "$AGENT" 2>/dev/null

# pull ret durumları ve özel sertifika
unset HEALTHBEAT_API_TOKEN; export HEALTHBEAT_PULL_SECRET="$SECRET"
expect_rejected "pull without allowed ips"   "missing"          --mode pull --listen 127.0.0.1:9443
expect_rejected "privileged port"            "1024"             --mode pull --allowed-ips 10.0.0.1 --listen 0.0.0.0:443
expect_rejected "garbage allowed ip"         "allowed IP"       --mode pull --allowed-ips '10.0.0.1"x'
expect_rejected "bad listen"                 "host:port"        --mode pull --allowed-ips 10.0.0.1 --listen 9443
expect_rejected "bad endpoint"               "invalid --endpoint" --mode pull --allowed-ips 10.0.0.1 --endpoint 'status?x'
expect_rejected "cert without key"           "readable files"   --mode pull --allowed-ips 10.0.0.1 --tls-cert "$WORK/none.pem"
HEALTHBEAT_PULL_SECRET="tooshort" expect_rejected "short pull secret" "invalid pull secret" --mode pull --allowed-ips 10.0.0.1

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 -subj /CN=mine -keyout "$WORK/my.key" -out "$WORK/my.crt" 2>/dev/null
new_root; run_install --mode pull --allowed-ips 10.0.0.1 --tls-cert "$WORK/my.crt" --tls-key "$WORK/my.key"
cmp -s "$WORK/my.crt" "$ROOT/etc/healthbeat/tls/cert.pem" && cmp -s "$WORK/my.key" "$ROOT/etc/healthbeat/tls/key.pem" && [[ "$(mode_of "$ROOT/etc/healthbeat/tls/key.pem")" == 640 ]] && pass || fail "supplied certificate was not installed as-is with a private key mode: $OUT"

# ------------------------------------------------------------------ özel CA ile push (gerçek agent, gerçek TLS server'ı)
new_root
run_install_env() { HEALTHBEAT_API_TOKEN="$TOKEN" run_install "$@"; }
unset HEALTHBEAT_PULL_SECRET; export HEALTHBEAT_API_TOKEN="$TOKEN"

# openssl ile yapılmış, 127.0.0.1 için geçerli bir CA ve server sertifikası.
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 -subj /CN=Test-CA -keyout "$WORK/ca.key" -out "$WORK/ca.crt" 2>/dev/null
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -subj /CN=server -keyout "$WORK/srv.key" -out "$WORK/srv.csr" 2>/dev/null
openssl x509 -req -in "$WORK/srv.csr" -CA "$WORK/ca.crt" -CAkey "$WORK/ca.key" -CAcreateserial -days 1 -out "$WORK/srv.crt" \
  -extfile <(printf 'subjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n') 2>/dev/null

CAPORT=$((20000 + RANDOM % 20000))
cat >"$WORK/tls_server.py" <<PY
import http.server, ssl, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        open("$WORK/pushes.log", "a").write("%s %s\n" % (self.path, self.headers.get("Authorization")))
        self.send_response(204); self.end_headers()
    def log_message(self, *a): pass
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain("$WORK/srv.crt", "$WORK/srv.key")
srv = http.server.HTTPServer(("127.0.0.1", $CAPORT), H); srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
PY
python3 "$WORK/tls_server.py" >/dev/null 2>&1 &
TLSSRV=$!
for _ in $(seq 1 50); do (exec 3<>/dev/tcp/127.0.0.1/$CAPORT) 2>/dev/null && break; sleep 0.1; done

run_install --mode push --server-url "https://127.0.0.1:$CAPORT" --host-id "$UUID" --interval 1 --ca-cert "$WORK/ca.crt"
[[ $RC -eq 0 ]] && pass || fail "--ca-cert install failed: $OUT"
CACONF="$ROOT/etc/healthbeat/agent.json"
python3 -c "import json;c=json.load(open('$CACONF'));assert c['ca_cert_file']=='/etc/healthbeat/ca.pem' and c['insecure_skip_verify'] is False" && pass || fail "config does not reference the CA file"
cmp -s "$WORK/ca.crt" "$ROOT/etc/healthbeat/ca.pem" && [[ "$(mode_of "$ROOT/etc/healthbeat/ca.pem")" == 644 ]] && pass || fail "CA certificate not installed as-is (mode $(mode_of "$ROOT/etc/healthbeat/ca.pem" 2>/dev/null))"

# Kurucunun yapılandırdığı gerçek agent, CA imzalı server'a doğrulama AÇIKKEN push eder.
sed "s#/etc/healthbeat#$ROOT/etc/healthbeat#g" "$CACONF" >"$WORK/ca-test.json"
rm -f "$WORK/pushes.log"; timeout 3 "$BIN" -config "$WORK/ca-test.json" >"$WORK/ca-agent.log" 2>&1
grep -q "Bearer $TOKEN" "$WORK/pushes.log" 2>/dev/null && pass || fail "the agent did not push to a server whose certificate is signed by the installed CA: $(head -3 "$WORK/ca-agent.log")"

# Aynı server, ama agent'a CA verilmez: doğrulama başarısız olmalı ve hiçbir şey (token dahil) gönderilmemeli.
python3 - <<PY
import json
c = json.load(open("$WORK/ca-test.json")); c.pop("ca_cert_file"); json.dump(c, open("$WORK/noca-test.json", "w"))
PY
rm -f "$WORK/pushes.log"; timeout 3 "$BIN" -config "$WORK/noca-test.json" >"$WORK/noca-agent.log" 2>&1
[[ ! -s "$WORK/pushes.log" ]] && grep -qi "certificate\|x509" "$WORK/noca-agent.log" && pass || fail "without the CA the agent should refuse the server (pushes: $(cat "$WORK/pushes.log" 2>/dev/null); log: $(head -2 "$WORK/noca-agent.log"))"
kill "$TLSSRV" 2>/dev/null

expect_rejected "ca-cert with insecure-skip-verify" "contradict"   --mode push --server-url https://a.example.com --host-id "$UUID" --ca-cert "$WORK/ca.crt" --insecure-skip-verify
expect_rejected "ca-cert in pull mode"              "only applies to push" --mode pull --allowed-ips 10.0.0.1 --ca-cert "$WORK/ca.crt"
expect_rejected "unreadable ca-cert"                "cannot read --ca-cert" --mode push --server-url https://a.example.com --host-id "$UUID" --ca-cert "$WORK/none.crt"
printf 'not a certificate\n' >"$WORK/junk.crt"
expect_rejected "ca-cert that is not a certificate" "does not contain a PEM certificate" --mode push --server-url https://a.example.com --host-id "$UUID" --ca-cert "$WORK/junk.crt"
unset HEALTHBEAT_API_TOKEN
export HEALTHBEAT_PULL_SECRET="$SECRET" # aşağıdaki bölümlerin ona yeniden ihtiyacı var

# ------------------------------------------------------------------ kaldırma
new_root; run_install --mode pull --allowed-ips 10.0.0.1 >/dev/null
./install.sh uninstall --root "$ROOT" >/dev/null 2>&1
[[ ! -e "$ROOT/usr/local/bin/healthbeat-agent" && ! -e "$ROOT/etc/systemd/system/healthbeat-agent.service" && -f "$ROOT/etc/healthbeat/agent.json" ]] && pass || fail "uninstall should remove the binary and unit but keep the config"
./install.sh uninstall --root "$ROOT" --purge >/dev/null 2>&1
[[ ! -e "$ROOT/etc/healthbeat" ]] && pass || fail "--purge should delete the configuration and certificates"

# ------------------------------------------------------------------ systemd unit'in kendisi
if command -v systemd-analyze >/dev/null; then
  cp healthbeat-agent.service "$WORK/unit.service"
  sed -i "s#^ExecStart=/usr/local/bin/healthbeat-agent#ExecStart=$BIN#; s#^User=.*#User=root#; s#^Group=.*#Group=root#" "$WORK/unit.service"
  out="$(systemd-analyze verify "$WORK/unit.service" 2>&1)"
  [[ -z "$out" ]] && pass || fail "systemd-analyze verify complained: $out"
fi

# ------------------------------------------------------------------ --print-inventory
# Operatör "bu agent ne raporlayacak?" diye bakabilir; yapılandırma gerekmez ve çıktı geçerli JSON olmalı.
inv="$("$BIN" --print-inventory 2>/dev/null)"; rc=$?
[[ $rc -eq 0 ]] && pass || fail "--print-inventory failed (rc=$rc)"
python3 - "$inv" <<'PY' && pass || fail "--print-inventory did not print a sane inventory"
import json, sys
d = json.loads(sys.argv[1])
assert d["kernel"]["release"] and d["uptime_seconds"] > 0 and len(d["load_avg"]) == 3
# gizli/yetkili hiçbir alan yok
text = sys.argv[1].lower()
for banned in ('"serial', '"uuid', '"mac"', 'password', 'token'):
    assert banned not in text, banned
PY

# ------------------------------------------------------------------ yükseltme ve geri alma
# Sürümleri ayırt etmek için aynı kaynaktan farklı Version ile derlenmiş binary'ler.
build_version() { ( cd .. && go build -ldflags "-X healthbeat-agent/internal/version.Version=$1" -o "$2" ./cmd/agent ); }
BIN_OLD="$WORK/hb-0.9.0"
build_version 0.9.0 "$BIN_OLD" || { echo "cannot build the older agent"; exit 2; }
BIN_BROKEN="$WORK/hb-broken"; printf '#!/bin/sh\nexit 1\n' >"$BIN_BROKEN"; chmod +x "$BIN_BROKEN"

ver_of() { "$1" --version 2>/dev/null | awk 'NR==1{print $2}'; }
sha_of() { sha256sum "$1" | cut -d' ' -f1; }
INSTALLED() { echo "$ROOT/usr/local/bin/healthbeat-agent"; }
# fresh_install BINARY: BINARY kurulu, push modu, taze kök.
fresh_install() {
  new_root
  HEALTHBEAT_API_TOKEN="$TOKEN" run_install --binary "$1" --mode push --server-url https://api.example.com --host-id "$UUID"
  [[ $RC -eq 0 ]] || { fail "setup: install failed: $OUT"; return 1; }
}
run_upgrade() { # run_upgrade YENI_BINARY [seçenekler] -> OUT, RC
  local b="$1"; shift
  OUT="$(./install.sh upgrade --root "$ROOT" --binary "$b" "$@" </dev/null 2>&1)"; RC=$?
}
run_rollback() { OUT="$(./install.sh rollback --root "$ROOT" "$@" </dev/null 2>&1)"; RC=$?; }
snapshot_conf() { sha_of "$ROOT/etc/healthbeat/agent.json"; }

# --- kurulum yokken
new_root; run_upgrade "$BIN"
[[ $RC -ne 0 && "$OUT" == *"no installation found"* ]] && pass || fail "upgrade without an install should be refused (rc=$RC): $OUT"

# --- mutlu yol: 0.9.0 -> güncel; yapılandırma ve unit'e dokunulmaz, eski binary .prev olur
fresh_install "$BIN_OLD"
conf_before="$(snapshot_conf)"; unit_before="$(sha_of "$ROOT/etc/systemd/system/healthbeat-agent.service")"; confmode="$(mode_of "$ROOT/etc/healthbeat/agent.json")"
run_upgrade "$BIN"
[[ $RC -eq 0 ]] && pass || fail "upgrade failed: $OUT"
[[ "$(ver_of "$(INSTALLED)")" == "$(ver_of "$BIN")" ]] && pass || fail "installed binary is $(ver_of "$(INSTALLED)"), want $(ver_of "$BIN")"
[[ "$(ver_of "$(INSTALLED).prev")" == 0.9.0 ]] && pass || fail "previous binary not kept as .prev (got '$(ver_of "$(INSTALLED).prev")')"
[[ "$(snapshot_conf)" == "$conf_before" && "$(mode_of "$ROOT/etc/healthbeat/agent.json")" == "$confmode" ]] && pass || fail "upgrade touched the config"
[[ "$(sha_of "$ROOT/etc/systemd/system/healthbeat-agent.service")" == "$unit_before" ]] && pass || fail "upgrade touched the unit"
[[ "$(mode_of "$(INSTALLED)")" == 755 && "$(mode_of "$(INSTALLED).prev")" == 755 ]] && pass || fail "binary modes"
[[ -z "$(ls "$ROOT"/usr/local/bin | grep -E '\.(new|swap)$')" ]] && pass || fail "temporary files left behind: $(ls "$ROOT"/usr/local/bin)"
[[ "$OUT" == *"0.9.0 -> $(ver_of "$BIN")"* && "$OUT" == *"rollback"* && "$OUT" != *"$TOKEN"* ]] && pass || fail "unexpected upgrade output: $OUT"

# --- aynı sürüm: bir şey yapma, .prev'i koru
run_upgrade "$BIN"
[[ $RC -eq 0 && "$OUT" == *"already up to date"* && "$(ver_of "$(INSTALLED).prev")" == 0.9.0 ]] && pass || fail "same-version upgrade should be a no-op (rc=$RC): $OUT"

# --- düşürme reddedilir; --allow-downgrade ile olur
run_upgrade "$BIN_OLD"
[[ $RC -ne 0 && "$OUT" == *"refusing to downgrade"* && "$(ver_of "$(INSTALLED)")" == "$(ver_of "$BIN")" ]] && pass || fail "downgrade should be refused and change nothing (rc=$RC): $OUT"
run_upgrade "$BIN_OLD" --allow-downgrade
[[ $RC -eq 0 && "$(ver_of "$(INSTALLED)")" == 0.9.0 ]] && pass || fail "--allow-downgrade should downgrade (rc=$RC): $OUT"

# --- dry-run hiçbir şeyi değiştirmez
fresh_install "$BIN_OLD"; bin_before="$(sha_of "$(INSTALLED)")"
run_upgrade "$BIN" --dry-run
[[ $RC -eq 0 && "$OUT" == *"dry run"* && "$(sha_of "$(INSTALLED)")" == "$bin_before" && ! -e "$(INSTALLED).prev" ]] && pass || fail "--dry-run must not change anything (rc=$RC): $OUT"

# --- yeni binary doğrulanamıyorsa hiçbir şey değişmez
fresh_install "$BIN_OLD"; bin_before="$(sha_of "$(INSTALLED)")"
run_upgrade "$BIN_BROKEN"
[[ $RC -ne 0 && "$OUT" == *"does not report a version"* && "$(sha_of "$(INSTALLED)")" == "$bin_before" && ! -e "$(INSTALLED).prev" ]] && pass || fail "a binary that cannot report its version must be refused (rc=$RC): $OUT"

# --- yeni binary MEVCUT yapılandırmayı kabul etmiyorsa hiçbir şey değişmez
fresh_install "$BIN_OLD"; bin_before="$(sha_of "$(INSTALLED)")"
sed -i 's/"mode": "push"/"mode": "bogus"/' "$ROOT/etc/healthbeat/agent.json"; conf_before="$(snapshot_conf)"
run_upgrade "$BIN"
[[ $RC -ne 0 && "$OUT" == *"EXISTING config"* && "$(sha_of "$(INSTALLED)")" == "$bin_before" && ! -e "$(INSTALLED).prev" && "$(snapshot_conf)" == "$conf_before" ]] && pass || fail "a config the new binary rejects must block the upgrade (rc=$RC): $OUT"

# --- geri alma ve geri almanın geri alınması
fresh_install "$BIN_OLD"; run_upgrade "$BIN" >/dev/null
run_rollback
[[ $RC -eq 0 && "$(ver_of "$(INSTALLED)")" == 0.9.0 && "$(ver_of "$(INSTALLED).prev")" == "$(ver_of "$BIN")" ]] && pass || fail "rollback should swap the binaries (rc=$RC): $OUT"
run_rollback
[[ $RC -eq 0 && "$(ver_of "$(INSTALLED)")" == "$(ver_of "$BIN")" ]] && pass || fail "a second rollback should swap back (rc=$RC): $OUT"
fresh_install "$BIN_OLD"; run_rollback
[[ $RC -ne 0 && "$OUT" == *"nothing to roll back to"* ]] && pass || fail "rollback without a previous binary should be refused (rc=$RC): $OUT"

# --- unit: farklıysa yalnızca haber verilir; --update-unit ile değiştirilir (ve yedeklenir)
fresh_install "$BIN_OLD"; printf '\n# local edit\n' >>"$ROOT/etc/systemd/system/healthbeat-agent.service"; unit_before="$(sha_of "$ROOT/etc/systemd/system/healthbeat-agent.service")"
run_upgrade "$BIN"
[[ $RC -eq 0 && "$OUT" == *"differs"* && "$(sha_of "$ROOT/etc/systemd/system/healthbeat-agent.service")" == "$unit_before" ]] && pass || fail "a locally edited unit must be left alone (rc=$RC): $OUT"
fresh_install "$BIN_OLD"; printf '\n# local edit\n' >>"$ROOT/etc/systemd/system/healthbeat-agent.service"
run_upgrade "$BIN" --update-unit
[[ $RC -eq 0 ]] && cmp -s healthbeat-agent.service "$ROOT/etc/systemd/system/healthbeat-agent.service" && [[ -f "$ROOT/etc/systemd/system/healthbeat-agent.service.prev" ]] && pass || fail "--update-unit should replace the unit and keep a backup (rc=$RC): $OUT"

# --- servis yönetimi: sahte bir systemctl ile yeniden başlatma, doğrulama ve otomatik geri alma
FAKE="$WORK/fake-systemctl"; FS="$WORK/fakestate"
cat >"$FAKE" <<'FAKE_EOF'
#!/usr/bin/env bash
# Test için sahte systemctl. Durum $FAKE_SYS_STATE altında; kurulu binary $FAKE_SYS_BIN.
S="$FAKE_SYS_STATE"; echo "$*" >>"$S/calls"
case "$1" in
  is-active) [[ "$(cat "$S/active" 2>/dev/null)" == yes ]] ;;
  show) cat "$S/nrestarts" 2>/dev/null || echo 0 ;;
  restart)
    v="$("$FAKE_SYS_BIN" --version 2>/dev/null | awk 'NR==1{print $2}')"
    if [[ "$v" == "$(cat "$S/crash_version" 2>/dev/null)" ]]; then echo no >"$S/active"; else echo yes >"$S/active"; fi
    if [[ "$v" == "$(cat "$S/loop_version" 2>/dev/null)" ]]; then echo $(( $(cat "$S/nrestarts" 2>/dev/null || echo 0) + 1 )) >"$S/nrestarts"; fi ;;
  *) : ;;
esac
FAKE_EOF
chmod +x "$FAKE"
svc_setup() { # svc_setup active(yes|no) BINARY
  rm -rf "$FS"; mkdir -p "$FS"; echo "$1" >"$FS/active"
  fresh_install "$2" || return 1
  export HB_SYSTEMCTL="$FAKE" FAKE_SYS_STATE="$FS" FAKE_SYS_BIN="$(INSTALLED)"
}
restarts() { grep -c '^restart' "$FS/calls" 2>/dev/null || true; }
svc_upgrade() { run_upgrade "$@" --verify-seconds 1; }

# çalışan sağlıklı servis: bir kez yeniden başlatılır
svc_setup yes "$BIN_OLD"; svc_upgrade "$BIN"
[[ $RC -eq 0 && "$(restarts)" == 1 && "$(ver_of "$(INSTALLED)")" == "$(ver_of "$BIN")" ]] && pass || fail "healthy upgrade should restart the service once (rc=$RC, restarts=$(restarts)): $OUT"

# yeni sürüm çöküyor: otomatik geri alma, yapılandırmaya dokunulmaz, çıkış kodu != 0
svc_setup yes "$BIN_OLD"; echo "$(ver_of "$BIN")" >"$FS/crash_version"; conf_before="$(snapshot_conf)"
svc_upgrade "$BIN"
[[ $RC -ne 0 && "$OUT" == *"rolled back automatically"* ]] && pass || fail "a crashing new version must fail the upgrade (rc=$RC): $OUT"
[[ "$(ver_of "$(INSTALLED)")" == 0.9.0 && "$(snapshot_conf)" == "$conf_before" ]] && pass || fail "the old binary was not restored / config touched (installed=$(ver_of "$(INSTALLED)"))"
[[ "$(restarts)" == 2 && "$(cat "$FS/active")" == yes ]] && pass || fail "after the automatic rollback the service should be restarted on the old binary (restarts=$(restarts), active=$(cat "$FS/active"))"

# çökme döngüsü (ayakta görünüyor ama NRestarts artıyor)
svc_setup yes "$BIN_OLD"; echo "$(ver_of "$BIN")" >"$FS/loop_version"
svc_upgrade "$BIN"
[[ $RC -ne 0 && "$OUT" == *"rolled back automatically"* && "$(ver_of "$(INSTALLED)")" == 0.9.0 ]] && pass || fail "a crash loop must trigger the rollback (rc=$RC): $OUT"

# durmuş servis başlatılmaz
svc_setup no "$BIN_OLD"; svc_upgrade "$BIN"
[[ $RC -eq 0 && "$(restarts)" == 0 && "$OUT" == *"was not running"* && "$(ver_of "$(INSTALLED)")" == "$(ver_of "$BIN")" ]] && pass || fail "a stopped service must not be started (rc=$RC, restarts=$(restarts)): $OUT"

# --no-restart yeniden başlatmaz
svc_setup yes "$BIN_OLD"; svc_upgrade "$BIN" --no-restart
[[ $RC -eq 0 && "$(restarts)" == 0 && "$OUT" == *"restart the service"* ]] && pass || fail "--no-restart must not restart (rc=$RC, restarts=$(restarts)): $OUT"

# geri alma: sağlıklı ise bir yeniden başlatma; geri alınan sürüm çöküyorsa eski durum geri gelir
svc_setup yes "$BIN_OLD"; svc_upgrade "$BIN" >/dev/null; : >"$FS/calls"
run_rollback --verify-seconds 1
[[ $RC -eq 0 && "$(restarts)" == 1 && "$(ver_of "$(INSTALLED)")" == 0.9.0 ]] && pass || fail "rollback of a healthy service (rc=$RC, restarts=$(restarts)): $OUT"
svc_setup yes "$BIN_OLD"; svc_upgrade "$BIN" >/dev/null; echo 0.9.0 >"$FS/crash_version"
run_rollback --verify-seconds 1
[[ $RC -ne 0 && "$OUT" == *"did not stay healthy after the rollback"* && "$(ver_of "$(INSTALLED)")" == "$(ver_of "$BIN")" ]] && pass || fail "a rollback target that crashes must restore the previous state (rc=$RC, installed=$(ver_of "$(INSTALLED)")): $OUT"
unset HB_SYSTEMCTL FAKE_SYS_STATE FAKE_SYS_BIN

# ------------------------------------------------------------------ configure (paketle kurulu agent) ve paket koruması
# Paket, binary'yi /usr/bin'e koyar; configure yalnızca yapılandırmayı yazar.
pkg_root() { # pkg_root: binary'si paketle konmuş gibi taze bir kök
  new_root; mkdir -p "$ROOT/usr/bin" "$ROOT/usr/share/healthbeat"; cp "$BIN" "$ROOT/usr/bin/healthbeat-agent"
}
run_configure() { OUT="$(./install.sh configure --root "$ROOT" --no-start "$@" </dev/null 2>&1)"; RC=$?; }

pkg_root
HEALTHBEAT_API_TOKEN="$TOKEN" run_configure --mode push --server-url https://api.example.com --host-id "$UUID" --interval 20 --disk-mounts /,/data
CONF="$ROOT/etc/healthbeat/agent.json"
[[ $RC -eq 0 ]] && pass || fail "configure failed: $OUT"
python3 - <<PY && pass || fail "configure wrote a wrong config"
import json
c = json.load(open("$CONF"))
assert c["mode"] == "push" and c["server_url"] == "https://api.example.com" and c["host_id"] == "$UUID"
assert c["api_token"] == "$TOKEN" and c["interval_seconds"] == 20 and c["disk_mounts"] == ["/", "/data"]
PY
[[ "$(mode_of "$CONF")" == 640 && "$(mode_of "$ROOT/etc/healthbeat")" == 750 ]] && pass || fail "configure: config/dir modes"
[[ ! -e "$ROOT/usr/local/bin/healthbeat-agent" && ! -e "$ROOT/etc/systemd/system/healthbeat-agent.service" ]] && pass || fail "configure must not install a binary or a unit"
[[ "$OUT" != *"$TOKEN"* ]] && pass || fail "configure printed the API token"
timeout 2 "$ROOT/usr/bin/healthbeat-agent" -config "$CONF" -check-config >/dev/null 2>&1 && pass || fail "the real agent rejected the configure-written config"

# pull modu: sertifika üretilir; docker drop-in
pkg_root
HEALTHBEAT_PULL_SECRET="$SECRET" run_configure --mode pull --allowed-ips 10.0.0.1 --docker
[[ $RC -eq 0 && -f "$ROOT/etc/healthbeat/tls/cert.pem" && -f "$ROOT/etc/systemd/system/healthbeat-agent.service.d/docker.conf" ]] && pass || fail "configure pull + docker: $OUT"

# var olan config --force olmadan değiştirilmez; --force ile yedeği alınarak değiştirilir
conf_before="$(sha_of "$ROOT/etc/healthbeat/agent.json")"
HEALTHBEAT_PULL_SECRET="$SECRET" run_configure --mode pull --allowed-ips 10.0.0.2
[[ $RC -ne 0 && "$OUT" == *"--force"* && "$(sha_of "$ROOT/etc/healthbeat/agent.json")" == "$conf_before" ]] && pass || fail "configure must not overwrite an existing config without --force (rc=$RC): $OUT"
HEALTHBEAT_PULL_SECRET="$SECRET" run_configure --force --mode pull --allowed-ips 10.0.0.2
[[ $RC -eq 0 && -n "$(ls "$ROOT"/etc/healthbeat/agent.json.bak.* 2>/dev/null)" && "$(sha_of "$ROOT/etc/healthbeat/agent.json")" != "$conf_before" ]] && pass || fail "configure --force should replace the config and keep a backup (rc=$RC): $OUT"

# binary kurulu değilse ya da --binary verilirse reddedilir
new_root
HEALTHBEAT_API_TOKEN="$TOKEN" run_configure --mode push --server-url https://a.example.com --host-id "$UUID"
[[ $RC -ne 0 && "$OUT" == *"no agent binary is installed"* && ! -e "$ROOT/etc/healthbeat/agent.json" ]] && pass || fail "configure without an installed binary must be refused (rc=$RC): $OUT"
pkg_root
HEALTHBEAT_API_TOKEN="$TOKEN" run_configure --binary "$BIN" --mode push --server-url https://a.example.com --host-id "$UUID"
[[ $RC -ne 0 && "$OUT" == *"--binary does not apply"* ]] && pass || fail "configure --binary must be refused (rc=$RC): $OUT"

# paket işaretçisi: install/upgrade/rollback/uninstall reddedilir, configure çalışır
pkg_root; : >"$ROOT/usr/share/healthbeat/installed-from-package"
for action in install upgrade rollback uninstall; do
  OUT="$(./install.sh $action --root "$ROOT" --binary "$BIN" </dev/null 2>&1)"; RC=$?
  [[ $RC -ne 0 && "$OUT" == *"installed from a package"* ]] && pass || fail "'$action' on a package-managed host must be refused (rc=$RC): $OUT"
done
[[ -x "$ROOT/usr/bin/healthbeat-agent" ]] && pass || fail "a refused action removed the packaged binary"
HEALTHBEAT_API_TOKEN="$TOKEN" run_configure --mode push --server-url https://a.example.com --host-id "$UUID"
[[ $RC -eq 0 && -f "$ROOT/etc/healthbeat/agent.json" ]] && pass || fail "configure must work on a package-managed host: $OUT"

echo
echo "install_test.sh: $PASSES passed, $FAILS failed"
[[ $FAILS -eq 0 ]]
