package httpapi_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"healthbeat-server/internal/httpapi"
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
