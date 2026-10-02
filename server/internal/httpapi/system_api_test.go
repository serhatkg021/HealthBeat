package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
