package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/httpapi"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/version"
)

// Bu testler gerçek router'ı, middleware'i, handler'ları, store'ları ve alert engine'i
// migration'ları uygulanmış geçici bir şema üzerinde sürer (bkz. internal/testdb).

const password = "correct-horse-battery"

type api struct {
	t       *testing.T
	pool    *pgxpool.Pool
	handler http.Handler
	tokens  *authsvc.TokenService
	deps    *httpapi.Deps
}

func newAPI(t *testing.T) *api {
	t.Helper()
	return newAPIWithLimits(t, httpapi.RateLimits{AuthFailuresPerMinute: 6000, IngestPerMinute: 6000})
}

// newAPIWithLimits, hız sınırlarını değiştirilebilir kılar (ör. başarısız denemelerin gerçekten sayıldığını göstermek için).
func newAPIWithLimits(t *testing.T, limits httpapi.RateLimits) *api {
	t.Helper()
	pool := testdb.New(t)
	tokens := authsvc.NewTokenService([]byte("access"), []byte("refresh"), 15*time.Minute, time.Hour)
	engine := alertengine.New(pool, notify.New(notify.Config{})) // yalnızca log'a yazan posta
	deps := httpapi.NewDeps(pool, tokens, engine, limits, testdb.SecretBox(t))
	return &api{t: t, pool: pool, handler: deps.Router(), tokens: tokens, deps: deps}
}

// rawBody, call'a JSON olarak yeniden kodlanmadan gönderilecek ham bir gövde verir.
type rawBody []byte

// call bir istek yapar; out (isteğe bağlı) çözülmüş JSON gövdesini alır. bearer Authorization
// başlığı olarak, extra ek başlıklar olarak gönderilir.
func (a *api) call(method, path, bearer string, body any, out any, extra ...string) int {
	a.t.Helper()
	var buf bytes.Buffer
	if raw, ok := body.(rawBody); ok {
		buf.Write(raw) // olduğu gibi: bozuk JSON gönderebilmek için
	} else if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			a.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			a.t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

// callHeaders is call, but also returns the response headers — for tests that need response
// metadata (X-Total-Count) rather than just the decoded body.
func (a *api) callHeaders(method, path, bearer string, out any) (int, http.Header) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			a.t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code, rec.Header()
}

func (a *api) expect(want int, method, path, bearer string, body any, out any, extra ...string) {
	a.t.Helper()
	if got := a.call(method, path, bearer, body, out, extra...); got != want {
		a.t.Fatalf("%s %s = %d, want %d", method, path, got, want)
	}
}

// login, verilen rolde bir kullanıcıyı doğrudan DB'de oluşturur ve gerçek endpoint üzerinden
// giriş yapar; access token'ını ve kimliğini döndürür.
func (a *api) login(email, role string) (token string, id uuid.UUID) {
	a.t.Helper()
	id = testdb.User(a.t, a.pool, email, role, password)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, &resp)
	return resp.AccessToken, id
}

type idBody struct {
	ID uuid.UUID `json:"id"`
}

func (a *api) createOrg(token, name string) uuid.UUID {
	a.t.Helper()
	var o idBody
	a.expect(201, "POST", "/api/v1/organizations", token, map[string]string{"name": name}, &o)
	return o.ID
}

type createdHost struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"`
	APIToken   string    `json:"api_token"`
	PullSecret string    `json:"pull_secret"`
}

func (a *api) createPushHost(token string, org uuid.UUID, host string) createdHost {
	a.t.Helper()
	var c createdHost
	a.expect(201, "POST", "/api/v1/hosts", token, map[string]any{
		"organization_id": org, "title": host, "ip": "10.0.0.7", "mode": "push", "interval_seconds": 10,
	}, &c)
	return c
}

func (a *api) push(c createdHost, token string, body any) int {
	return a.call("POST", "/api/v1/metrics", token, body, nil, "X-Host-ID", c.ID.String())
}

func metrics(cpu, ram float64) map[string]any {
	return map[string]any{"cpu_usage_pct": cpu, "ram_usage_pct": ram, "disk": []any{}, "docker_containers": []any{}}
}

func (a *api) auditCount(action string) int {
	a.t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE action = $1`, action).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

func TestLogin(t *testing.T) {
	a := newAPI(t)
	testdb.User(t, a.pool, "root@x.test", "super_admin", password)

	var ok struct {
		AccessToken  string         `json:"access_token"`
		RefreshToken string         `json:"refresh_token"`
		User         map[string]any `json:"user"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "root@x.test", "password": password}, &ok)
	if ok.AccessToken == "" || ok.RefreshToken == "" {
		t.Fatal("login returned no tokens")
	}
	if _, leaked := ok.User["password_hash"]; leaked {
		t.Fatal("login response exposes password_hash")
	}
	if ok.User["role"] != "super_admin" {
		t.Fatalf("user = %v", ok.User)
	}
	if a.auditCount("auth.login") != 1 {
		t.Fatal("successful login not audited")
	}

	// Yanlış şifre ve bilinmeyen kullanıcı ayırt edilemez olmalı.
	var wrongPw, noUser struct {
		Error string `json:"error"`
	}
	a.expect(401, "POST", "/api/v1/auth/login", "", map[string]string{"email": "root@x.test", "password": "nope"}, &wrongPw)
	a.expect(401, "POST", "/api/v1/auth/login", "", map[string]string{"email": "ghost@x.test", "password": password}, &noUser)
	if wrongPw.Error == "" || wrongPw != noUser {
		t.Fatalf("distinguishable failures: %q vs %q", wrongPw.Error, noUser.Error)
	}

	a.expect(400, "POST", "/api/v1/auth/login", "", map[string]string{"email": "root@x.test"}, nil)
	a.expect(400, "POST", "/api/v1/auth/login", "", map[string]any{"email": "root@x.test", "password": "x", "extra": 1}, nil) // bilinmeyen alan
}

func TestTokenHandling(t *testing.T) {
	a := newAPI(t)
	testdb.User(t, a.pool, "root@x.test", "super_admin", password)
	var pair struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "root@x.test", "password": password}, &pair)

	a.expect(401, "GET", "/api/v1/me", "", nil, nil)
	a.expect(401, "GET", "/api/v1/me", "garbage", nil, nil)
	a.expect(401, "GET", "/api/v1/me", pair.RefreshToken, nil, nil) // refresh token bir access token değildir
	a.expect(200, "GET", "/api/v1/me", pair.AccessToken, nil, nil)

	a.expect(401, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": pair.AccessToken}, nil)
	var refreshed struct {
		AccessToken string `json:"access_token"`
	}
	a.expect(200, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, &refreshed)
	a.expect(200, "GET", "/api/v1/me", refreshed.AccessToken, nil, nil)

	// Refresh yolu kullanıcıyı yeniden okur: onları silmek refresh'i öldürür.
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	a.expect(401, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, nil)
}

func TestPermissionsByRole(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	orgAdmin, _ := a.login("oa@x.test", "org_admin")
	operator, _ := a.login("op@x.test", "operator")

	cases := []struct {
		name         string
		method, path string
		token        string
		body         any
		want         int
	}{
		{"operator cannot create org", "POST", "/api/v1/organizations", operator, map[string]string{"name": "x"}, 403},
		{"org_admin cannot create org", "POST", "/api/v1/organizations", orgAdmin, map[string]string{"name": "x"}, 403},
		{"org_admin cannot list users", "GET", "/api/v1/users", orgAdmin, nil, 403},
		{"operator cannot list users", "GET", "/api/v1/users", operator, nil, 403},
		{"operator cannot list organizations", "GET", "/api/v1/organizations", operator, nil, 403},
		{"operator cannot create host", "POST", "/api/v1/hosts", operator, map[string]any{}, 403},
		{"operator cannot edit thresholds", "POST", "/api/v1/thresholds", operator, map[string]any{}, 403},
		{"operator can view thresholds", "GET", "/api/v1/thresholds", operator, nil, 200},
		{"operator can view alerts", "GET", "/api/v1/alerts", operator, nil, 200},
		{"operator can view dashboard", "GET", "/api/v1/dashboard/summary", operator, nil, 200},
		{"super_admin can list users", "GET", "/api/v1/users", root, nil, 200},
		{"unauthenticated blocked", "GET", "/api/v1/alerts", "", nil, 401},
	}
	for _, c := range cases {
		if got := a.call(c.method, c.path, c.token, c.body, nil); got != c.want {
			t.Errorf("%s: %s %s = %d, want %d", c.name, c.method, c.path, got, c.want)
		}
	}
}

func TestOrgAdminIsConfinedToAssignedOrganizations(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")

	orgA := a.createOrg(root, "A")
	orgB := a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	hostA := a.createPushHost(root, orgA, "a-1")
	hostB := a.createPushHost(root, orgB, "b-1")

	var orgs []idBody
	a.expect(200, "GET", "/api/v1/organizations", adminTok, nil, &orgs)
	if len(orgs) != 1 || orgs[0].ID != orgA {
		t.Fatalf("org_admin sees organizations %v, want only A", orgs)
	}
	a.expect(200, "GET", "/api/v1/organizations/"+orgA.String()+"/hosts", adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/organizations/"+orgB.String()+"/hosts", adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/organizations/"+orgB.String(), adminTok, nil, nil)

	a.expect(200, "GET", "/api/v1/hosts/"+hostA.ID.String(), adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/hosts/"+hostB.ID.String(), adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/hosts/"+hostB.ID.String()+"/metrics", adminTok, nil, nil)
	a.expect(403, "PUT", "/api/v1/hosts/"+hostB.ID.String(), adminTok, map[string]any{"title": "pwned"}, nil)
	a.expect(403, "DELETE", "/api/v1/hosts/"+hostB.ID.String(), adminTok, nil, nil)
	a.expect(403, "POST", "/api/v1/hosts/"+hostB.ID.String()+"/rotate-credentials", adminTok, nil, nil)

	newHost := func(org uuid.UUID) map[string]any {
		return map[string]any{"organization_id": org, "title": "n", "ip": "10.0.0.9", "mode": "push", "interval_seconds": 10}
	}
	a.expect(201, "POST", "/api/v1/hosts", adminTok, newHost(orgA), nil)
	a.expect(403, "POST", "/api/v1/hosts", adminTok, newHost(orgB), nil)

	th := func(org *uuid.UUID) map[string]any {
		return map[string]any{"organization_id": org, "metric_type": "cpu", "warning_level": 70, "critical_level": 90}
	}
	a.expect(403, "POST", "/api/v1/thresholds", adminTok, th(nil), nil)   // global: yalnızca super_admin
	a.expect(403, "POST", "/api/v1/thresholds", adminTok, th(&orgB), nil) // başkasının organizasyonu
	a.expect(201, "POST", "/api/v1/thresholds", adminTok, th(&orgA), nil) // kendi organizasyonu
	// Sunucuya özel eşik bu uçtan verilmez (PUT /hosts/:id/thresholds'tan yönetilir): host_id tanınmayan alandır.
	a.expect(400, "POST", "/api/v1/thresholds", adminTok, map[string]any{"organization_id": &orgA, "host_id": hostB.ID, "metric_type": "ram", "warning_level": 70, "critical_level": 90}, nil)
	a.expect(200, "GET", "/api/v1/dashboard/summary", adminTok, nil, nil)
}

func TestOperatorSeesOnlyAssignedHosts(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	opTok, opID := a.login("op@x.test", "operator")

	org := a.createOrg(root, "A")
	mine := a.createPushHost(root, org, "mine")
	other := a.createPushHost(root, org, "other") // aynı organizasyon, atanmamış
	testdb.AssignHost(t, a.pool, opID, mine.ID)

	var hosts []idBody
	a.expect(200, "GET", "/api/v1/me/hosts", opTok, nil, &hosts)
	if len(hosts) != 1 || hosts[0].ID != mine.ID {
		t.Fatalf("/me/hosts = %v, want only the assigned host", hosts)
	}
	a.expect(200, "GET", "/api/v1/hosts/"+mine.ID.String(), opTok, nil, nil)
	a.expect(403, "GET", "/api/v1/hosts/"+other.ID.String(), opTok, nil, nil)
	a.expect(403, "GET", "/api/v1/hosts/"+other.ID.String()+"/docker", opTok, nil, nil)

	// İki host'ta da alert var; operator yalnızca kendisininkileri görür ve onaylayabilir.
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 50, "critical_level": 90}, nil)
	a.expect(204, "POST", "/api/v1/metrics", mine.APIToken, metrics(95, 10), nil, "X-Host-ID", mine.ID.String())
	a.expect(204, "POST", "/api/v1/metrics", other.APIToken, metrics(95, 10), nil, "X-Host-ID", other.ID.String())

	var all, visible []struct {
		ID     uuid.UUID `json:"id"`
		HostID uuid.UUID `json:"host_id"`
	}
	a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &all)
	a.expect(200, "GET", "/api/v1/alerts?status=open", opTok, nil, &visible)
	if len(all) != 2 {
		t.Fatalf("super_admin sees %d alerts, want 2", len(all))
	}
	if len(visible) != 1 || visible[0].HostID != mine.ID {
		t.Fatalf("operator sees %v, want only the alert on their host", visible)
	}

	// host_id ile tek sunucuya sınırlama: host.view ile aynı kapsam kuralı geçerli.
	var mineOnly []struct {
		ID     uuid.UUID `json:"id"`
		HostID uuid.UUID `json:"host_id"`
	}
	a.expect(200, "GET", "/api/v1/alerts?host_id="+mine.ID.String(), opTok, nil, &mineOnly)
	if len(mineOnly) != 1 || mineOnly[0].HostID != mine.ID {
		t.Fatalf("alerts?host_id=mine = %v, want only the alert on mine", mineOnly)
	}
	a.expect(403, "GET", "/api/v1/alerts?host_id="+other.ID.String(), opTok, nil, nil) // atanmamış host
	a.expect(200, "GET", "/api/v1/alerts?host_id="+other.ID.String(), root, nil, nil)  // super_admin kapsamsız
	a.expect(404, "GET", "/api/v1/alerts?host_id="+uuid.New().String(), opTok, nil, nil)
	a.expect(400, "GET", "/api/v1/alerts?host_id=not-a-uuid", opTok, nil, nil)

	for _, al := range all {
		if al.HostID == other.ID {
			a.expect(403, "POST", "/api/v1/alerts/"+al.ID.String()+"/acknowledge", opTok, nil, nil)
		}
	}
	a.expect(200, "POST", "/api/v1/alerts/"+visible[0].ID.String()+"/acknowledge", opTok, nil, nil)
	a.expect(409, "POST", "/api/v1/alerts/"+visible[0].ID.String()+"/acknowledge", opTok, nil, nil) // artık açık değil
	a.expect(404, "POST", "/api/v1/alerts/"+uuid.New().String()+"/acknowledge", opTok, nil, nil)
	a.expect(400, "POST", "/api/v1/alerts/not-a-uuid/acknowledge", opTok, nil, nil)
	a.expect(400, "GET", "/api/v1/alerts?status=bogus", opTok, nil, nil)
}

func TestHostCreationValidation(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")

	valid := func() map[string]any {
		return map[string]any{"organization_id": org, "title": "h", "ip": "10.0.0.1", "mode": "push", "interval_seconds": 10}
	}
	with := func(k string, v any) map[string]any { m := valid(); m[k] = v; return m }
	without := func(k string) map[string]any { m := valid(); delete(m, k); return m }

	badPort := with("mode", "pull")
	badPort["pull_port"], badPort["pull_endpoint"] = 70000, "/api/v1/status"

	for name, body := range map[string]map[string]any{
		"organization is mandatory": without("organization_id"),
		"title is mandatory":        with("title", "   "),
		"bad ip":                    with("ip", "not-an-ip"),
		"bad mode":                  with("mode", "carrier-pigeon"),
		"zero interval":             with("interval_seconds", 0),
		"pull needs port+endpoint":  with("mode", "pull"),
		"pull port out of range":    badPort,
	} {
		if got := a.call("POST", "/api/v1/hosts", root, body, nil); got != 400 {
			t.Errorf("%s: status %d, want 400", name, got)
		}
	}
	a.expect(404, "POST", "/api/v1/hosts", root, with("organization_id", uuid.New()), nil)
	a.expect(201, "POST", "/api/v1/hosts", root, valid(), nil)
}

func TestHostCredentialsAndIngest(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")

	push := a.createPushHost(root, org, "push-1")
	if push.APIToken == "" || push.PullSecret != "" {
		t.Fatalf("push host creds = %+v, want api_token only", push)
	}
	otherPush := a.createPushHost(root, org, "push-2")

	var pull createdHost
	a.expect(201, "POST", "/api/v1/hosts", root, map[string]any{
		"organization_id": org, "title": "pull-1", "ip": "10.0.0.8", "mode": "pull",
		"interval_seconds": 10, "pull_port": 9443, "pull_endpoint": "/api/v1/status",
	}, &pull)
	if pull.PullSecret == "" || pull.APIToken != "" {
		t.Fatalf("pull host creds = %+v, want pull_secret only", pull)
	}

	// Secret'lar bir kez gösterilir: host'ı geri okumak onları ifşa etmemeli.
	var raw map[string]any
	a.expect(200, "GET", "/api/v1/hosts/"+push.ID.String(), root, nil, &raw)
	for _, k := range []string{"api_token", "api_token_hash", "pull_secret", "pull_secret_hash"} {
		if _, present := raw[k]; present {
			t.Errorf("GET /hosts/:id exposes %q", k)
		}
	}
	// ...ve saklanan token'ın kendisi değil, bir hash'tir.
	var stored string
	if err := a.pool.QueryRow(context.Background(), `SELECT api_token_hash FROM hosts WHERE id = $1`, push.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == push.APIToken || stored != authsvc.HashOpaqueSecret(push.APIToken) {
		t.Fatal("api token is not stored as its hash")
	}

	// POST /metrics için kimlik doğrulama matrisi.
	a.expect(204, "POST", "/api/v1/metrics", push.APIToken, metrics(12, 34), nil, "X-Host-ID", push.ID.String())
	for name, c := range map[string]struct {
		token, id string
	}{
		"wrong token":          {"nope", push.ID.String()},
		"another host's token": {otherPush.APIToken, push.ID.String()},
		"pull host's secret":   {pull.PullSecret, pull.ID.String()},
		"unknown host id":      {push.APIToken, uuid.New().String()},
		"missing host id":      {push.APIToken, ""},
		"panel JWT instead":    {mustLoginToken(a, "x@x.test"), push.ID.String()},
	} {
		if got := a.call("POST", "/api/v1/metrics", c.token, metrics(1, 1), nil, "X-Host-ID", c.id); got != 401 {
			t.Errorf("%s: status %d, want 401", name, got)
		}
	}

	// Doğrulama.
	hdr := []string{"X-Host-ID", push.ID.String()}
	a.expect(400, "POST", "/api/v1/metrics", push.APIToken, metrics(150, 10), nil, hdr...)
	a.expect(400, "POST", "/api/v1/metrics", push.APIToken, metrics(10, -1), nil, hdr...)
	// Bilinmeyen alan artık 400 DEĞİL: yeni bir agent sürümü henüz o alanı tanımayan server'a
	// gönderebilmeli (docs/COMPATIBILITY.md; bkz. TestIngestAcceptsEveryGoldenPayload).

	// İyi push saklanır, panele görünür olur ve host'ı çevrimiçi yapar.
	var points []struct {
		CPU float64 `json:"cpu_usage_pct"`
		RAM float64 `json:"ram_usage_pct"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+push.ID.String()+"/metrics", root, nil, &points)
	if len(points) != 1 || points[0].CPU != 12 || points[0].RAM != 34 {
		t.Fatalf("stored metrics = %+v, want the single valid push", points)
	}
	var c createdHost
	a.expect(200, "GET", "/api/v1/hosts/"+push.ID.String(), root, nil, &c)
	if c.Status != "online" {
		t.Fatalf("status = %q after a push, want online", c.Status)
	}
	a.expect(400, "GET", "/api/v1/hosts/"+push.ID.String()+"/metrics?from=yesterday", root, nil, nil)

	// Kimlik bilgisini yenilemek eski token'ı hemen geçersiz kılar.
	var rotated createdHost
	a.expect(200, "POST", "/api/v1/hosts/"+push.ID.String()+"/rotate-credentials", root, nil, &rotated)
	if rotated.APIToken == "" || rotated.APIToken == push.APIToken {
		t.Fatal("rotation did not issue a new token")
	}
	a.expect(401, "POST", "/api/v1/metrics", push.APIToken, metrics(1, 1), nil, hdr...)
	a.expect(204, "POST", "/api/v1/metrics", rotated.APIToken, metrics(1, 1), nil, hdr...)
}

func mustLoginToken(a *api, email string) string {
	tok, _ := a.login(email, "super_admin")
	return tok
}

func TestIngestRaisesAndResolvesAlertsEndToEnd(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	hdr := []string{"X-Host-ID", c.ID.String()}

	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 50, "critical_level": 90}, nil)
	a.expect(409, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 60, "critical_level": 95}, nil) // yinelenen global
	a.expect(400, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 95, "critical_level": 60}, nil)
	a.expect(400, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "host_offline", "warning_level": 1, "critical_level": 2}, nil)

	type summary struct {
		Total, Online, Critical, Warning int
	}
	dash := func() summary {
		var s struct {
			Total    int `json:"total_hosts"`
			Online   int `json:"online_hosts"`
			Critical int `json:"open_critical_alerts"`
			Warning  int `json:"open_warning_alerts"`
		}
		a.expect(200, "GET", "/api/v1/dashboard/summary", root, nil, &s)
		return summary{s.Total, s.Online, s.Critical, s.Warning}
	}

	a.expect(204, "POST", "/api/v1/metrics", c.APIToken, metrics(20, 10), nil, hdr...)
	if got := dash(); got != (summary{1, 1, 0, 0}) {
		t.Fatalf("healthy dashboard = %+v", got)
	}

	a.expect(204, "POST", "/api/v1/metrics", c.APIToken, metrics(95, 10), nil, hdr...)
	a.expect(204, "POST", "/api/v1/metrics", c.APIToken, metrics(96, 10), nil, hdr...) // aynı olay
	if got := dash(); got != (summary{1, 1, 1, 0}) {
		t.Fatalf("dashboard during incident = %+v, want exactly one critical alert", got)
	}
	var open []struct {
		AlertType string `json:"alert_type"`
		Level     string `json:"level"`
	}
	a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &open)
	if len(open) != 1 || open[0].AlertType != "cpu" || open[0].Level != "critical" {
		t.Fatalf("open alerts = %+v", open)
	}

	a.expect(204, "POST", "/api/v1/metrics", c.APIToken, metrics(10, 10), nil, hdr...)
	if got := dash(); got != (summary{1, 1, 0, 0}) {
		t.Fatalf("dashboard after recovery = %+v", got)
	}
	a.expect(200, "GET", "/api/v1/alerts?status=resolved", root, nil, &open)
	if len(open) != 1 {
		t.Fatalf("resolved alerts = %+v, want the one that recovered", open)
	}
}

func TestAuditLogRecordsCriticalActions(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	a.expect(200, "POST", "/api/v1/hosts/"+c.ID.String()+"/rotate-credentials", root, nil, nil)
	a.expect(204, "DELETE", "/api/v1/hosts/"+c.ID.String(), root, nil, nil)

	for _, action := range []string{"organization.create", "host.create", "host.rotate_credentials", "host.delete"} {
		if a.auditCount(action) != 1 {
			t.Errorf("audit rows for %q = %d, want 1", action, a.auditCount(action))
		}
	}

	var actor uuid.UUID
	var email string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT user_id, actor_email FROM audit_logs WHERE action = 'host.delete'`).Scan(&actor, &email); err != nil {
		t.Fatal(err)
	}
	if actor != rootID || email != "root@x.test" {
		t.Fatalf("audit actor = %v/%q, want the deleting user", actor, email)
	}

	// Denetim izinde gizli hiçbir şey yok.
	var dump string
	if err := a.pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(details::text, ' '), '') FROM audit_logs`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, c.APIToken) {
		t.Fatal("api token leaked into audit_logs.details")
	}
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (a *api) loginPair(email string) tokenPair {
	a.t.Helper()
	var p tokenPair
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, &p)
	return p
}

func (a *api) refresh(refreshToken string, out *tokenPair) int {
	a.t.Helper()
	var dst any // tipli nil bir *tokenPair nil olmayan bir arayüz olurdu; bunun yerine gerçek bir nil geçir
	if out != nil {
		dst = out
	}
	return a.call("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refreshToken}, dst)
}

func TestRefreshTokenRotation(t *testing.T) {
	a := newAPI(t)
	testdb.User(t, a.pool, "u@x.test", "operator", password)
	first := a.loginPair("u@x.test")

	var second tokenPair
	if code := a.refresh(first.RefreshToken, &second); code != 200 {
		t.Fatalf("refresh = %d", code)
	}
	if second.RefreshToken == "" || second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh did not rotate the refresh token")
	}
	a.expect(200, "GET", "/api/v1/me", second.AccessToken, nil, nil)

	// Döndürülmüş token'ın kendisi de değiştirilebilir ve zincir böyle devam eder.
	var third tokenPair
	if code := a.refresh(second.RefreshToken, &third); code != 200 {
		t.Fatalf("second-generation refresh = %d", code)
	}

	// Aynı token'la yarışan iki sekme, tolerans penceresi içinde, ikisi de kazanır.
	var racer tokenPair
	if code := a.refresh(second.RefreshToken, &racer); code != 200 {
		t.Fatalf("refresh of a just-rotated token inside grace = %d, want 200", code)
	}
}

func TestRefreshTokenReuseRevokesTheSession(t *testing.T) {
	a := newAPI(t)
	testdb.User(t, a.pool, "u@x.test", "operator", password)
	stolen := a.loginPair("u@x.test")

	// Meşru host yeniler; hırsız hâlâ eski token'ı tutuyor.
	var legit tokenPair
	if code := a.refresh(stolen.RefreshToken, &legit); code != 200 {
		t.Fatalf("legit refresh = %d", code)
	}

	// Zaman tolerans penceresini aşar, sonra hırsız onu yeniden oynatır.
	if _, err := a.pool.Exec(context.Background(), `UPDATE refresh_tokens SET rotated_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if code := a.refresh(stolen.RefreshToken, nil); code != 401 {
		t.Fatalf("replayed token = %d, want 401", code)
	}

	// ...ve meşru istemcinin daha yeni token'ı da onunla ölür: yeniden giriş gerekir.
	if code := a.refresh(legit.RefreshToken, nil); code != 401 {
		t.Fatalf("token from a compromised family = %d, want 401", code)
	}

	if a.auditCount("auth.token_reuse_detected") != 1 {
		t.Fatal("refresh token reuse was not recorded in the audit log")
	}

	// Yeni bir giriş temiz bir aile başlatır.
	fresh := a.loginPair("u@x.test")
	if code := a.refresh(fresh.RefreshToken, nil); code != 200 {
		t.Fatalf("refresh after re-login = %d, want 200", code)
	}
}

func TestLogoutRevokesOnlyThatSession(t *testing.T) {
	a := newAPI(t)
	testdb.User(t, a.pool, "u@x.test", "operator", password)
	laptop := a.loginPair("u@x.test")
	phone := a.loginPair("u@x.test")

	a.expect(204, "POST", "/api/v1/auth/logout", "", map[string]string{"refresh_token": laptop.RefreshToken}, nil)
	if code := a.refresh(laptop.RefreshToken, nil); code != 401 {
		t.Fatalf("refresh after logout = %d, want 401", code)
	}
	if code := a.refresh(phone.RefreshToken, nil); code != 200 {
		t.Fatalf("other device after logout = %d, want 200", code)
	}

	// İdempotenttir ve refresh token olmayan şeyleri reddeder.
	a.expect(204, "POST", "/api/v1/auth/logout", "", map[string]string{"refresh_token": laptop.RefreshToken}, nil)
	a.expect(401, "POST", "/api/v1/auth/logout", "", map[string]string{"refresh_token": laptop.AccessToken}, nil)
	a.expect(401, "POST", "/api/v1/auth/logout", "", map[string]string{"refresh_token": "junk"}, nil)
	a.expect(400, "POST", "/api/v1/auth/logout", "", map[string]string{}, nil)
	if a.auditCount("auth.logout") != 2 {
		t.Fatalf("auth.logout audit rows = %d, want 2", a.auditCount("auth.logout"))
	}
}

func TestPasswordChangeEndsAllSessions(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	victimID := testdb.User(t, a.pool, "u@x.test", "operator", password)
	s1, s2 := a.loginPair("u@x.test"), a.loginPair("u@x.test")

	// Şifre dışında bir şeyi güncellemek oturumlara dokunmaz.
	a.expect(200, "PUT", "/api/v1/users/"+victimID.String(), root, map[string]any{"role": "operator"}, nil)
	if code := a.refresh(s1.RefreshToken, nil); code != 200 {
		t.Fatalf("refresh after a non-password update = %d, want 200", code)
	}

	a.expect(200, "PUT", "/api/v1/users/"+victimID.String(), root, map[string]any{"password": "a-brand-new-password"}, nil)
	if code := a.refresh(s2.RefreshToken, nil); code != 401 {
		t.Fatalf("refresh after password change = %d, want 401", code)
	}

	// Eski şifre artık giriş yapmaz; yenisi yapar.
	a.expect(401, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": password}, nil)
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": "a-brand-new-password"}, nil)
}

func TestRefreshTokenWithoutTrackingIsRejected(t *testing.T) {
	a := newAPI(t)
	id := testdb.User(t, a.pool, "u@x.test", "operator", password)

	// Geçerli imzalı ama server'ın hiç kaydetmediği bir refresh token — ör. token izlemeden önce
	// üretilmiş ya da sızmış bir secret'la ama bilinmeyen bir jti ile sahte üretilmiş — çalışmamalı.
	untracked, _, err := a.tokens.IssueRefreshToken(id, "u@x.test", "operator", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if code := a.refresh(untracked, nil); code != 401 {
		t.Fatalf("untracked jti = %d, want 401", code)
	}
}

func TestPullSecretLifecycleOverAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")

	var pull createdHost
	a.expect(201, "POST", "/api/v1/hosts", root, map[string]any{
		"organization_id": org, "title": "pull-1", "ip": "10.0.0.8", "mode": "pull",
		"interval_seconds": 10, "pull_port": 9443, "pull_endpoint": "/api/v1/status",
	}, &pull)
	if pull.PullSecret == "" {
		t.Fatal("create did not return the pull secret once")
	}

	stored := func() string {
		var v string
		if err := a.pool.QueryRow(context.Background(), `SELECT pull_secret_enc FROM hosts WHERE id = $1`, pull.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	box := testdb.SecretBox(t)
	assertSealed := func(want string) {
		t.Helper()
		raw := stored()
		if strings.Contains(raw, want) || !strings.HasPrefix(raw, "enc:v1:") {
			t.Fatalf("pull_secret at rest = %q, want ciphertext", raw)
		}
		// Pull scheduler'ın host'a sunacağı şey tam olarak üretilen şeydir.
		if got, err := box.Open(raw, pull.ID.String()); err != nil || got != want {
			t.Fatalf("decrypted = %q err=%v, want the issued secret", got, err)
		}
	}
	assertSealed(pull.PullSecret)

	var read map[string]any
	a.expect(200, "GET", "/api/v1/hosts/"+pull.ID.String(), root, nil, &read)
	if _, leaked := read["pull_secret"]; leaked {
		t.Fatal("GET /hosts/:id exposes pull_secret")
	}

	var rotated createdHost
	a.expect(200, "POST", "/api/v1/hosts/"+pull.ID.String()+"/rotate-credentials", root, nil, &rotated)
	if rotated.PullSecret == "" || rotated.PullSecret == pull.PullSecret {
		t.Fatal("rotation did not issue a new secret")
	}
	assertSealed(rotated.PullSecret)
}

type auditPage struct {
	Items []struct {
		ID         uuid.UUID       `json:"id"`
		UserID     *uuid.UUID      `json:"user_id"`
		ActorEmail string          `json:"actor_email"`
		Action     string          `json:"action"`
		TargetType string          `json:"target_type"`
		TargetID   *string         `json:"target_id"`
		Details    json.RawMessage `json:"details"`
		CreatedAt  time.Time       `json:"created_at"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// seedAudit, tam bir zaman damgasıyla bir denetim satırı ekler.
func (a *api) seedAudit(actor *uuid.UUID, email, action, targetType, targetID string, at time.Time) uuid.UUID {
	a.t.Helper()
	var id uuid.UUID
	err := a.pool.QueryRow(context.Background(),
		`INSERT INTO audit_logs (user_id, actor_email, action, target_type, target_id, created_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6) RETURNING id`,
		actor, email, action, targetType, targetID, at).Scan(&id)
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

func TestAuditLogsRequireAuditViewPermission(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	orgAdmin, _ := a.login("oa@x.test", "org_admin")
	operator, _ := a.login("op@x.test", "operator")

	a.expect(200, "GET", "/api/v1/audit-logs", root, nil, nil)
	a.expect(403, "GET", "/api/v1/audit-logs", orgAdmin, nil, nil)
	a.expect(403, "GET", "/api/v1/audit-logs", operator, nil, nil)
	a.expect(401, "GET", "/api/v1/audit-logs", "", nil, nil)
}

func TestAuditLogsReflectRealActions(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "acme")
	host := a.createPushHost(root, org, "web-1")
	a.expect(204, "DELETE", "/api/v1/hosts/"+host.ID.String(), root, nil, nil)

	var page auditPage
	a.expect(200, "GET", "/api/v1/audit-logs?action=host.", root, nil, &page)
	if len(page.Items) != 2 || page.Items[0].Action != "host.delete" || page.Items[1].Action != "host.create" {
		t.Fatalf("host actions (newest first) = %+v", page.Items)
	}
	del := page.Items[0]
	if del.ActorEmail != "root@x.test" || del.UserID == nil || *del.UserID != rootID ||
		del.TargetType != "host" || del.TargetID == nil || *del.TargetID != host.ID.String() {
		t.Fatalf("delete entry = %+v", del)
	}
	var det map[string]any
	if err := json.Unmarshal(page.Items[1].Details, &det); err != nil || det["title"] != "web-1" {
		t.Fatalf("create details = %s (%v), want the title recorded", page.Items[1].Details, err)
	}

	// Secret'lar endpoint'in döndürdüğü şeyde asla görünmez.
	var raw map[string]any
	a.expect(200, "GET", "/api/v1/audit-logs?limit=200", root, nil, &raw)
	if b, _ := json.Marshal(raw); strings.Contains(string(b), host.APIToken) {
		t.Fatal("api token leaked through the audit endpoint")
	}
}

func TestAuditLogFilters(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	alice := testdb.User(t, a.pool, "alice@x.test", "org_admin", password)
	bob := testdb.User(t, a.pool, "bob@x.test", "operator", password)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)

	// Yukarıdaki giriş gerçek bir auth.login satırı yazdı; bilinen boş bir logdan başla.
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM audit_logs`); err != nil {
		t.Fatal(err)
	}

	a.seedAudit(&alice, "alice@x.test", "host.create", "host", "c-1", base.Add(1*time.Minute))
	a.seedAudit(&alice, "alice@x.test", "host.delete", "host", "c-1", base.Add(2*time.Minute))
	a.seedAudit(&bob, "bob@x.test", "alert.acknowledge", "alert", "a-9", base.Add(3*time.Minute))
	a.seedAudit(&bob, "bob@x.test", "user.update", "user", "u-2", base.Add(4*time.Minute))

	count := func(query string) int {
		t.Helper()
		var p auditPage
		a.expect(200, "GET", "/api/v1/audit-logs?"+query, root, nil, &p)
		return len(p.Items)
	}
	stamp := func(d time.Duration) string { return url.QueryEscape(base.Add(d).Format(time.RFC3339)) }

	if n := count("action=host."); n != 2 {
		t.Errorf("action prefix host. = %d rows, want 2", n)
	}
	if n := count("action=host.create"); n != 1 {
		t.Errorf("exact action = %d rows, want 1", n)
	}
	if n := count("action=lient"); n != 0 {
		t.Errorf("action filter must be a prefix match, but a substring matched (%d rows)", n)
	}
	if n := count("action=%25"); n != 0 {
		t.Errorf("LIKE metacharacters are not escaped (%d rows for %%)", n)
	}
	if n := count("target_type=alert"); n != 1 {
		t.Errorf("target_type = %d rows, want 1", n)
	}
	if n := count("target_id=c-1"); n != 2 {
		t.Errorf("target_id = %d rows, want 2", n)
	}
	if n := count("user_id=" + bob.String()); n != 2 {
		t.Errorf("user_id = %d rows, want 2", n)
	}
	if n := count("from=" + stamp(2*time.Minute)); n != 3 { // kapsayıcı alt sınır
		t.Errorf("from = %d rows, want 3", n)
	}
	if n := count("to=" + stamp(2*time.Minute)); n != 1 { // dışlayıcı üst sınır
		t.Errorf("to = %d rows, want 1", n)
	}
	if n := count("action=host.&user_id=" + bob.String()); n != 0 {
		t.Errorf("combined filters = %d rows, want 0", n)
	}
}

func TestAuditLogPaginationIsStableAndComplete(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)

	// 8 satır; birkaçı aynı zaman damgasını paylaşır, bu yüzden sayfalama id üzerinden ayrım
	// yapmalı, yoksa sayfa sınırlarında satırları atlar/yineler.
	want := map[uuid.UUID]bool{}
	for i := 0; i < 8; i++ {
		at := base.Add(time.Duration(i/3) * time.Minute) // zaman damgası başına 3 satır
		id := a.seedAudit(nil, "seed@x.test", fmt.Sprintf("test.action%d", i), "test", "", at)
		want[id] = true
	}
	a.expect(200, "GET", "/api/v1/audit-logs?action=test.&limit=200", root, nil, nil)

	seen := map[uuid.UUID]bool{}
	var order []time.Time
	cursor, pages := "", 0
	for {
		path := "/api/v1/audit-logs?action=test.&limit=3"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		var p auditPage
		a.expect(200, "GET", path, root, nil, &p)
		pages++
		for _, it := range p.Items {
			if seen[it.ID] {
				t.Fatalf("row %s returned twice across pages", it.ID)
			}
			seen[it.ID] = true
			order = append(order, it.CreatedAt)
		}

		if pages == 1 { // sayfalama sırasında gelen yeni etkinlik sonraki sayfaları kaydırmamalı
			a.seedAudit(nil, "seed@x.test", "test.newer", "test", "", time.Now().UTC())
		}
		if p.NextCursor == nil {
			break
		}
		if len(p.Items) != 3 {
			t.Fatalf("page %d has %d items but claims a next page", pages, len(p.Items))
		}
		cursor = *p.NextCursor
		if pages > 10 {
			t.Fatal("pagination does not terminate")
		}
	}

	for id := range want {
		if !seen[id] {
			t.Errorf("row %s never returned", id)
		}
	}
	if pages != 3 || len(seen) != 8 {
		t.Errorf("pages=%d rows=%d, want 3 pages and the 8 seeded rows (the newer row must not appear)", pages, len(seen))
	}
	for i := 1; i < len(order); i++ {
		if order[i].After(order[i-1]) {
			t.Fatal("rows are not ordered newest first")
		}
	}

	// Tam sığan bir limit: sarkan sonraki sayfa yok.
	var p auditPage
	a.expect(200, "GET", "/api/v1/audit-logs?action=test.action&limit=8", root, nil, &p)
	if len(p.Items) != 8 || p.NextCursor != nil {
		t.Errorf("exact-fit page: %d items, next_cursor=%v; want 8 and none", len(p.Items), p.NextCursor)
	}
}

func TestAuditLogRejectsBadParameters(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")

	notACursor := base64.RawURLEncoding.EncodeToString([]byte("no-separator"))
	badTime := base64.RawURLEncoding.EncodeToString([]byte("yesterday|" + uuid.NewString()))
	badID := base64.RawURLEncoding.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano) + "|nope"))

	for _, q := range []string{
		"limit=0", "limit=201", "limit=-1", "limit=abc",
		"user_id=not-a-uuid", "from=yesterday", "to=2026-13-45",
		"cursor=!!!", "cursor=" + notACursor, "cursor=" + badTime, "cursor=" + badID,
	} {
		if got := a.call("GET", "/api/v1/audit-logs?"+q, root, nil, nil); got != 400 {
			t.Errorf("?%s = %d, want 400", q, got)
		}
	}
	a.expect(200, "GET", "/api/v1/audit-logs?limit=1", root, nil, nil)
	a.expect(200, "GET", "/api/v1/audit-logs?limit=200", root, nil, nil)
}

func TestAuditRowsSurviveUserDeletion(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	ghost := testdb.User(t, a.pool, "ghost@x.test", "operator", password)
	a.seedAudit(&ghost, "ghost@x.test", "alert.acknowledge", "alert", "a-1", time.Now().UTC())

	a.expect(204, "DELETE", "/api/v1/users/"+ghost.String(), root, nil, nil)

	var p auditPage
	a.expect(200, "GET", "/api/v1/audit-logs?action=alert.", root, nil, &p)
	if len(p.Items) != 1 || p.Items[0].ActorEmail != "ghost@x.test" || p.Items[0].UserID != nil {
		t.Fatalf("entry after actor deletion = %+v, want it kept with the email snapshot and no user_id", p.Items)
	}
}

func TestSoleSuperAdminCannotLockTheSystemOut(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")

	var resp struct {
		Error string `json:"error"`
	}
	a.expect(409, "PUT", "/api/v1/users/"+rootID.String(), root, map[string]any{"role": "operator"}, &resp)
	if !strings.Contains(resp.Error, "son super_admin") {
		t.Fatalf("error = %q, want it to explain why", resp.Error)
	}
	var role string
	a.pool.QueryRow(context.Background(), `SELECT role FROM users WHERE id = $1`, rootID).Scan(&role)
	if role != "super_admin" {
		t.Fatalf("role became %q despite the 409", role)
	}
	a.expect(400, "DELETE", "/api/v1/users/"+rootID.String(), root, nil, nil) // kendini silme engelli kalır

	// İkinci bir super_admin olunca ilki görevi bırakabilir.
	backup, _ := a.login("backup@x.test", "super_admin")
	a.expect(200, "PUT", "/api/v1/users/"+rootID.String(), root, map[string]any{"role": "operator"}, nil)
	a.expect(200, "GET", "/api/v1/users", backup, nil, nil) // kalan yönetici hâlâ çalışır
	// ...ve kalan artık korunuyor.
	var backupID uuid.UUID
	a.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = 'backup@x.test'`).Scan(&backupID)
	a.expect(409, "PUT", "/api/v1/users/"+backupID.String(), backup, map[string]any{"role": "org_admin"}, nil)
}

func TestMetricsEndpointBoundsItsResponse(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "o")
	c := a.createPushHost(root, org, "c")
	// 20.000 birer saniyelik örnek (regresyon: bu eskiden tam olarak döndürülüyordu).
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO metrics (host_id, recorded_at, cpu_usage_pct, ram_usage_pct, disk_json)
		 SELECT $1, now() - (g || ' seconds')::interval, 10, 20, '[]'::jsonb FROM generate_series(1, 20000) g`, c.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/hosts/" + c.ID.String() + "/metrics?from=2020-01-01T00:00:00Z"

	var pts []map[string]any
	a.expect(200, "GET", path, root, nil, &pts)
	if len(pts) == 0 || len(pts) > 1100 {
		t.Fatalf("default request returned %d points, want between 1 and ~1000", len(pts))
	}

	pts = nil
	a.expect(200, "GET", path+"&max_points=50", root, nil, &pts)
	if len(pts) == 0 || len(pts) > 60 {
		t.Fatalf("max_points=50 returned %d points", len(pts))
	}

	for _, q := range []string{"max_points=0", "max_points=-1", "max_points=5001", "max_points=abc", "max_points="} {
		want := 400
		if q == "max_points=" { // boş değer "verilmedi" demektir
			want = 200
		}
		if got := a.call("GET", path+"&"+q, root, nil, nil); got != want {
			t.Errorf("?%s = %d, want %d", q, got, want)
		}
	}
	a.expect(200, "GET", path+"&max_points=5000", root, nil, nil)
}

func TestDockerEndpointReflectsLatestReport(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "o")
	c := a.createPushHost(root, org, "web-1")
	hdr := []string{"X-Host-ID", c.ID.String()}
	push := func(restarts int, names ...string) {
		var cs []map[string]any
		for _, n := range names {
			cs = append(cs, map[string]any{"name": n, "image": n + ":1", "status": "running", "cpu_pct": 1.5, "ram_mb": 10, "restart_count": restarts, "uptime_seconds": 5})
		}
		a.expect(204, "POST", "/api/v1/metrics", c.APIToken,
			map[string]any{"cpu_usage_pct": 1, "ram_usage_pct": 1, "disk": []any{}, "docker_containers": cs}, nil, hdr...)
	}

	for i := 0; i < 5; i++ {
		push(i, "web", "db")
	}
	var got []struct {
		Name         string `json:"name"`
		RestartCount int    `json:"restart_count"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String()+"/docker", root, nil, &got)
	if len(got) != 2 || got[0].Name != "db" || got[0].RestartCount != 4 || got[1].Name != "web" {
		t.Fatalf("docker = %+v, want the two containers with the latest restart_count", got)
	}
	var rows int
	a.pool.QueryRow(context.Background(), `SELECT count(*) FROM docker_containers WHERE host_id = $1`, c.ID).Scan(&rows)
	if rows != 2 {
		t.Fatalf("%d docker_containers rows after 5 pushes, want 2", rows)
	}

	push(9, "web") // db rapordan kayboldu
	got = nil
	a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String()+"/docker", root, nil, &got)
	if len(got) != 1 || got[0].Name != "web" || got[0].RestartCount != 9 {
		t.Fatalf("after db disappeared: %+v", got)
	}
}

func TestUserEmailsAreCaseInsensitive(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	create := func(email string) int {
		return a.call("POST", "/api/v1/users", root, map[string]any{"email": email, "password": "long-enough-pw", "role": "operator"}, nil)
	}

	var created struct {
		ID    uuid.UUID `json:"id"`
		Email string    `json:"email"`
	}
	a.expect(201, "POST", "/api/v1/users", root, map[string]any{"email": "  Alice@Example.COM ", "password": "long-enough-pw", "role": "operator"}, &created)
	if created.Email != "alice@example.com" {
		t.Fatalf("stored email = %q, want it normalised to lowercase", created.Email)
	}

	// Regresyon: bunlar eskiden ayrı hesaplardı.
	if got := create("alice@example.com"); got != 409 {
		t.Errorf("same address, different case: %d, want 409", got)
	}
	if got := create("ALICE@EXAMPLE.COM"); got != 409 {
		t.Errorf("upper-case duplicate: %d, want 409", got)
	}

	// Kullanıcı hangi harf büyüklüğüyle yazarsa yazsın giriş çalışır.
	for _, typed := range []string{"alice@example.com", "Alice@Example.com", "ALICE@EXAMPLE.COM"} {
		a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": typed, "password": "long-enough-pw"}, nil)
	}

	// Güncellemeler de normalleştirilir ve büyük/küçük harfe duyarsız çakışır.
	other := testdb.User(t, a.pool, "bob@example.com", "operator", password)
	a.expect(409, "PUT", "/api/v1/users/"+other.String(), root, map[string]any{"email": "ALICE@example.com"}, nil)
	var updated struct {
		Email string `json:"email"`
	}
	a.expect(200, "PUT", "/api/v1/users/"+other.String(), root, map[string]any{"email": "Robert@Example.com"}, &updated)
	if updated.Email != "robert@example.com" {
		t.Fatalf("updated email = %q", updated.Email)
	}

	// Çöp adresler reddedilir; başlık enjeksiyonu denemeleri dahil.
	for _, bad := range []string{"not-an-email", "a@x.com\r\nBcc: e@evil.test", "Alice <alice@x.com>", "a@x.com, b@y.com"} {
		if got := create(bad); got != 400 {
			t.Errorf("create(%q) = %d, want 400", bad, got)
		}
	}
	// Uygulama kodu atlansa bile veritabanı karışık harfi reddeder.
	if _, err := a.pool.Exec(context.Background(), `UPDATE users SET email = 'Mixed@Case.com' WHERE id = $1`, other); err == nil {
		t.Fatal("database accepted a mixed-case email; users_email_lowercase_chk is missing")
	}
}

// Regresyon: bilinmeyen bir e-posta hiç bcrypt işi yapmadan dönüyordu; bu yüzden yanıt süresi
// hangi e-postaların hesabı olduğunu ele veriyordu (~80 kat daha hızlı ölçüldü).
func TestLoginTimingDoesNotRevealWhichEmailsExist(t *testing.T) {
	a := newAPI(t)
	// Üretim maliyetinde hash'e sahip gerçek bir hesap (testdb fixture'ları MinCost kullanır).
	hash, err := authsvc.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (email, password_hash, role) VALUES ('real@x.test', $1, 'operator')`, hash); err != nil {
		t.Fatal(err)
	}

	median := func(email string) time.Duration {
		var ds []time.Duration
		for i := 0; i < 5; i++ {
			start := time.Now()
			a.call("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": "wrong-password"}, nil)
			ds = append(ds, time.Since(start))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2]
	}
	known, unknown := median("real@x.test"), median("ghost@x.test")
	t.Logf("wrong password for an existing account: %v; unknown account: %v", known, unknown)

	lo, hi := known, unknown
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < hi/2 { // iki kat içinde: ikisinde de bcrypt baskın
		t.Fatalf("login timing differs %v vs %v: response time reveals whether an email is registered", known, unknown)
	}
}

func (a *api) assignedHosts(root string, userID uuid.UUID) []uuid.UUID {
	a.t.Helper()
	var resp struct {
		HostIDs []uuid.UUID `json:"host_ids"`
	}
	a.expect(200, "GET", "/api/v1/users/"+userID.String()+"/hosts", root, nil, &resp)
	return resp.HostIDs
}

func sameSet(got []uuid.UUID, want ...uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[uuid.UUID]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// Panelin "operatöre sunucu ata" ekranı seçimin tamamını PUT ile gönderir; bu yüzden PUT
// tam değiştirme anlamına sahip olmalı.
func TestOperatorHostAssignmentReplacesTheWholeSet(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	opTok, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	c1, c2 := a.createPushHost(root, orgA, "a-1"), a.createPushHost(root, orgA, "a-2")
	c3 := a.createPushHost(root, orgB, "b-1")
	put := func(ids ...uuid.UUID) int {
		if ids == nil {
			ids = []uuid.UUID{}
		}
		return a.call("PUT", "/api/v1/users/"+opID.String()+"/hosts", root, map[string]any{"host_ids": ids}, nil)
	}
	visible := func() []uuid.UUID {
		var cs []idBody
		a.expect(200, "GET", "/api/v1/me/hosts", opTok, nil, &cs)
		var ids []uuid.UUID
		for _, c := range cs {
			ids = append(ids, c.ID)
		}
		return ids
	}

	if got := a.assignedHosts(root, opID); len(got) != 0 {
		t.Fatalf("new operator already has %v", got)
	}
	if code := put(c1.ID, c3.ID); code != 204 && code != 200 {
		t.Fatalf("PUT = %d", code)
	}
	if got := a.assignedHosts(root, opID); !sameSet(got, c1.ID, c3.ID) {
		t.Fatalf("after first PUT: %v", got)
	}
	if !sameSet(visible(), c1.ID, c3.ID) {
		t.Fatal("the operator's own view does not match the assignment")
	}
	a.expect(200, "GET", "/api/v1/hosts/"+c3.ID.String(), opTok, nil, nil)

	// İkinci bir PUT değiştirir, birleştirmez: c1 ve c3 kaybolur, c2 görünür.
	put(c2.ID)
	if got := a.assignedHosts(root, opID); !sameSet(got, c2.ID) {
		t.Fatalf("after replacing: %v, want only c2", got)
	}
	a.expect(403, "GET", "/api/v1/hosts/"+c3.ID.String(), opTok, nil, nil) // erişim hemen iptal edilir

	put() // her şeyi temizlemek geçerli bir seçimdir
	if got := a.assignedHosts(root, opID); len(got) != 0 {
		t.Fatalf("after clearing: %v", got)
	}
	if len(visible()) != 0 {
		t.Fatal("operator still sees hosts after all were unassigned")
	}
}

func TestOperatorHostAssignmentValidation(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	_, opID := a.login("op@x.test", "operator")
	org := a.createOrg(root, "A")
	c1 := a.createPushHost(root, org, "a-1")
	path := "/api/v1/users/" + opID.String() + "/hosts"

	// Aynı kimliğin iki kez gelmesi (özensiz bir host) 500'e dönüşmemeli.
	if code := a.call("PUT", path, root, map[string]any{"host_ids": []uuid.UUID{c1.ID, c1.ID}}, nil); code >= 500 {
		t.Errorf("duplicate host ids in one request -> %d, want a 2xx (deduplicated) or a 4xx", code)
	}
	if got := a.assignedHosts(root, opID); !sameSet(got, c1.ID) {
		t.Errorf("after the duplicate request: %v, want a single assignment", got)
	}

	// Bilinmeyen host: reddedilir ve önceki atama korunmalı (atomik değiştirme).
	a.expect(400, "PUT", path, root, map[string]any{"host_ids": []uuid.UUID{c1.ID, uuid.New()}}, nil)
	if got := a.assignedHosts(root, opID); !sameSet(got, c1.ID) {
		t.Errorf("a rejected PUT changed the assignment: %v", got)
	}
	a.expect(400, "PUT", path, root, map[string]any{"host_ids": []string{"nope"}}, nil)
	a.expect(400, "PUT", "/api/v1/users/not-a-uuid/hosts", root, map[string]any{"host_ids": []uuid.UUID{}}, nil)

	// Atamaları yalnızca super_admin'ler yönetir.
	orgAdminTok, _ := a.login("oa@x.test", "org_admin")
	opTok, _ := a.login("op2@x.test", "operator")
	for _, tok := range []string{orgAdminTok, opTok} {
		a.expect(403, "PUT", path, tok, map[string]any{"host_ids": []uuid.UUID{}}, nil)
		a.expect(403, "GET", path, tok, nil, nil)
		a.expect(403, "POST", path+"/by-organization", tok, map[string]any{"organization_id": org}, nil)
	}
}

func TestAssignAllHostsOfAnOrganization(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	_, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	a1, a2 := a.createPushHost(root, orgA, "a-1"), a.createPushHost(root, orgA, "a-2")
	b1 := a.createPushHost(root, orgB, "b-1")
	path := "/api/v1/users/" + opID.String() + "/hosts/by-organization"

	a.expect(204, "PUT", "/api/v1/users/"+opID.String()+"/hosts", root, map[string]any{"host_ids": []uuid.UUID{b1.ID}}, nil)

	var resp struct {
		Added int `json:"hosts_added"`
	}
	a.expect(200, "POST", path, root, map[string]any{"organization_id": orgA}, &resp)
	if resp.Added != 2 {
		t.Fatalf("hosts_added = %d, want 2", resp.Added)
	}
	// Başka yerlerdeki mevcut atamalara dokunulmaz; organizasyonun host'ları eklenir.
	if got := a.assignedHosts(root, opID); !sameSet(got, a1.ID, a2.ID, b1.ID) {
		t.Fatalf("assignments = %v, want a1, a2 and the untouched b1", got)
	}
	a.expect(200, "POST", path, root, map[string]any{"organization_id": orgA}, &resp)
	if resp.Added != 0 {
		t.Fatalf("second call added %d, want 0 (idempotent)", resp.Added)
	}

	// Bu bir anlık görüntüdür, kalıcı bir kural değil: sonradan oluşturulan host'lar otomatik atanmaz.
	late := a.createPushHost(root, orgA, "a-3")
	if got := a.assignedHosts(root, opID); sameSet(got, a1.ID, a2.ID, b1.ID, late.ID) {
		t.Fatal("a host created after the bulk assignment was assigned automatically")
	}
	a.expect(400, "POST", path, root, map[string]any{}, nil) // organization_id zorunlu
}

func TestOrgAdminOrganizationAssignmentToleratesDuplicates(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	_, adminID := a.login("oa@x.test", "org_admin")
	org := a.createOrg(root, "A")
	path := "/api/v1/users/" + adminID.String() + "/organizations"

	if code := a.call("PUT", path, root, map[string]any{"organization_ids": []uuid.UUID{org, org}}, nil); code >= 300 {
		t.Fatalf("duplicate organization ids -> %d, want success", code)
	}
	var orgs []idBody
	a.expect(200, "GET", path, root, nil, &orgs)
	if len(orgs) != 1 || orgs[0].ID != org {
		t.Fatalf("assigned organizations = %v, want exactly one", orgs)
	}
}

func TestThresholdLevelsAreValidated(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	create := func(metric string, warn, crit float64) int {
		return a.call("POST", "/api/v1/thresholds", root, map[string]any{"metric_type": metric, "warning_level": warn, "critical_level": crit}, nil)
	}

	for name, c := range map[string]struct {
		metric     string
		warn, crit float64
	}{
		"negative warning":       {"cpu", -1, 90},
		"negative critical":      {"docker_restart", 0, -5},
		"cpu above 100":          {"cpu", 80, 101},
		"ram above 100":          {"ram", 90, 150},
		"disk above 100":         {"disk", 50, 1000},
		"warning above critical": {"docker_restart", 10, 3},
		"absurd restart count":   {"docker_restart", 1, 5_000_000},
	} {
		if got := create(c.metric, c.warn, c.crit); got != 400 {
			t.Errorf("%s: %d, want 400", name, got)
		}
	}
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "docker_restart", "warning_level": 3, "critical_level": 10}, nil)
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 0, "critical_level": 100}, nil) // iki sınır da geçerli

	// Güncellemeler değişiklikten sonraki hâlleriyle doğrulanır.
	var list []struct {
		ID         uuid.UUID `json:"id"`
		MetricType string    `json:"metric_type"`
	}
	a.expect(200, "GET", "/api/v1/thresholds", root, nil, &list)
	ids := map[string]uuid.UUID{}
	for _, th := range list {
		ids[th.MetricType] = th.ID
	}
	dockerPath := "/api/v1/thresholds/" + ids["docker_restart"].String()
	cpuPath := "/api/v1/thresholds/" + ids["cpu"].String()
	a.expect(400, "PUT", dockerPath, root, map[string]any{"warning_level": 11}, nil) // 11 > mevcut critical 10
	a.expect(400, "PUT", cpuPath, root, map[string]any{"critical_level": 101}, nil)  // yüzde 100'ün üstü
	a.expect(400, "PUT", dockerPath, root, map[string]any{"warning_level": -2}, nil)
	a.expect(200, "PUT", dockerPath, root, map[string]any{"warning_level": 5, "critical_level": 20}, nil)

	// Seviyelere dokunmayan (boş) bir düzenleme, doğrulamadan önceki aralık dışı değerler yüzünden başarısız olmamalı.
	if _, err := a.pool.Exec(context.Background(), `UPDATE threshold_defaults SET critical_level = 150 WHERE id = $1`, ids["cpu"]); err != nil {
		t.Fatal(err)
	}
	a.expect(200, "PUT", cpuPath, root, map[string]any{}, nil)
}

// İnceleme bulgusu (M5) için regresyon: docker_restart eşikleri oluşturulabiliyordu ama hiç alert
// üretmiyordu; çünkü alert'ler host+metrik başına tekti ve bir container'ı adlandıramıyordu.
func TestDockerRestartAlertsEndToEndOverTheAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-host")
	hdr := []string{"X-Host-ID", c.ID.String()}
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "docker_restart", "warning_level": 3, "critical_level": 10}, nil)

	push := func(counts map[string]int) {
		var cs []map[string]any
		for n, r := range counts {
			cs = append(cs, map[string]any{"name": n, "image": n + ":1", "status": "running", "cpu_pct": 1, "ram_mb": 1, "restart_count": r, "uptime_seconds": 1})
		}
		a.expect(204, "POST", "/api/v1/metrics", c.APIToken, map[string]any{"cpu_usage_pct": 1, "ram_usage_pct": 1, "disk": []any{}, "docker_containers": cs}, nil, hdr...)
	}
	type alert struct {
		ID        uuid.UUID `json:"id"`
		AlertType string    `json:"alert_type"`
		Subject   string    `json:"subject"`
		Level     string    `json:"level"`
	}
	open := func() []alert {
		var out []alert
		a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &out)
		return out
	}

	push(map[string]int{"web": 1, "db": 0})
	if got := open(); len(got) != 0 {
		t.Fatalf("alerts below the threshold: %+v", got)
	}

	push(map[string]int{"web": 4, "db": 12})
	got := open()
	if len(got) != 2 {
		t.Fatalf("open alerts = %+v, want one per container", got)
	}
	byName := map[string]alert{}
	for _, al := range got {
		byName[al.Subject] = al
	}
	if byName["web"].AlertType != "docker_restart" || byName["web"].Level != "warning" || byName["db"].Level != "critical" {
		t.Fatalf("alerts = %+v", byName)
	}

	var summary struct {
		Critical int `json:"open_critical_alerts"`
		Warning  int `json:"open_warning_alerts"`
	}
	a.expect(200, "GET", "/api/v1/dashboard/summary", root, nil, &summary)
	if summary.Critical != 1 || summary.Warning != 1 {
		t.Fatalf("dashboard = %+v, want 1 critical + 1 warning", summary)
	}

	// Bir container'ın alert'ini onaylamak diğerine dokunmaz.
	a.expect(200, "POST", "/api/v1/alerts/"+byName["db"].ID.String()+"/acknowledge", root, nil, nil)
	if got := open(); len(got) != 1 || got[0].Subject != "web" {
		t.Fatalf("after acknowledging db: %+v", got)
	}

	// web yeniden oluşturulur (sayaç sıfırlanır): alert'i çözülür.
	push(map[string]int{"web": 0, "db": 12})
	for _, al := range open() {
		if al.Subject == "web" {
			t.Fatal("web's alert stayed open after the container was recreated")
		}
	}
}

// İşaretli bir kullanıcı (ör. bootstrap admin) giriş yapabilir, kendi profilini görebilir ve
// şifresini değiştirebilir — ve o yapana kadar başka hiçbir şey.
func TestMustChangePasswordGatesTheWholeAPIUntilChanged(t *testing.T) {
	a := newAPI(t)
	id := testdb.User(t, a.pool, "boot@x.test", "super_admin", password)
	if _, err := a.pool.Exec(context.Background(), `UPDATE users SET must_change_password = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	var login struct {
		tokenPair
		User struct {
			MustChange bool `json:"must_change_password"`
		} `json:"user"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "boot@x.test", "password": password}, &login)
	if !login.User.MustChange {
		t.Fatal("login did not tell the host the password must be changed")
	}
	tok := login.AccessToken

	// İzinli: kendi profili (bayrağı bildirir) ve şifre değişikliğinin kendisi.
	var me struct {
		MustChange bool `json:"must_change_password"`
	}
	a.expect(200, "GET", "/api/v1/me", tok, nil, &me)
	if !me.MustChange {
		t.Fatal("/me does not report must_change_password")
	}

	// Bunun dışındaki her şey — okumalar ve yazmalar, super_admin için bile — tanınabilir bir kodla reddedilir.
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/users"}, {"GET", "/api/v1/alerts"}, {"GET", "/api/v1/dashboard/summary"},
		{"GET", "/api/v1/organizations"}, {"POST", "/api/v1/organizations"}, {"GET", "/api/v1/me/hosts"},
		{"GET", "/api/v1/audit-logs"},
	} {
		var body struct {
			Code string `json:"code"`
		}
		if code := a.call(req.method, req.path, tok, map[string]any{}, &body); code != 403 || body.Code != "password_change_required" {
			t.Errorf("%s %s = %d code=%q, want 403 password_change_required", req.method, req.path, code, body.Code)
		}
	}
	// Kapı yenilemeden sağ çıkar: bayrak veritabanından yeniden okunur.
	var refreshed struct {
		tokenPair
	}
	a.expect(200, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": login.RefreshToken}, &refreshed)
	a.expect(403, "GET", "/api/v1/users", refreshed.AccessToken, nil, nil)

	// Şifreyi değiştirmek: yanlış mevcut şifre, zayıf/çok uzun/aynı yeni şifreler reddedilir.
	change := func(cur, next string, out any) int {
		return a.call("POST", "/api/v1/me/password", tok, map[string]string{"current_password": cur, "new_password": next}, out)
	}
	if got := change("wrong-current-password", "a-brand-new-password", nil); got != 403 {
		t.Errorf("wrong current password: %d, want 403 (not 401: the panel would log the user out)", got)
	}
	if got := change(password, "short", nil); got != 400 {
		t.Errorf("short new password: %d, want 400", got)
	}
	if got := change(password, strings.Repeat("x", 73), nil); got != 400 {
		t.Errorf("73-byte new password: %d, want 400", got)
	}
	if got := change(password, password, nil); got != 400 {
		t.Errorf("unchanged password: %d, want 400", got)
	}
	if got := a.call("POST", "/api/v1/me/password", tok, map[string]string{"current_password": password}, nil); got != 400 {
		t.Errorf("missing new_password: %d, want 400", got)
	}
	a.expect(403, "GET", "/api/v1/users", tok, nil, nil) // tüm başarısızlıklardan sonra hâlâ kapalı

	const newPassword = "a-brand-new-password"
	var changed struct {
		tokenPair
		User struct {
			MustChange bool `json:"must_change_password"`
		} `json:"user"`
	}
	if got := change(password, newPassword, &changed); got != 200 {
		t.Fatalf("password change = %d", got)
	}
	if changed.User.MustChange || changed.AccessToken == "" || changed.RefreshToken == "" {
		t.Fatalf("response after the change: %+v", changed)
	}

	// Yeni token'lar her yerde çalışır; eski oturum biter.
	a.expect(200, "GET", "/api/v1/users", changed.AccessToken, nil, nil)
	if code := a.refresh(login.RefreshToken, nil); code != 401 {
		t.Errorf("the pre-change refresh token still works (%d)", code)
	}
	if code := a.refresh(refreshed.RefreshToken, nil); code != 401 {
		t.Errorf("the refreshed pre-change session still works (%d)", code)
	}
	a.expect(200, "GET", "/api/v1/users", changed.AccessToken, nil, nil)

	// Yeni şifre giriş yapar, eskisi yapmaz ve kapı artık kalıcı olarak kalkmıştır.
	a.expect(401, "POST", "/api/v1/auth/login", "", map[string]string{"email": "boot@x.test", "password": password}, nil)
	var again struct {
		tokenPair
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "boot@x.test", "password": newPassword}, &again)
	a.expect(200, "GET", "/api/v1/users", again.AccessToken, nil, nil)
	if a.auditCount("auth.password_changed") != 1 {
		t.Errorf("auth.password_changed audit rows = %d, want 1", a.auditCount("auth.password_changed"))
	}
}

func TestAnyUserCanChangeTheirPasswordAndThatEndsTheirOtherSessions(t *testing.T) {
	a := newAPI(t)
	testdb.User(t, a.pool, "u@x.test", "operator", password)
	laptop, phone := a.loginPair("u@x.test"), a.loginPair("u@x.test")

	a.expect(401, "POST", "/api/v1/me/password", "", map[string]string{"current_password": password, "new_password": "another-new-password"}, nil)
	var changed tokenPair
	a.expect(200, "POST", "/api/v1/me/password", laptop.AccessToken,
		map[string]string{"current_password": password, "new_password": "another-new-password"}, &changed)

	if code := a.refresh(phone.RefreshToken, nil); code != 401 {
		t.Errorf("another device's session survived a password change (%d)", code)
	}
	if code := a.refresh(changed.RefreshToken, nil); code != 200 {
		t.Errorf("the session issued with the change should work (%d)", code)
	}
}

// Yanlış "mevcut şifre" denemeleri başarısız girişlerle aynı bütçeden düşer; böylece çalınmış
// bir access token hesap şifresini kaba kuvvetle denemek için kullanılamaz.
func TestChangePasswordGuessesAreRateLimited(t *testing.T) {
	pool := testdb.New(t)
	tokens := authsvc.NewTokenService([]byte(strings.Repeat("a", 32)), []byte(strings.Repeat("r", 32)), 15*time.Minute, time.Hour)
	deps := httpapi.NewDeps(pool, tokens, alertengine.New(pool, notify.New(notify.Config{})),
		httpapi.RateLimits{AuthFailuresPerMinute: 60, IngestPerMinute: 60}, testdb.SecretBox(t))
	a := &api{t: t, pool: pool, handler: deps.Router(), tokens: tokens}
	testdb.User(t, pool, "u@x.test", "operator", password)
	tok := a.loginPair("u@x.test").AccessToken

	got := map[int]int{}
	for i := 0; i < 14; i++ {
		got[a.call("POST", "/api/v1/me/password", tok,
			map[string]string{"current_password": fmt.Sprintf("wrong-guess-%d-aaaa", i), "new_password": "some-new-password-1"}, nil)]++
	}
	if got[403] != 10 || got[429] != 4 {
		t.Fatalf("status counts = %v, want 10x 403 then 429s (burst of 10 failures)", got)
	}
}

func TestPasswordPolicyIsEnforcedWhereverPasswordsAreSet(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	victim := testdb.User(t, a.pool, "v@x.test", "operator", password)
	create := func(pw string) int {
		return a.call("POST", "/api/v1/users", root, map[string]any{"email": fmt.Sprintf("n%d@x.test", len(pw)), "password": pw, "role": "operator"}, nil)
	}
	if got := create("elevenchars"); got != 400 { // 11 karakter
		t.Errorf("11-char password on create: %d, want 400", got)
	}
	// Regresyon: 73+ bayt eskiden bcrypt'e ulaşıp 500 olarak dönüyordu.
	if got := create(strings.Repeat("a", 73)); got != 400 {
		t.Errorf("73-byte password on create: %d, want 400", got)
	}
	if got := create("twelve-chars"); got != 201 {
		t.Errorf("12-char password on create: %d, want 201", got)
	}
	path := "/api/v1/users/" + victim.String()
	a.expect(400, "PUT", path, root, map[string]any{"password": "short"}, nil)
	a.expect(400, "PUT", path, root, map[string]any{"password": strings.Repeat("b", 100)}, nil)
	a.expect(200, "PUT", path, root, map[string]any{"password": "long-enough-again"}, nil)
}

// ---- hangi mount'ların disk alert'i üretebileceğini seçmek ------------------------------------------------

type diskSettings struct {
	AllMounts bool     `json:"all_mounts_alert"`
	Custom    []string `json:"custom_alert_mounts"`
	Reported  []struct {
		Mount   string  `json:"mount"`
		UsedPct float64 `json:"used_pct"`
	} `json:"reported"`
}

func (a *api) pushDisks(c createdHost, pcts map[string]float64) {
	a.t.Helper()
	var disks []map[string]any
	for m, p := range pcts {
		disks = append(disks, map[string]any{"mount": m, "used_pct": p, "total": 1000, "free": 100})
	}
	a.expect(204, "POST", "/api/v1/metrics", c.APIToken,
		map[string]any{"cpu_usage_pct": 1, "ram_usage_pct": 1, "disk": disks, "docker_containers": []any{}}, nil, "X-Host-ID", c.ID.String())
}

func TestChoosingWhichMountsRaiseDiskAlertsEndToEnd(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "disk", "warning_level": 85, "critical_level": 95}, nil)
	path := "/api/v1/hosts/" + c.ID.String() + "/disk-alerts"

	// On mount, üçü eşiğin üstünde.
	report := map[string]float64{"/": 40, "/boot": 30, "/data": 96, "/var/lib/docker": 97, "/mnt/backup": 99, "/srv": 20}
	a.pushDisks(c, report)

	// Panel raporlananı ve henüz hiçbir şeyin seçilmediğini (= her mount) görür.
	var s diskSettings
	a.expect(200, "GET", path, root, nil, &s)
	if !s.AllMounts || len(s.Custom) != 0 || len(s.Reported) != 6 {
		t.Fatalf("settings = %+v, want all_mounts_alert=true and 6 reported mounts", s)
	}
	openDisk := func() map[string]string {
		var alerts []struct {
			AlertType string `json:"alert_type"`
			Subject   string `json:"subject"`
			Level     string `json:"level"`
		}
		a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &alerts)
		out := map[string]string{}
		for _, al := range alerts {
			if al.AlertType == "disk" {
				out[al.Subject] = al.Level
			}
		}
		return out
	}
	if got := openDisk(); len(got) != 3 {
		t.Fatalf("with nothing selected every over-threshold mount alerts: %v", got)
	}

	// "Benim için yalnızca / ve /data önemli."
	a.expect(200, "PUT", path, root, map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/", "/data"}}, &s)
	if s.AllMounts || len(s.Custom) != 2 {
		t.Fatalf("PUT response = %+v", s)
	}
	a.pushDisks(c, report) // sonraki rapor onu uygular
	got := openDisk()
	if len(got) != 1 || got["/data"] != "critical" {
		t.Fatalf("open disk alerts = %v, want only /data: /var/lib/docker and /mnt/backup are unselected and their alerts closed", got)
	}

	// "Hiçbiri": bu host için disk alert'leri kapalı.
	a.expect(200, "PUT", path, root, map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{}}, &s)
	a.pushDisks(c, report)
	if got := openDisk(); len(got) != 0 {
		t.Fatalf("alerts with an empty selection: %v", got)
	}

	// all_mounts_alert=true: raporlanan her mount'a geri dön.
	var raw map[string]json.RawMessage
	a.expect(200, "PUT", path, root, map[string]any{"all_mounts_alert": true}, &raw)
	if string(raw["all_mounts_alert"]) != "true" {
		t.Fatalf("all_mounts_alert is %s, want true", raw["all_mounts_alert"])
	}
	a.pushDisks(c, report)
	if got := openDisk(); len(got) != 3 {
		t.Fatalf("after going back to 'all': %v", got)
	}
	if a.auditCount("host.update_disk_alerts") != 3 {
		t.Errorf("audit rows = %d, want 3", a.auditCount("host.update_disk_alerts"))
	}
}

func TestDiskAlertSettingsValidationAndTriState(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	path := "/api/v1/hosts/" + c.ID.String() + "/disk-alerts"
	put := func(body any) int { return a.call("PUT", path, root, body, nil) }

	// all_mounts_alert zorunludur: bir yazım hatası sessizce "tüm mount'lar" anlamına gelmemeli ve yanıt
	// çağırana bunu nasıl söyleyeceğini anlatmalı (yalnızca "bad request" değil).
	for name, body := range map[string]any{"missing key": map[string]any{}} {
		var e struct {
			Error string `json:"error"`
		}
		if code := a.call("PUT", path, root, body, &e); code != 400 || !strings.Contains(e.Error, `"all_mounts_alert" zorunlu`) {
			t.Errorf("%s: %d %q, want 400 explaining that all_mounts_alert is required", name, code, e.Error)
		}
	}
	for name, body := range map[string]any{
		"misspelled key":   map[string]any{"monitor": []string{"/"}},
		"wrong type":       map[string]any{"monitored": "/data"},
		"number":           map[string]any{"monitored": 5},
		"object":           map[string]any{"monitored": map[string]any{}},
		"array of numbers": map[string]any{"monitored": []int{1}},
		"relative path":    map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"data"}},
		"duplicate":        map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/a", "/a"}},
		"control char":     map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/a\u0000b"}},
	} {
		if got := put(body); got != 400 {
			t.Errorf("%s: %d, want 400", name, got)
		}
	}
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("/m%d", i)
	}
	if got := put(map[string]any{"all_mounts_alert": false, "custom_alert_mounts": tooMany}); got != 400 {
		t.Errorf("65 mounts: %d, want 400", got)
	}
	a.expect(400, "PUT", "/api/v1/hosts/not-a-uuid/disk-alerts", root, map[string]any{"all_mounts_alert": true}, nil)
	a.expect(404, "PUT", "/api/v1/hosts/"+uuid.NewString()+"/disk-alerts", root, map[string]any{"all_mounts_alert": true}, nil)
	a.expect(404, "GET", "/api/v1/hosts/"+uuid.NewString()+"/disk-alerts", root, nil, nil)

	// Reddedilen tüm denemelerden sonra saklı seçim hâlâ dokunulmamış (tüm mount'lar).
	var s diskSettings
	a.expect(200, "GET", path, root, nil, &s)
	if !s.AllMounts || len(s.Custom) != 0 {
		t.Fatalf("a rejected request changed the selection: %+v", s)
	}
	a.expect(200, "PUT", path, root, map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/mnt/My Disk", "/data"}}, &s)
	if s.AllMounts || len(s.Custom) != 2 || s.Custom[0] != "/data" || s.Custom[1] != "/mnt/My Disk" {
		t.Fatalf("paths with spaces: %+v (list is returned sorted)", s)
	}

	// Host'ın kendisi de seçimi bildirir.
	var cl struct {
		All    bool     `json:"all_mounts_alert"`
		Mounts []string `json:"custom_alert_mounts"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String(), root, nil, &cl)
	if cl.All || len(cl.Mounts) != 2 {
		t.Fatalf("GET /hosts/:id disk selection = %+v", cl)
	}
}

func TestDiskAlertSettingsPermissions(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	otherOp, _ := a.login("op2@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	mine := a.createPushHost(root, orgA, "a-1")
	theirs := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, mine.ID)
	body := map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/"}}
	p := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/disk-alerts" }

	// super_admin ve sahibi org_admin bunu değiştirebilir.
	a.expect(200, "PUT", p(mine), root, body, nil)
	a.expect(200, "PUT", p(mine), adminTok, body, nil)
	// ...ama başka bir organizasyonun host'ı için değil.
	a.expect(403, "PUT", p(theirs), adminTok, body, nil)
	a.expect(403, "GET", p(theirs), adminTok, nil, nil)
	// Operatörler kendi host'larının ayarlarını okuyabilir ve hiçbir şeyi değiştiremez.
	a.expect(200, "GET", p(mine), opTok, nil, nil)
	a.expect(403, "PUT", p(mine), opTok, body, nil)
	a.expect(403, "GET", p(theirs), opTok, nil, nil)
	a.expect(403, "GET", p(mine), otherOp, nil, nil) // ataması olmayan bir operatör
	a.expect(401, "GET", p(mine), "", nil, nil)
	a.expect(401, "PUT", p(mine), "", body, nil)
}

func TestCreatingAHostWithADiskSelection(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	disk := 0
	create := func(extra map[string]any) (int, createdHost, map[string]json.RawMessage) {
		disk++
		body := map[string]any{"organization_id": org, "title": fmt.Sprintf("h%d", disk), "ip": "10.0.0.1", "mode": "push", "interval_seconds": 10}
		for k, v := range extra {
			body[k] = v
		}
		var cc createdHost
		var raw map[string]json.RawMessage
		code := a.call("POST", "/api/v1/hosts", root, body, &raw)
		if code == 201 {
			b, _ := json.Marshal(raw)
			json.Unmarshal(b, &cc)
		}
		return code, cc, raw
	}

	if code, _, raw := create(nil); code != 201 || string(raw["all_mounts_alert"]) != "true" {
		t.Fatalf("default: %d all_mounts_alert=%s, want 201 and true (all reported mounts)", code, raw["all_mounts_alert"])
	}
	if code, _, raw := create(map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/", "/data"}}); code != 201 ||
		string(raw["all_mounts_alert"]) != "false" || string(raw["custom_alert_mounts"]) != `["/","/data"]` {
		t.Fatalf("with a selection: %d %s %s", code, raw["all_mounts_alert"], raw["custom_alert_mounts"])
	}
	if code, _, raw := create(map[string]any{"all_mounts_alert": false}); code != 201 || string(raw["all_mounts_alert"]) != "false" || string(raw["custom_alert_mounts"]) != "[]" {
		t.Fatalf("with none: %d %s %s", code, raw["all_mounts_alert"], raw["custom_alert_mounts"])
	}
	for name, v := range map[string]any{"relative": []string{"data"}, "duplicate": []string{"/a", "/a"}, "wrong type": "/data"} {
		if code, _, _ := create(map[string]any{"all_mounts_alert": false, "custom_alert_mounts": v}); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
}

// Yöneticinin oluşturduğu hesabın ilk şifresini yönetici bilir: hesap sahibi ilk girişte kendi
// şifresini seçmeden başka hiçbir şey yapamaz.
func TestAccountsCreatedByAnAdministratorMustChooseTheirOwnPasswordFirst(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")

	var created struct {
		ID         uuid.UUID `json:"id"`
		MustChange bool      `json:"must_change_password"`
	}
	a.expect(201, "POST", "/api/v1/users", root, map[string]any{"email": "new@x.test", "password": "temporary-pass-123", "role": "operator"}, &created)
	if !created.MustChange {
		t.Fatal("the create response does not say the new account must change its password")
	}

	var login struct {
		tokenPair
		User struct {
			MustChange bool `json:"must_change_password"`
		} `json:"user"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "new@x.test", "password": "temporary-pass-123"}, &login)
	if !login.User.MustChange {
		t.Fatal("login did not tell the panel to send the user to the password form")
	}
	var gated struct {
		Code string `json:"code"`
	}
	if code := a.call("GET", "/api/v1/me/hosts", login.AccessToken, nil, &gated); code != 403 || gated.Code != "password_change_required" {
		t.Fatalf("GET /me/hosts before changing the password = %d code=%q, want 403 password_change_required", code, gated.Code)
	}

	a.expect(200, "POST", "/api/v1/me/password", login.AccessToken, map[string]string{"current_password": "temporary-pass-123", "new_password": "my-own-password-456"}, &login)
	a.expect(200, "GET", "/api/v1/me/hosts", login.AccessToken, nil, nil)
	var again struct {
		User struct {
			MustChange bool `json:"must_change_password"`
		} `json:"user"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "new@x.test", "password": "my-own-password-456"}, &again)
	if again.User.MustChange {
		t.Fatal("the flag survived the user choosing their own password")
	}

	// Şifresini seçmiş bir kullanıcıya yönetici şifre sıfırlayınca bayrak bilerek geri gelmez
	// (SetOwnPassword/UpdatePassword ayrımı).
	a.expect(200, "PUT", "/api/v1/users/"+created.ID.String(), root, map[string]any{"password": "reset-by-admin-789"}, nil)
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "new@x.test", "password": "reset-by-admin-789"}, &again)
	if again.User.MustChange {
		t.Fatal("an administrator resetting a password must not change the existing policy for that account")
	}
}

// Agent'ın bildirdiği donanım toplamları hosts'ta "son bilinen değer" olarak saklanır: yeni
// değer üzerine yazılır, 0/eksik ("bilinmiyor") ise eski değer korunur, negatif değer reddedilir.
func TestIngestStoresHardwareTotalsAsLastKnownValue(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "hw-1")

	type totals struct {
		CPUCores   *int   `json:"cpu_cores"`
		RAMTotalMB *int64 `json:"ram_total_mb"`
	}
	read := func() totals {
		t.Helper()
		var got totals
		a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String(), root, nil, &got)
		return got
	}
	withTotals := func(cores int, ramMB int64) map[string]any {
		m := metrics(10, 20)
		m["cpu_cores"], m["ram_total_mb"] = cores, ramMB
		return m
	}

	// Henüz toplam bildiren bir rapor yok: alanlar boş (panel yalnızca yüzdeyi gösterir).
	if got := read(); got.CPUCores != nil || got.RAMTotalMB != nil {
		t.Fatalf("before any report = %+v, want nil totals", got)
	}

	if code := a.push(c, c.APIToken, withTotals(8, 16000)); code != 204 {
		t.Fatalf("push with totals = %d", code)
	}
	if got := read(); got.CPUCores == nil || *got.CPUCores != 8 || got.RAMTotalMB == nil || *got.RAMTotalMB != 16000 {
		t.Fatalf("after first report = %+v, want 8 cores / 16000 MB", got)
	}

	// Eski bir agent alanları hiç göndermez: son bilinen değer silinmemeli.
	if code := a.push(c, c.APIToken, metrics(10, 20)); code != 204 {
		t.Fatalf("push without totals = %d", code)
	}
	if got := read(); got.CPUCores == nil || *got.CPUCores != 8 || got.RAMTotalMB == nil || *got.RAMTotalMB != 16000 {
		t.Fatalf("after report without totals = %+v, want last known values kept", got)
	}

	// Makine büyütüldü: yeni değer eskinin üzerine yazar.
	if code := a.push(c, c.APIToken, withTotals(16, 32000)); code != 204 {
		t.Fatalf("push with new totals = %d", code)
	}
	if got := read(); *got.CPUCores != 16 || *got.RAMTotalMB != 32000 {
		t.Fatalf("after resize = %+v, want 16 cores / 32000 MB", got)
	}

	// Negatif değer 400 olmalı ve saklanmamalı.
	if code := a.push(c, c.APIToken, withTotals(-1, 32000)); code != 400 {
		t.Fatalf("push with negative cpu_cores = %d, want 400", code)
	}
	if code := a.push(c, c.APIToken, withTotals(16, -5)); code != 400 {
		t.Fatalf("push with negative ram_total_mb = %d, want 400", code)
	}
	if got := read(); *got.CPUCores != 16 || *got.RAMTotalMB != 32000 {
		t.Fatalf("after rejected reports = %+v, want unchanged", got)
	}
}

// Fiziksel diskler de "son bilinen değer" kuralına uyar: yeni liste eskisini değiştirir, boş/eksik
// olan onu korur. Bir mount birden çok diskin listesinde olabilir (LVM) ve bozuk girdiler tüm
// metrik alımını düşürmez, temizlenir.
func TestIngestStoresPhysicalDisksAsLastKnownValue(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "disks-1")

	type disk struct {
		Name      string   `json:"name"`
		Model     string   `json:"model"`
		SizeBytes int64    `json:"size_bytes"`
		Kind      string   `json:"kind"`
		Mounts    []string `json:"mounts"`
	}
	read := func() []disk {
		t.Helper()
		var got struct {
			PhysicalDisks []disk `json:"physical_disks"`
		}
		a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String(), root, nil, &got)
		return got.PhysicalDisks
	}
	withDisks := func(disks any) map[string]any {
		m := metrics(10, 20)
		m["physical_disks"] = disks
		return m
	}

	if got := read(); got != nil {
		t.Fatalf("before any report = %+v, want none", got)
	}

	first := []map[string]any{
		{"name": "nvme0n1", "model": "CT500P2SSD8", "size_bytes": 500107862016, "kind": "nvme", "mounts": []string{"/", "/boot/efi"}},
	}
	if code := a.push(c, c.APIToken, withDisks(first)); code != 204 {
		t.Fatalf("push with disks = %d", code)
	}
	got := read()
	if len(got) != 1 || got[0].Name != "nvme0n1" || got[0].Model != "CT500P2SSD8" || got[0].SizeBytes != 500107862016 ||
		got[0].Kind != "nvme" || len(got[0].Mounts) != 2 || got[0].Mounts[0] != "/" || got[0].Mounts[1] != "/boot/efi" {
		t.Fatalf("after first report = %+v", got)
	}

	// Eski agent (alan yok) ya da keşfedilemeyen disk (boş liste): son bilinen değer silinmemeli.
	for name, body := range map[string]map[string]any{"absent": metrics(10, 20), "empty": withDisks([]any{})} {
		if code := a.push(c, c.APIToken, body); code != 204 {
			t.Fatalf("push (%s) = %d", name, code)
		}
		if got := read(); len(got) != 1 || got[0].Name != "nvme0n1" {
			t.Fatalf("after %s report = %+v, want last known kept", name, got)
		}
	}

	// Yeni liste eskisini tümüyle değiştirir; LVM'de aynı mount iki diskin altında görünür.
	lvm := []map[string]any{
		{"name": "sda", "size_bytes": 2000, "kind": "hdd", "mounts": []string{"/srv"}},
		{"name": "sdb", "size_bytes": 4000, "kind": "ssd", "mounts": []string{"/srv", "/data"}},
	}
	if code := a.push(c, c.APIToken, withDisks(lvm)); code != 204 {
		t.Fatalf("push with new disks = %d", code)
	}
	if got := read(); len(got) != 2 || got[0].Name != "sda" || got[1].Name != "sdb" || len(got[1].Mounts) != 2 {
		t.Fatalf("after replace = %+v, want sda + sdb", got)
	}

	// Bozuk girdi (denetim karakteri, adsız/negatif boyutlu disk) metrik alımını reddettirmemeli.
	hostile := []map[string]any{
		{"name": "sdc\u0000", "model": "M\u0000odel", "size_bytes": -1, "mounts": nil},
		{"name": "", "mounts": []string{"/x"}},
	}
	if code := a.push(c, c.APIToken, withDisks(hostile)); code != 204 {
		t.Fatalf("push with hostile disks = %d, want 204 (sanitized, not rejected)", code)
	}
	if got := read(); len(got) != 1 || got[0].Name != "sdc" || got[0].Model != "Model" || got[0].SizeBytes != 0 || len(got[0].Mounts) != 0 {
		t.Fatalf("after hostile report = %+v, want one cleaned disk sdc", got)
	}
}

// Her agent sürümünün gerçek gövdesi (testdata/payloads) kabul edilmeli ve çekirdek metrikler
// saklanmalı — "future" fixture'ı bu server'ın tanımadığı alanlar taşır ve YİNE 204 almalıdır
// (bkz. docs/COMPATIBILITY.md). Bu test, ingest'in katı decode'a geri dönmesini yakalar.
func TestIngestAcceptsEveryGoldenPayload(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")

	files, err := filepath.Glob("../../testdata/payloads/*.json")
	if err != nil || len(files) < 3 {
		t.Fatalf("golden payloads: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var want struct {
			CPU float64 `json:"cpu_usage_pct"`
			RAM float64 `json:"ram_usage_pct"`
		}
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		c := a.createPushHost(root, org, "golden-"+strings.TrimSuffix(name, ".json"))
		if code := a.push(c, c.APIToken, json.RawMessage(raw)); code != 204 {
			t.Errorf("%s: push = %d, want 204", name, code)
			continue
		}
		var cpu, ram float64
		err = a.pool.QueryRow(context.Background(),
			`SELECT cpu_usage_pct, ram_usage_pct FROM metrics WHERE host_id = $1 ORDER BY recorded_at DESC LIMIT 1`, c.ID).Scan(&cpu, &ram)
		if err != nil || cpu != want.CPU || ram != want.RAM {
			t.Errorf("%s: stored cpu=%v ram=%v (err %v), want %v/%v", name, cpu, ram, err, want.CPU, want.RAM)
		}
	}
}

// Liberal kabul, bozuk gövdeleri kabul etmek demek değildir.
func TestIngestStillRejectsMalformedBodies(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "bad-1")
	for name, body := range map[string]any{
		"null":            rawBody(`null`),
		"array":           rawBody(`[]`),
		"broken json":     rawBody(`{"cpu_usage_pct":`),
		"core wrong type": rawBody(`{"cpu_usage_pct":"x","ram_usage_pct":1}`),
		"out of range":    map[string]any{"cpu_usage_pct": 150, "ram_usage_pct": 1},
	} {
		if code := a.push(c, c.APIToken, body); code != 400 {
			t.Errorf("%s: push = %d, want 400", name, code)
		}
	}
}

// Agent sürümü başlıktan okunur ve donanımın aksine HER istekte yazılır: sürüm bildirmeyen
// (eski) bir agent'a geri dönülürse panel eski sürümü göstermeye devam etmemeli.
func TestIngestRecordsAgentVersionFromHeaders(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "ver-1")

	type agent struct {
		Version  *string `json:"agent_version"`
		Protocol *int    `json:"agent_protocol"`
	}
	read := func() agent {
		t.Helper()
		var got agent
		a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String(), root, nil, &got)
		return got
	}
	pushWith := func(headers ...string) {
		t.Helper()
		h := append([]string{"X-Host-ID", c.ID.String()}, headers...)
		a.expect(204, "POST", "/api/v1/metrics", c.APIToken, metrics(10, 20), nil, h...)
	}

	if got := read(); got.Version != nil || got.Protocol != nil {
		t.Fatalf("before any report = %+v, want nothing", got)
	}

	pushWith("User-Agent", "healthbeat-agent/1.1.0", "X-HealthBeat-Protocol", "2")
	if got := read(); got.Version == nil || *got.Version != "1.1.0" || got.Protocol == nil || *got.Protocol != 2 {
		t.Fatalf("after versioned push = %+v, want 1.1.0 / 2", got)
	}

	// Yeni sürüm: üzerine yazılır.
	pushWith("User-Agent", "healthbeat-agent/1.2.0", "X-HealthBeat-Protocol", "3")
	if got := read(); *got.Version != "1.2.0" || *got.Protocol != 3 {
		t.Fatalf("after upgrade = %+v", got)
	}

	// Başlıksız (sürüm bildirmeyen eski agent): sürüm SİLİNİR, protokol 1 olur.
	pushWith()
	if got := read(); got.Version != nil || got.Protocol == nil || *got.Protocol != 1 {
		t.Fatalf("after legacy push = version %v protocol %v, want nil / 1", got.Version, got.Protocol)
	}

	// Bozuk/düşmanca başlık metrik alımını düşürmez, yalnız sürümü atar.
	pushWith("User-Agent", "healthbeat-agent/1.0; DROP TABLE hosts", "X-HealthBeat-Protocol", "zzz")
	if got := read(); got.Version != nil || *got.Protocol != 1 {
		t.Fatalf("after hostile headers = %+v", got)
	}
}

// /meta: server sürümü ve agent sürüm politikası; oturum gerektirir, hassas bir şey içermez.
func TestMetaEndpointReportsVersionPolicy(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	a.expect(401, "GET", "/api/v1/meta", "", nil, nil)

	var m struct {
		ServerVersion string `json:"server_version"`
		Protocol      int    `json:"protocol"`
		Latest        string `json:"latest_agent_version"`
		Min           string `json:"min_agent_version"`
	}
	a.expect(200, "GET", "/api/v1/meta", root, nil, &m)
	if m.ServerVersion != version.Version || m.Protocol != version.Protocol || m.Latest != "" || m.Min != "" {
		t.Errorf("default meta = %+v (policy is unset in tests)", m)
	}

	a.deps.SetAgentPolicy(httpapi.AgentPolicy{Latest: "1.5.0", Min: "1.2.0"})
	a.expect(200, "GET", "/api/v1/meta", root, nil, &m)
	if m.Latest != "1.5.0" || m.Min != "1.2.0" {
		t.Errorf("meta after SetAgentPolicy = %+v", m)
	}

	// Sıradan bir kullanıcı da okuyabilir.
	op, _ := a.login("op@x.test", "operator")
	a.expect(200, "GET", "/api/v1/meta", op, nil, nil)
}

// Özet uç noktası host başına agent sürümünü de taşır (panel "güncellenmesi gerekenler"i süzer).
func TestOverviewCarriesAgentVersion(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "ov-1")
	a.expect(204, "POST", "/api/v1/metrics", c.APIToken, metrics(1, 1), nil,
		"X-Host-ID", c.ID.String(), "User-Agent", "healthbeat-agent/1.1.0", "X-HealthBeat-Protocol", "2")

	var ov struct {
		Hosts []struct {
			ID       uuid.UUID `json:"id"`
			Version  *string   `json:"agent_version"`
			Protocol *int      `json:"agent_protocol"`
		} `json:"hosts"`
	}
	a.expect(200, "GET", "/api/v1/dashboard/overview", root, nil, &ov)
	if len(ov.Hosts) != 1 || ov.Hosts[0].Version == nil || *ov.Hosts[0].Version != "1.1.0" || *ov.Hosts[0].Protocol != 2 {
		t.Errorf("overview hosts = %+v", ov.Hosts)
	}
}

// Server'ın tanımadığı alanlar (gelecekteki bir agent'tan) reddedilmez ama kaydedilir: panel
// "agent server'dan yeni, server'ı güncelleyin" diyebilsin. Her raporda yazılır; alan tanınınca
// ya da agent onu bırakınca kayıt temizlenir.
func TestIngestRecordsUnsupportedFieldNames(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "future-1")

	read := func() []string {
		t.Helper()
		var got struct {
			Fields []string `json:"unsupported_fields"`
		}
		a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String(), root, nil, &got)
		return got.Fields
	}
	post := func(file string) {
		t.Helper()
		raw, err := os.ReadFile("../../testdata/payloads/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if code := a.push(c, c.APIToken, json.RawMessage(raw)); code != 204 {
			t.Fatalf("%s: push = %d", file, code)
		}
	}

	if got := read(); got != nil {
		t.Fatalf("before any report = %v", got)
	}
	post("v99_future.json")
	if got := read(); !reflect.DeepEqual(got, []string{"agent_notes", "gpu", "load_average"}) {
		t.Fatalf("after future payload = %v, want [agent_notes gpu load_average]", got)
	}
	post("v2_hardware.json") // bilinmeyen alan yok: kayıt temizlenir
	if got := read(); got != nil {
		t.Fatalf("after a payload without unknown fields = %v, want cleared", got)
	}
}

// Ingest yanıtı server sürümünü bildirir (400 dahil): agent karşısındaki server'ı tanır.
func TestIngestResponseAnnouncesServerVersion(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "hdr-1")

	do := func(body string) http.Header {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/v1/metrics", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+c.APIToken)
		req.Header.Set("X-Host-ID", c.ID.String())
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, req)
		return rec.Header()
	}
	check := func(name string, h http.Header, wantLatest string) {
		t.Helper()
		if got := h.Get(version.HeaderServerVersion); got != version.Version {
			t.Errorf("%s: %s = %q, want %q", name, version.HeaderServerVersion, got, version.Version)
		}
		if got := h.Get(version.HeaderProtocol); got != strconv.Itoa(version.Protocol) {
			t.Errorf("%s: %s = %q, want %d", name, version.HeaderProtocol, got, version.Protocol)
		}
		if got := h.Get(version.HeaderLatestAgent); got != wantLatest {
			t.Errorf("%s: %s = %q, want %q", name, version.HeaderLatestAgent, got, wantLatest)
		}
	}

	check("204, no policy", do(`{"cpu_usage_pct":1,"ram_usage_pct":1}`), "")
	check("400", do(`{"cpu_usage_pct":`), "")
	a.deps.SetAgentPolicy(httpapi.AgentPolicy{Latest: "1.5.0"})
	check("204, with policy", do(`{"cpu_usage_pct":1,"ram_usage_pct":1}`), "1.5.0")
}

// Makine envanteri (host_info) da "son bilinen değer" kuralına uyar: yeni rapor tümüyle değiştirir,
// eksik/boş/tümüyle geçersiz rapor mevcut kaydı korur, bozuk girdi tüm metrik alımını düşürmez.
func TestIngestStoresHostInfoAsLastKnownValue(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "inv-1")

	type hostInfo struct {
		Hostname string `json:"hostname"`
		OS       *struct {
			PrettyName string `json:"pretty_name"`
		} `json:"os"`
		Kernel *struct {
			Release string `json:"release"`
			Arch    string `json:"arch"`
		} `json:"kernel"`
		Addresses []struct {
			Interface string `json:"interface"`
			Address   string `json:"address"`
		} `json:"addresses"`
		UptimeSeconds  int64  `json:"uptime_seconds"`
		RebootRequired *bool  `json:"reboot_required"`
		FailedUnits    *int   `json:"failed_units"`
		SecurityModule string `json:"security_module"`
	}
	read := func() *hostInfo {
		t.Helper()
		var got struct {
			HostInfo *hostInfo `json:"host_info"`
		}
		a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String(), root, nil, &got)
		return got.HostInfo
	}
	push := func(host any) int {
		m := metrics(10, 20)
		if host != nil {
			m["host_info"] = host
		}
		return a.push(c, c.APIToken, m)
	}

	if got := read(); got != nil {
		t.Fatalf("before any report = %+v, want none", got)
	}

	full := map[string]any{
		"hostname": "monster",
		"os":       map[string]any{"id": "ubuntu", "pretty_name": "Ubuntu 24.04.5 LTS"},
		"kernel":   map[string]any{"release": "7.0.0-31-generic", "arch": "x86_64"},
		"addresses": []map[string]any{
			{"interface": "enp3s0", "address": "192.168.1.106/24"},
			{"interface": "enp3s0", "address": "GEÇERSİZ"},
		},
		"uptime_seconds": 18034, "reboot_required": false, "failed_units": 0, "security_module": "apparmor",
	}
	if code := push(full); code != 204 {
		t.Fatalf("push with host_info = %d", code)
	}
	got := read()
	if got == nil || got.Hostname != "monster" || got.OS == nil || got.OS.PrettyName != "Ubuntu 24.04.5 LTS" || got.Kernel == nil || got.Kernel.Arch != "x86_64" ||
		got.UptimeSeconds != 18034 || got.SecurityModule != "apparmor" {
		t.Fatalf("after first report = %+v", got)
	}
	if len(got.Addresses) != 1 || got.Addresses[0].Address != "192.168.1.106/24" {
		t.Errorf("addresses = %+v, want the invalid one dropped", got.Addresses)
	}
	if got.RebootRequired == nil || *got.RebootRequired || got.FailedUnits == nil || *got.FailedUnits != 0 {
		t.Errorf("false/0 must be stored, not treated as unknown: reboot=%v failed=%v", got.RebootRequired, got.FailedUnits)
	}

	// Eski agent (alan yok), boş nesne ve tümüyle geçersiz nesne: son bilinen değer korunur.
	for name, host := range map[string]any{"absent": nil, "empty": map[string]any{}, "only garbage": map[string]any{"hostname": "\u0000", "addresses": []map[string]any{{"address": "x"}}}} {
		if code := push(host); code != 204 {
			t.Fatalf("push (%s) = %d", name, code)
		}
		if got := read(); got == nil || got.Hostname != "monster" {
			t.Fatalf("after %s report = %+v, want last known kept", name, got)
		}
	}

	// Yeni rapor TÜMÜYLE değiştirir: eski os/kernel/adresler kalmaz.
	if code := push(map[string]any{"hostname": "renamed", "uptime_seconds": 5}); code != 204 {
		t.Fatalf("push replacement = %d", code)
	}
	if got := read(); got == nil || got.Hostname != "renamed" || got.OS != nil || got.Kernel != nil || len(got.Addresses) != 0 || got.UptimeSeconds != 5 {
		t.Fatalf("after replacement = %+v, want a complete replacement", got)
	}

	// Düşmanca girdi metrik alımını (204) düşürmez, temizlenir.
	hostile := map[string]any{
		"hostname": "ho\u0000st", "virtualization": map[string]any{"kind": "hologram"},
		"load_avg": []float64{1e12, 1, 1}, "swap": map[string]any{"total_mb": -1, "used_mb": 5},
		"boot_time": "not a time", "machine_id_hash": "zz",
	}
	if code := push(hostile); code != 204 {
		t.Fatalf("push with hostile host_info = %d, want 204 (sanitized, not rejected)", code)
	}
	if got := read(); got == nil || got.Hostname != "host" {
		t.Fatalf("after hostile report = %+v", got)
	}

	// Metrikler her durumda yazıldı.
	var n int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM metrics WHERE host_id = $1`, c.ID).Scan(&n); err != nil || n != 6 {
		t.Errorf("metrics rows = %d (err %v), want all 6 pushes stored", n, err)
	}
}

// Disk girdisindeki inode yüzdesi saklanır ve geçmişte döner; aralık dışı değer 400 üretmez, atılır.
func TestIngestStoresDiskInodePercentage(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "inode-1")

	m := metrics(10, 20)
	m["disk"] = []map[string]any{
		{"mount": "/", "used_pct": 10, "total": 100, "free": 90, "inodes_used_pct": 42.5},
		{"mount": "/data", "used_pct": 10, "total": 100, "free": 90, "inodes_used_pct": 250},
		{"mount": "/tmp", "used_pct": 10, "total": 100, "free": 90},
	}
	if code := a.push(c, c.APIToken, m); code != 204 {
		t.Fatalf("push = %d", code)
	}
	var points []struct {
		Disk []struct {
			Mount         string   `json:"mount"`
			InodesUsedPct *float64 `json:"inodes_used_pct"`
		} `json:"disk"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String()+"/metrics", root, nil, &points)
	if len(points) != 1 || len(points[0].Disk) != 3 {
		t.Fatalf("points = %+v", points)
	}
	by := map[string]*float64{}
	for _, d := range points[0].Disk {
		by[d.Mount] = d.InodesUsedPct
	}
	if by["/"] == nil || *by["/"] != 42.5 || by["/data"] != nil || by["/tmp"] != nil {
		t.Errorf("inode pcts = %v %v %v, want 42.5 / dropped / absent", by["/"], by["/data"], by["/tmp"])
	}
}
