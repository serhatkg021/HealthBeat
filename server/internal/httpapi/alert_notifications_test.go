package httpapi_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

type alertNotification struct {
	Event          string   `json:"event"`
	Level          string   `json:"level"`
	Channel        string   `json:"channel"`
	Status         string   `json:"status"`
	Attempts       int      `json:"attempts"`
	RecipientCount int      `json:"recipient_count"`
	Recipients     []string `json:"recipients"`
	LastError      string   `json:"last_error"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
	SentAt         *string  `json:"sent_at"`
	FailedAt       *string  `json:"failed_at"`
	NextAttemptAt  *string  `json:"next_attempt_at"`
}

// GET /alerts/:id/notifications: alert'in bildirim geçmişi sırasıyla; kapsam alert'in sunucusudur; alıcılar ve son hata
// yalnızca notification.view ile görünür. Alert listesi bildirimlerin toplu durumunu taşır.
func TestAlertNotificationHistory(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	root, _ := a.login("root@x.test", "super_admin")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	host := a.createPushHost(root, orgA, "web-1")
	opToken, opID := a.login("op@x.test", "operator")
	testdb.AssignHost(t, a.pool, opID, host.ID)
	otherAdmin, otherAdminID := a.login("adminb@x.test", "org_admin")
	testdb.AssignOrg(t, a.pool, otherAdminID, orgB)

	alert, _, err := store.NewAlerts(a.pool).CreateIfNoneActive(ctx, host.ID, "cpu", "", "critical", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	queue := store.NewOutbox(a.pool, testdb.SecretBox(t))
	add := func(event, level string) uuid.UUID {
		id, err := queue.Enqueue(ctx, store.OutboxMessage{Kind: store.OutboxKindAlert, Channel: "email",
			Recipients: []string{"ops@acme.test", "cto@acme.test"}, Subject: "[HealthBeat] -- " + event, Body: "gövde " + event,
			AlertID: &alert.ID, AlertEvent: event, AlertLevel: level})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	opened, resolved := add(store.AlertEventOpened, "critical"), add(store.AlertEventResolved, "critical")
	if err := queue.MarkSent(ctx, opened); err != nil {
		t.Fatal(err)
	}
	if err := queue.MarkFailed(ctx, resolved, "550 mailbox unavailable"); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/alerts/" + alert.ID.String() + "/notifications"

	var got []alertNotification
	a.expect(200, "GET", path, root, nil, &got)
	if len(got) != 2 {
		t.Fatalf("history = %+v, want 2 entries", got)
	}
	if g := got[0]; g.Event != "opened" || g.Level != "critical" || g.Channel != "email" || g.Status != "sent" || g.SentAt == nil ||
		g.RecipientCount != 2 || len(g.Recipients) != 2 || g.Body != "gövde opened" || g.NextAttemptAt != nil {
		t.Fatalf("first entry = %+v", g)
	}
	if g := got[1]; g.Event != "resolved" || g.Status != "failed" || g.FailedAt == nil || g.LastError != "550 mailbox unavailable" {
		t.Fatalf("second entry = %+v", g)
	}

	// Atanmış operatör görür (tohum veride notification.view'i de var); başka organizasyonun yöneticisi göremez.
	a.expect(200, "GET", path, opToken, nil, &got)
	if len(got) != 2 || len(got[0].Recipients) != 2 {
		t.Fatalf("operator view = %+v", got)
	}
	a.expect(403, "GET", path, otherAdmin, nil, nil)

	// notification.view'i olmayan rol durumu ve alıcı sayısını görür, adresleri ve hatayı görmez.
	if _, err := a.pool.Exec(ctx, `DELETE FROM role_permissions WHERE role = 'operator' AND permission_key = 'notification.view'`); err != nil {
		t.Fatal(err)
	}
	a.deps.ResetPermissionCache()
	got = nil
	a.expect(200, "GET", path, opToken, nil, &got)
	if len(got) != 2 || got[0].Recipients != nil || got[0].RecipientCount != 2 || got[1].LastError != "" || got[1].Status != "failed" {
		t.Fatalf("view without notification.view = %+v", got)
	}

	a.expect(400, "GET", "/api/v1/alerts/abc/notifications", root, nil, nil)
	a.expect(404, "GET", "/api/v1/alerts/"+uuid.NewString()+"/notifications", root, nil, nil)

	// Alert listesi toplu durumu taşır: en az bir bildirimi gitmediyse "failed".
	var list []struct {
		ID                 uuid.UUID `json:"id"`
		NotificationStatus string    `json:"notification_status"`
	}
	a.expect(200, "GET", "/api/v1/alerts", root, nil, &list)
	if len(list) != 1 || list[0].ID != alert.ID || list[0].NotificationStatus != "failed" {
		t.Fatalf("alert list = %+v", list)
	}
}
