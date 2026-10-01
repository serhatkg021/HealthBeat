package store_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

var allKinds = []string{store.OutboxKindAlert, store.OutboxKindPasswordReset, store.OutboxKindPasswordChanged}

func newOutbox(t *testing.T) (*pgxpool.Pool, *store.Outbox) {
	t.Helper()
	pool := testdb.New(t)
	return pool, store.NewOutbox(pool, testdb.SecretBox(t))
}

func enqueue(t *testing.T, o *store.Outbox, m store.OutboxMessage) uuid.UUID {
	t.Helper()
	if m.Kind == "" {
		m.Kind = store.OutboxKindAlert
	}
	if m.Kind == store.OutboxKindAlert && m.AlertEvent == "" {
		m.AlertEvent, m.AlertLevel = store.AlertEventOpened, "warning"
	}
	if m.Channel == "" {
		m.Channel = "email"
	}
	if m.Recipients == nil {
		m.Recipients = []string{"ops@x.test"}
	}
	if m.Subject == "" {
		m.Subject = "konu"
	}
	id, err := o.Enqueue(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Şifreli gövde veritabanında düz hâliyle bulunmaz; alan işçi çözülmüş gövdeyi alır; gönderilince şifreli hâli de silinir.
func TestOutboxSealedBodyNeverStoredInPlaintext(t *testing.T) {
	pool, o := newOutbox(t)
	ctx := context.Background()
	link := "https://panel.test/reset-password#token=SECRET-TOKEN-123"
	id := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Body: "Bağlantı: " + link, Seal: true})

	var body, sealed *string
	if err := pool.QueryRow(ctx, `SELECT body, body_sealed FROM notification_outbox WHERE id = $1`, id).Scan(&body, &sealed); err != nil {
		t.Fatal(err)
	}
	if body != nil || sealed == nil || strings.Contains(*sealed, "SECRET-TOKEN-123") {
		t.Fatalf("stored body=%v sealed=%v; want only an encrypted body", body, sealed)
	}

	items, err := o.Claim(ctx, allKinds, 10, time.Minute)
	if err != nil || len(items) != 1 || items[0].OpenErr != nil || !strings.Contains(items[0].Body, link) {
		t.Fatalf("claimed = %+v, %v", items, err)
	}
	if err := o.MarkSent(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT body_sealed FROM notification_outbox WHERE id = $1`, id).Scan(&sealed); err != nil || sealed != nil {
		t.Fatalf("after sending body_sealed = %v, %v; want it wiped", sealed, err)
	}

	// Başka bir satırın şifreli gövdesi bu satırda çözülmez (aad = satır kimliği).
	other := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Body: "x", Seal: true})
	victim := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Body: "y", Seal: true})
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET body_sealed = (SELECT body_sealed FROM notification_outbox WHERE id = $1) WHERE id = $2`, other, victim); err != nil {
		t.Fatal(err)
	}
	items, _ = o.Claim(ctx, allKinds, 10, time.Minute)
	for _, it := range items {
		if it.ID == victim && it.OpenErr == nil {
			t.Fatal("a sealed body copied from another row was decrypted")
		}
	}
}

// Alınan satır kira süresince yeniden alınmaz; kira bitince (süreç düştüyse) yeniden alınır ve deneme sayısı artar.
func TestOutboxClaimLeasesRows(t *testing.T) {
	pool, o := newOutbox(t)
	ctx := context.Background()
	id := enqueue(t, o, store.OutboxMessage{Body: "b"})

	first, err := o.Claim(ctx, allKinds, 10, time.Hour)
	if err != nil || len(first) != 1 || first[0].Attempts != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if again, _ := o.Claim(ctx, allKinds, 10, time.Hour); len(again) != 0 {
		t.Fatalf("a leased row was claimed again: %+v", again)
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if again, _ := o.Claim(ctx, allKinds, 10, time.Hour); len(again) != 1 || again[0].Attempts != 2 {
		t.Fatalf("after the lease expired = %+v, want the row back with attempts 2", again)
	}

	// Yalnızca istenen türler alınır.
	enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordChanged, Body: "b"})
	if got, _ := o.Claim(ctx, []string{store.OutboxKindAlert}, 10, time.Hour); len(got) != 0 {
		t.Fatalf("claimed another kind: %+v", got)
	}
}

// Eşzamanlı işçiler aynı satırı iki kez almaz (FOR UPDATE SKIP LOCKED).
func TestOutboxConcurrentClaimsNeverOverlap(t *testing.T) {
	_, o := newOutbox(t)
	ctx := context.Background()
	const n = 40
	for i := 0; i < n; i++ {
		enqueue(t, o, store.OutboxMessage{Body: "b"})
	}
	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				items, err := o.Claim(ctx, allKinds, 3, time.Hour)
				if err != nil {
					t.Error(err)
					return
				}
				if len(items) == 0 {
					return
				}
				mu.Lock()
				for _, it := range items {
					seen[it.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("claimed %d distinct rows, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("row %s claimed %d times", id, c)
		}
	}
}

// Süresi dolan ve yerine yenisi gelen satırlar gönderilmeden kapanır; bitmiş eski satırlar temizlenir.
func TestOutboxExpireSupersedeAndPurge(t *testing.T) {
	pool, o := newOutbox(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	expired := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Body: "old link", Seal: true, ExpiresAt: &past})
	if got, _ := o.Claim(ctx, allKinds, 10, time.Minute); len(got) != 0 {
		t.Fatalf("an expired row was claimed: %+v", got)
	}
	if n, err := o.Expire(ctx, allKinds); err != nil || n != 1 {
		t.Fatalf("Expire = %d, %v", n, err)
	}

	future := time.Now().Add(time.Hour)
	superseded := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Recipients: []string{"u@x.test"}, Body: "l1", Seal: true, ExpiresAt: &future})
	other := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Recipients: []string{"v@x.test"}, Body: "l2", Seal: true, ExpiresAt: &future})
	if n, err := o.Supersede(ctx, store.OutboxKindPasswordReset, "u@x.test"); err != nil || n != 1 {
		t.Fatalf("Supersede = %d, %v", n, err)
	}

	state := func(id uuid.UUID) (failed bool, lastErr string, sealed bool) {
		t.Helper()
		var f *time.Time
		var le, s *string
		if err := pool.QueryRow(ctx, `SELECT failed_at, last_error, body_sealed FROM notification_outbox WHERE id = $1`, id).Scan(&f, &le, &s); err != nil {
			t.Fatal(err)
		}
		if le != nil {
			lastErr = *le
		}
		return f != nil, lastErr, s != nil
	}
	if f, le, s := state(expired); !f || le != "expired before delivery" || s {
		t.Fatalf("expired row: failed=%v err=%q sealed=%v", f, le, s)
	}
	if f, le, s := state(superseded); !f || le != "superseded" || s {
		t.Fatalf("superseded row: failed=%v err=%q sealed=%v", f, le, s)
	}
	if f, _, s := state(other); f || !s {
		t.Fatalf("another recipient's row was touched: failed=%v sealed=%v", f, s)
	}

	// Bitmiş satırlar süresi gelince silinir, bekleyenler asla.
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET created_at = now() - interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := o.PurgeFinished(ctx, allKinds, time.Now().Add(-30*24*time.Hour)); err != nil || n != 2 {
		t.Fatalf("PurgeFinished = %d, %v; want the expired and superseded rows", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("rows left = %d, %v; want the pending one", left, err)
	}
}

// WithTx: geri alınan transaction'daki bildirim kuyrukta kalmaz.
func TestOutboxEnqueueFollowsTheTransaction(t *testing.T) {
	pool, o := newOutbox(t)
	ctx := context.Background()
	errRollback := context.Canceled
	err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := o.WithTx(tx).Enqueue(ctx, store.OutboxMessage{Kind: store.OutboxKindAlert, Channel: "email", Recipients: []string{"a@x"},
			Subject: "s", Body: "b", AlertEvent: store.AlertEventOpened, AlertLevel: "warning"}); err != nil {
			return err
		}
		return errRollback
	})
	if err != errRollback {
		t.Fatalf("InTx = %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after rollback = %d, %v", n, err)
	}
}

// Alert bildirimleri alert'leri durdukça (gövdesiyle) saklanır; alert'i silinmiş sahipsiz satırlar ve hesap e-postaları
// süresi gelince silinir. Alert detayı bildirimleri sırasıyla ve durumlarıyla okur; alert listesi toplu durumu taşır.
func TestOutboxAlertHistoryIsKeptAndSummarized(t *testing.T) {
	pool, o := newOutbox(t)
	ctx := context.Background()
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "hash")
	alerts := store.NewAlerts(pool)
	newAlert := func(metric string) uuid.UUID {
		t.Helper()
		a, _, err := alerts.CreateIfNoneActive(ctx, host, metric, "", "warning", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	sentAlert, failedAlert, pendingAlert, quietAlert := newAlert("cpu"), newAlert("ram"), newAlert("disk"), newAlert("docker_restart")
	notify := func(alertID uuid.UUID, event, level string) uuid.UUID {
		return enqueue(t, o, store.OutboxMessage{AlertID: &alertID, AlertEvent: event, AlertLevel: level, Subject: event, Body: "gövde " + event})
	}
	opened := notify(sentAlert, store.AlertEventOpened, "warning")
	resolved := notify(sentAlert, store.AlertEventResolved, "warning")
	failedOpen := notify(failedAlert, store.AlertEventOpened, "critical")
	failedResolved := notify(failedAlert, store.AlertEventResolved, "critical")
	notify(pendingAlert, store.AlertEventOpened, "warning")
	for _, id := range []uuid.UUID{opened, resolved, failedResolved} {
		if err := o.MarkSent(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.MarkFailed(ctx, failedOpen, "550 mailbox unavailable"); err != nil {
		t.Fatal(err)
	}

	history, err := o.ListForAlert(ctx, sentAlert)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if h := history[0]; h.Event != store.AlertEventOpened || h.Status != "sent" || h.SentAt == nil || h.Body != "gövde opened" ||
		h.Recipients[0] != "ops@x.test" || h.NextAttemptAt != nil {
		t.Fatalf("first entry = %+v", h)
	}
	if failed, _ := o.ListForAlert(ctx, failedAlert); failed[0].Status != "failed" || failed[0].LastError != "550 mailbox unavailable" {
		t.Fatalf("failed entry = %+v", failed[0])
	}
	if pending, _ := o.ListForAlert(ctx, pendingAlert); pending[0].Status != "pending" || pending[0].NextAttemptAt == nil {
		t.Fatalf("pending entry = %+v", pending[0])
	}

	list, _, err := alerts.List(ctx, store.AlertFilter{}, store.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]string{sentAlert: "sent", failedAlert: "failed", pendingAlert: "pending", quietAlert: ""}
	for _, a := range list {
		if a.NotificationStatus != want[a.ID] {
			t.Errorf("alert %s notification_status = %q, want %q", a.AlertType, a.NotificationStatus, want[a.ID])
		}
	}

	// Temizlik: alert'i duran bildirimler kalır; sahipsiz alert bildirimi ve hesap e-postası silinir.
	orphan := enqueue(t, o, store.OutboxMessage{Body: "sahipsiz"})
	account := enqueue(t, o, store.OutboxMessage{Kind: store.OutboxKindPasswordChanged, Body: "hesap"})
	for _, id := range []uuid.UUID{orphan, account} {
		if err := o.MarkSent(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET created_at = now() - interval '400 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := o.PurgeFinished(ctx, allKinds, time.Now().Add(-30*24*time.Hour)); err != nil || n != 2 {
		t.Fatalf("PurgeFinished = %d, %v; want only the orphaned and the account rows", n, err)
	}
	history, _ = o.ListForAlert(ctx, sentAlert)
	bodies := map[string]bool{}
	for _, h := range history {
		bodies[h.Body] = true
	}
	if len(history) != 2 || !bodies["gövde opened"] || !bodies["gövde resolved"] {
		t.Fatalf("alert history after purge = %+v, want it kept with its bodies", history)
	}
}
