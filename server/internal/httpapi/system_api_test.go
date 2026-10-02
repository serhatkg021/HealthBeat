package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"healthbeat-server/internal/httpapi"
	"healthbeat-server/internal/logging"
)

type queueStatusView struct {
	Pending  int `json:"pending"`
	Retrying int `json:"retrying"`
	Sent     int `json:"sent"`
	Failed   int `json:"failed"`
	ByKind   []struct {
		Kind    string `json:"kind"`
		Pending int    `json:"pending"`
	} `json:"by_kind"`
	OldestActiveAt     *string `json:"oldest_active_at"`
	MaxAttempts        int     `json:"max_attempts"`
	RetainFinishedDays int     `json:"retain_finished_days"`
	DBPool             struct {
		Acquired int `json:"acquired"`
		Total    int `json:"total"`
		Max      int `json:"max"`
	} `json:"db_pool"`
}

type queueItemsView struct {
	Items []struct {
		Kind          string   `json:"kind"`
		Recipients    []string `json:"recipients"`
		Subject       string   `json:"subject"`
		Status        string   `json:"status"`
		Attempts      int      `json:"attempts"`
		LastError     string   `json:"last_error"`
		NextAttemptAt *string  `json:"next_attempt_at"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// Sistem Araçları uç noktaları yalnızca system.* izni olanlara (varsayılan olarak super_admin) açıktır.
func TestSystemEndpointsNeedSystemPermissions(t *testing.T) {
	a := newAPI(t)
	orgAdmin, _ := a.login("oa@x.test", "org_admin")
	operator, _ := a.login("op@x.test", "operator")
	for _, tok := range []string{orgAdmin, operator} {
		for _, path := range []string{"/api/v1/system/queue", "/api/v1/system/queue/items"} {
			if code := a.call("GET", path, tok, nil, nil); code != 403 {
				t.Errorf("GET %s = %d, want 403", path, code)
			}
		}
	}
	a.expect(401, "GET", "/api/v1/system/queue", "", nil, nil)
}

func TestQueueStatusAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	ctx := context.Background()

	var st queueStatusView
	a.expect(200, "GET", "/api/v1/system/queue", root, nil, &st)
	if st.Pending+st.Retrying+st.Sent+st.Failed != 0 || st.OldestActiveAt != nil || st.MaxAttempts != 10 || st.RetainFinishedDays != 30 ||
		st.DBPool.Max < 1 || st.DBPool.Total < 1 || st.DBPool.Acquired > st.DBPool.Max {
		t.Fatalf("empty queue status = %+v", st)
	}

	const secret = "https://panel.x.test/reset-password#token=COK-GIZLI"
	if _, err := a.pool.Exec(ctx, `
		INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, alert_event, alert_level, attempts, last_error, sent_at, failed_at, created_at) VALUES
		  (gen_random_uuid(), 'alert', 'email', '{noc@x.test}', 'CPU yüksek', $1, 'opened', 'critical', 0, NULL, NULL, NULL, now() - interval '5 minutes'),
		  (gen_random_uuid(), 'alert', 'email', '{noc@x.test}', 'Disk dolu', $1, 'opened', 'warning', 4, 'smtp auth: 535', NULL, NULL, now() - interval '4 minutes'),
		  (gen_random_uuid(), 'alert', 'email', '{noc@x.test}', 'Çözüldü', $1, 'resolved', 'warning', 1, NULL, now(), NULL, now() - interval '3 minutes'),
		  (gen_random_uuid(), 'password_reset', 'email', '{ali@x.test}', 'Şifre sıfırlama', NULL, NULL, NULL, 10, 'giving up', NULL, now(), now() - interval '2 minutes')`,
		secret); err != nil {
		t.Fatal(err)
	}

	a.expect(200, "GET", "/api/v1/system/queue", root, nil, &st)
	if st.Pending != 1 || st.Retrying != 1 || st.Sent != 1 || st.Failed != 1 || st.OldestActiveAt == nil || len(st.ByKind) != 2 ||
		st.ByKind[0].Kind != "alert" || st.ByKind[0].Pending != 1 {
		t.Fatalf("queue status = %+v", st)
	}

	var items queueItemsView
	a.expect(200, "GET", "/api/v1/system/queue/items", root, nil, &items)
	if len(items.Items) != 4 || items.NextCursor != nil || items.Items[0].Subject != "Şifre sıfırlama" || items.Items[0].Status != "failed" ||
		items.Items[3].Subject != "CPU yüksek" || items.Items[3].Status != "pending" || items.Items[3].NextAttemptAt == nil {
		t.Fatalf("queue items = %+v", items)
	}
	a.expect(200, "GET", "/api/v1/system/queue/items?status=active", root, nil, &items)
	if len(items.Items) != 2 || items.Items[0].Status != "retrying" || items.Items[0].Attempts != 4 || items.Items[0].LastError != "smtp auth: 535" ||
		items.Items[0].Recipients[0] != "noc@x.test" {
		t.Fatalf("active items = %+v", items)
	}
	a.expect(200, "GET", "/api/v1/system/queue/items?kind=password_reset&status=failed", root, nil, &items)
	if len(items.Items) != 1 || items.Items[0].Kind != "password_reset" {
		t.Fatalf("failed password reset items = %+v", items)
	}

	// Sayfalama: imleç bir sonraki (daha eski) sayfayı getirir.
	a.expect(200, "GET", "/api/v1/system/queue/items?limit=3", root, nil, &items)
	if len(items.Items) != 3 || items.NextCursor == nil {
		t.Fatalf("first page = %+v", items)
	}
	a.expect(200, "GET", "/api/v1/system/queue/items?limit=3&cursor="+*items.NextCursor, root, nil, &items)
	if len(items.Items) != 1 || items.Items[0].Subject != "CPU yüksek" || items.NextCursor != nil {
		t.Fatalf("second page = %+v", items)
	}

	// İleti gövdesi hiçbir yanıtta yer almaz.
	var raw json.RawMessage
	a.expect(200, "GET", "/api/v1/system/queue/items", root, nil, &raw)
	if strings.Contains(string(raw), "COK-GIZLI") || strings.Contains(string(raw), `"body"`) {
		t.Fatalf("queue items leak the message body: %s", raw)
	}

	for _, bad := range []string{"?status=bekleyen", "?kind=sms", "?limit=0", "?limit=201", "?cursor=!!"} {
		a.expect(400, "GET", "/api/v1/system/queue/items"+bad, root, nil, nil)
	}
}

type cacheStatusView struct {
	Permissions struct {
		TTLSeconds int `json:"ttl_seconds"`
		Roles      []struct {
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
			ExpiresAt   string   `json:"expires_at"`
		} `json:"roles"`
	} `json:"permissions"`
	RateLimiters []struct {
		ID      string  `json:"id"`
		KeyKind string  `json:"key_kind"`
		PerMin  float64 `json:"per_minute"`
		Burst   int     `json:"burst"`
		Enabled bool    `json:"enabled"`
		Keys    int     `json:"keys"`
		Entries []struct {
			Key       string  `json:"key"`
			Label     string  `json:"label"`
			Remaining float64 `json:"remaining"`
		} `json:"entries"`
	} `json:"rate_limiters"`
	PullScheduler  *json.RawMessage `json:"pull_scheduler"`
	TrustedProxies *struct {
		Prefixes []string `json:"prefixes"`
	} `json:"trusted_proxies"`
	TLSCertificate *json.RawMessage `json:"tls_certificate"`
}

func TestCacheStatusAPI(t *testing.T) {
	a := newAPIWithLimits(t, httpapi.RateLimits{AuthFailuresPerMinute: 6, IngestPerMinute: 60})
	root, _ := a.login("root@x.test", "super_admin")
	orgAdmin, _ := a.login("oa@x.test", "org_admin")
	a.expect(403, "GET", "/api/v1/system/cache", orgAdmin, nil, nil)
	a.expect(401, "GET", "/api/v1/system/cache", "", nil, nil)

	// Sınırlayıcılara iz bırak: başarısız giriş (IP), şifre sıfırlama (e-posta) ve bir push (host).
	a.trustProxies("")
	a.expect(401, "POST", "/api/v1/auth/login", "", map[string]string{"email": "root@x.test", "password": "yanlış-şifre-1"}, nil)
	a.deps.SetPasswordReset(&fakeMailer{enabled: true}, "https://panel.x.test")
	a.forgot("oa@x.test", 204)
	host := a.createPushHost(root, a.createOrg(root, "acme"), "web-1")
	if code := a.push(host, host.APIToken, metrics(10, 10)); code != 204 {
		t.Fatalf("push = %d", code)
	}

	var st cacheStatusView
	a.expect(200, "GET", "/api/v1/system/cache", root, nil, &st)

	// İzin önbelleği: bu istekleri yapan roller önbellekte, izinleriyle.
	roles := map[string][]string{}
	for _, r := range st.Permissions.Roles {
		roles[r.Role] = r.Permissions
	}
	if st.Permissions.TTLSeconds != 60 || !slices.Contains(roles["super_admin"], "system.cache.view") || slices.Contains(roles["org_admin"], "system.cache.view") {
		t.Fatalf("permission cache = %+v", st.Permissions)
	}

	byID := map[string]int{}
	for i, l := range st.RateLimiters {
		byID[l.ID] = i
	}
	if len(st.RateLimiters) != 5 {
		t.Fatalf("rate limiters = %+v", st.RateLimiters)
	}
	login := st.RateLimiters[byID["login_failures"]]
	if login.KeyKind != "ip" || !login.Enabled || login.PerMin != 6 || login.Keys != 1 || login.Entries[0].Remaining >= float64(login.Burst) {
		t.Fatalf("login failures = %+v", login)
	}
	// E-posta anahtarı olduğu gibi gösterilir (maskesiz).
	emails := st.RateLimiters[byID["reset_emails"]]
	if emails.KeyKind != "email" || emails.Keys != 1 || emails.Entries[0].Key != "oa@x.test" {
		t.Fatalf("reset e-mails = %+v", emails)
	}
	// Host anahtarı sunucu başlığıyla etiketlenir.
	ingest := st.RateLimiters[byID["ingest_rate"]]
	if ingest.KeyKind != "host" || ingest.Keys != 1 || ingest.Entries[0].Key != host.ID.String() || ingest.Entries[0].Label != "web-1" {
		t.Fatalf("ingest rate = %+v", ingest)
	}

	// Bağlı olmayan kaynaklar null döner; güvenilir proxy çözücüsü (test ortamında boş) bağlıysa listesi boştur.
	if st.PullScheduler != nil || st.TLSCertificate != nil {
		t.Fatalf("unwired sources are present: pull=%v tls=%v", st.PullScheduler, st.TLSCertificate)
	}
	if st.TrustedProxies == nil || len(st.TrustedProxies.Prefixes) != 0 {
		t.Fatalf("trusted proxies = %+v", st.TrustedProxies)
	}

	// Yanıtta sır yok: token, şifre özeti ya da anahtar alanı bulunmaz.
	var raw json.RawMessage
	a.expect(200, "GET", "/api/v1/system/cache", root, nil, &raw)
	for _, leak := range []string{host.APIToken, "password", "secret", "token"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("cache status contains %q: %s", leak, raw)
		}
	}
}

type logFilesView struct {
	Enabled bool `json:"enabled"`
	Days    []struct {
		Day   string `json:"day"`
		Parts int    `json:"parts"`
		Bytes int64  `json:"bytes"`
	} `json:"days"`
	TotalBytes    int64 `json:"total_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	MaxAgeDays    int   `json:"max_age_days"`
}

type logEntriesView struct {
	Entries []struct {
		Line    int     `json:"line"`
		Time    *string `json:"time"`
		Level   string  `json:"level"`
		Message string  `json:"message"`
		Attrs   []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"attrs"`
	} `json:"entries"`
	NextBefore int `json:"next_before"`
	Scanned    int `json:"scanned"`
}

// LOG_FILE boşken Log Analiz "kapalı" görünür; satır ve indirme istekleri 404 olur.
func TestLogAnalysisIsOffWithoutALogFile(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	var files logFilesView
	a.expect(200, "GET", "/api/v1/system/logs", root, nil, &files)
	if files.Enabled || files.Days == nil || len(files.Days) != 0 {
		t.Fatalf("log files without a log file = %+v", files)
	}
	a.expect(404, "GET", "/api/v1/system/logs/entries?day=2026-10-02", root, nil, nil)
	a.expect(404, "GET", "/api/v1/system/logs/download?day=2026-10-02", root, nil, nil)
}

func TestLogAnalysisAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	orgAdmin, _ := a.login("oa@x.test", "org_admin")

	dir := t.TempDir()
	fw, err := logging.OpenFile(logging.FileOptions{Path: filepath.Join(dir, "server.log"), MaxAgeDays: 14, MaxTotalBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fw.Close() })
	a.deps.SetLogFiles(fw)
	day := time.Now().Format(time.DateOnly)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, l := range []string{
		`time=` + now + ` level=INFO msg=request method=GET path=/api/v1/meta status=200 request_id=r1 ip=10.0.0.1`,
		`time=` + now + ` level=WARN msg="request failed" method=POST path=/api/v1/auth/login status=401 request_id=r2 ip=10.0.0.2`,
		`time=` + now + ` level=ERROR msg="notification delivery failed; giving up" request_id=r2 err="smtp auth: 535"`,
	} {
		if _, err := fw.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{"/api/v1/system/logs", "/api/v1/system/logs/entries?day=" + day, "/api/v1/system/logs/download?day=" + day} {
		a.expect(403, "GET", path, orgAdmin, nil, nil)
		a.expect(401, "GET", path, "", nil, nil)
	}

	var files logFilesView
	a.expect(200, "GET", "/api/v1/system/logs", root, nil, &files)
	if !files.Enabled || len(files.Days) != 1 || files.Days[0].Day != day || files.Days[0].Parts != 1 || files.TotalBytes != files.Days[0].Bytes ||
		files.TotalBytes == 0 || files.MaxTotalBytes != 64<<20 || files.MaxAgeDays != 14 {
		t.Fatalf("log files = %+v", files)
	}

	var res logEntriesView
	a.expect(200, "GET", "/api/v1/system/logs/entries?day="+day, root, nil, &res)
	if len(res.Entries) != 3 || res.Entries[0].Level != "ERROR" || res.Entries[2].Message != "request" || res.NextBefore != 0 || res.Scanned != 3 ||
		res.Entries[0].Time == nil || res.Entries[0].Attrs[1].Key != "err" || res.Entries[0].Attrs[1].Value != "smtp auth: 535" {
		t.Fatalf("entries = %+v", res)
	}
	a.expect(200, "GET", "/api/v1/system/logs/entries?day="+day+"&level=warn&request_id=r2&q=LOGIN", root, nil, &res)
	if len(res.Entries) != 1 || res.Entries[0].Message != "request failed" {
		t.Fatalf("filtered entries = %+v", res)
	}
	a.expect(200, "GET", "/api/v1/system/logs/entries?day="+day+"&limit=2", root, nil, &res)
	if len(res.Entries) != 2 || res.NextBefore != 2 {
		t.Fatalf("first page = %+v", res)
	}
	a.expect(200, "GET", fmt.Sprintf("/api/v1/system/logs/entries?day=%s&limit=2&before=%d", day, res.NextBefore), root, nil, &res)
	if len(res.Entries) != 1 || res.Entries[0].Line != 1 || res.NextBefore != 0 {
		t.Fatalf("second page = %+v", res)
	}
	a.expect(200, "GET", "/api/v1/system/logs/entries?day="+day+"&to=2000-01-01T00:00:00Z", root, nil, &res)
	if len(res.Entries) != 0 || res.Entries == nil {
		t.Fatalf("entries before 2000 = %+v", res)
	}

	// Bir günün açılması (ilk sayfa) süzgeçleriyle denetim kaydına yazılır; sayfa çevirmek yazılmaz.
	if n := a.auditCount("system.logs.view"); n != 4 {
		t.Fatalf("system.logs.view audit entries = %d, want 4 (first pages only)", n)
	}
	var details string
	if err := a.pool.QueryRow(context.Background(), `SELECT target_id || ' ' || details::text FROM audit_logs WHERE action = 'system.logs.view' AND details::text LIKE '%r2%'`).Scan(&details); err != nil ||
		!strings.HasPrefix(details, day+" ") || !strings.Contains(details, `"level": "warn"`) || !strings.Contains(details, `"q": "LOGIN"`) {
		t.Fatalf("audit details = %q err=%v", details, err)
	}

	// İndirme: günün düz metni, ek olarak; denetim kaydına yazılır.
	req := httptest.NewRequest("GET", "/api/v1/system/logs/download?day="+day, nil)
	req.Header.Set("Authorization", "Bearer "+root)
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "healthbeat-server-"+day+".log") ||
		strings.Count(rec.Body.String(), "\n") < 3 || !strings.Contains(rec.Body.String(), `msg="request failed"`) {
		t.Fatalf("download = %d %v\n%s", rec.Code, rec.Header(), rec.Body.String())
	}
	if n := a.auditCount("system.logs.download"); n != 1 {
		t.Fatalf("system.logs.download audit entries = %d, want 1", n)
	}

	// Geçersiz istekler: gün yerine yol verilemez; olmayan gün 404.
	for _, bad := range []string{"", "?day=../../etc/passwd", "?day=2026-10-2", "?day=" + day + "&level=trace", "?day=" + day + "&limit=501",
		"?day=" + day + "&before=0", "?day=" + day + "&from=dün", "?day=" + day + "&q=" + strings.Repeat("x", 201)} {
		a.expect(400, "GET", "/api/v1/system/logs/entries"+bad, root, nil, nil)
	}
	a.expect(404, "GET", "/api/v1/system/logs/entries?day=1999-01-01", root, nil, nil)
	a.expect(400, "GET", "/api/v1/system/logs/download?day=..%2F..%2Fetc%2Fpasswd", root, nil, nil)
	a.expect(404, "GET", "/api/v1/system/logs/download?day=1999-01-01", root, nil, nil)
}
