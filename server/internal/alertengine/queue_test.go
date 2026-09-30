package alertengine

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/testsmtp"
)

// hungSMTP bağlantıları kabul eder ve hiç konuşmaz: ölü/kara deliğe düşmüş bir relay.
// release() tutulan her bağlantıyı kapatır; böylece bloklanan göndericiler hemen başarısız olur.
type hungSMTP struct {
	host, port string
	mu         sync.Mutex
	conns      []net.Conn
	ln         net.Listener
}

func startHungSMTP(t *testing.T) *hungSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &hungSMTP{ln: ln}
	h.host, h.port, _ = net.SplitHostPort(ln.Addr().String())
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h.mu.Lock()
			h.conns = append(h.conns, c)
			h.mu.Unlock()
		}
	}()
	t.Cleanup(h.release)
	return h
}

func (h *hungSMTP) release() {
	h.ln.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		c.Close()
	}
}

func (h *hungSMTP) mailer() *notify.Mailer {
	return notify.New(notify.Config{Host: h.host, Port: h.port, From: "hb@x.test"})
}

type fixture struct {
	pool  *pgxpool.Pool
	org   uuid.UUID
	hosts []uuid.UUID
}

func newFixture(t *testing.T, nHosts int) *fixture {
	t.Helper()
	pool := testdb.New(t)
	org := testdb.Org(t, pool, "acme")
	f := &fixture{pool: pool, org: org}
	for i := 0; i < nHosts; i++ {
		f.hosts = append(f.hosts, testdb.PushHost(t, pool, org, "web-"+string(rune('a'+i)), "h"))
	}
	testdb.Threshold(t, pool, nil, nil, "cpu", 80, 95)
	testdb.Threshold(t, pool, nil, nil, "ram", 80, 95)
	testdb.EmailOwner(t, pool, "owner@x.test") // bildirilecek biri
	return f
}

func (f *fixture) alertCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM alerts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) outboxRows(t *testing.T) (pending, sent int) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE sent_at IS NULL AND failed_at IS NULL), count(*) FILTER (WHERE sent_at IS NOT NULL)
		 FROM notification_outbox`).Scan(&pending, &sent); err != nil {
		t.Fatal(err)
	}
	return pending, sent
}

// makeDue, bekleyen satırların geri çekilme süresini bitirir.
func (f *fixture) makeDue(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE notification_outbox SET next_attempt_at = now() - interval '1 second' WHERE sent_at IS NULL`); err != nil {
		t.Fatal(err)
	}
}

// Regresyon: e-posta eskiden ingest çağrısının içinde gönderiliyordu; bu yüzden takılan bir
// SMTP relay metrik alımını durduruyordu (ölçüldü: 8 sn sonra hâlâ bloklu). Artık ingest yalnızca kuyruğa yazar.
func TestHungSMTPDoesNotBlockIngest(t *testing.T) {
	f := newFixture(t, 1)
	hung := startHungSMTP(t)
	e := New(f.pool, hung.mailer(), "")

	start := time.Now()
	e.EvaluateMetrics(context.Background(), f.hosts[0], f.org, 97, 10, nil)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ingest took %v with a hung SMTP relay; it must not wait for e-mail", elapsed)
	}
	if f.alertCount(t) != 1 {
		t.Fatal("the alert was not stored")
	}
	if pending, _ := f.outboxRows(t); pending != 1 {
		t.Fatalf("pending notifications = %d, want 1", pending)
	}

	// Kapanış, sonsuza dek takılmak yerine takılı teslimattan vazgeçer; bildirim kuyrukta kalır.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); err == nil {
		t.Fatal("Close reported a clean drain while a delivery is stuck")
	}
	if pending, sent := f.outboxRows(t); pending != 1 || sent != 0 {
		t.Fatalf("after a stuck shutdown pending=%d sent=%d, want the notification kept", pending, sent)
	}
	hung.release()
}

// SMTP kapalıyken açılan alert'in bildirimi kaybolmaz: kuyrukta bekler; server yeniden başlayıp SMTP düzelince gider.
func TestNotificationSurvivesSMTPOutageAndRestart(t *testing.T) {
	f := newFixture(t, 1)
	down := notify.New(notify.Config{Host: "127.0.0.1", Port: "1", From: "hb@x.test"}) // bağlantıyı hemen reddeder
	e := New(f.pool, down, "")

	e.EvaluateMetrics(context.Background(), f.hosts[0], f.org, 97, 10, nil)
	e.Flush()
	var attempts int
	var lastErr *string
	if err := f.pool.QueryRow(context.Background(), `SELECT attempts, last_error FROM notification_outbox`).Scan(&attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastErr == nil {
		t.Fatalf("after a failed delivery attempts=%d last_error=%v, want a recorded retry", attempts, lastErr)
	}

	// "Yeniden başlatma": aynı veritabanı üzerinde yeni bir motor, çalışan bir SMTP ile.
	smtp := testsmtp.Start(t)
	restarted := New(f.pool, notify.New(notify.Config{Host: smtp.Host, Port: smtp.Port, From: "hb@x.test"}), "")
	f.makeDue(t)
	restarted.Flush()
	if msgs := smtp.Messages(); len(msgs) != 1 {
		t.Fatalf("delivered %d e-mails after the restart, want the 1 that waited", len(msgs))
	}
	if pending, sent := f.outboxRows(t); pending != 0 || sent != 1 {
		t.Fatalf("pending=%d sent=%d after delivery", pending, sent)
	}
}

// Close, teslim zamanı gelmiş bildirimleri gönderir; Close'dan sonra yazılan bildirim de kaybolmaz (kuyrukta bekler).
func TestCloseDeliversDueNotificationsAndLaterOnesWait(t *testing.T) {
	f := newFixture(t, 3)
	smtp := testsmtp.Start(t)
	e := New(f.pool, notify.New(notify.Config{Host: smtp.Host, Port: smtp.Port, From: "hb@x.test"}), "")

	for _, c := range f.hosts[:2] {
		e.EvaluateMetrics(context.Background(), c, f.org, 97, 10, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := len(smtp.Messages()); n != 2 {
		t.Fatalf("%d e-mails delivered by shutdown, want the 2 that were queued", n)
	}

	e.EvaluateMetrics(context.Background(), f.hosts[2], f.org, 97, 10, nil)
	if f.alertCount(t) != 3 {
		t.Fatal("alert not stored after Close")
	}
	if pending, _ := f.outboxRows(t); pending != 1 {
		t.Fatalf("pending = %d, want the notification written after Close to wait in the queue", pending)
	}
	e.Flush()
	if n := len(smtp.Messages()); n != 3 {
		t.Fatalf("%d e-mails, want the waiting one delivered too", n)
	}
}

// Gerçek PostgreSQL'de: bildirim satırı yazılamazsa (burada tablo geçici olarak yok) alert yine kaydedilir; kayıt
// noktası yalnızca bildirimi geri alır, transaction'ı bozmaz.
func TestAlertIsRecordedWhenNotificationCannotBeQueued(t *testing.T) {
	f := newFixture(t, 1)
	ctx := context.Background()
	e := New(f.pool, notify.New(notify.Config{}), "")
	if _, err := f.pool.Exec(ctx, `ALTER TABLE notification_outbox RENAME TO notification_outbox_away`); err != nil {
		t.Fatal(err)
	}
	e.EvaluateMetrics(ctx, f.hosts[0], f.org, 97, 10, nil)
	if _, err := f.pool.Exec(ctx, `ALTER TABLE notification_outbox_away RENAME TO notification_outbox`); err != nil {
		t.Fatal(err)
	}
	if n := f.alertCount(t); n != 1 {
		t.Fatalf("alerts = %d, want the alert recorded even though its notification could not be queued", n)
	}
	if pending, _ := f.outboxRows(t); pending != 0 {
		t.Fatalf("pending = %d, want no notification row", pending)
	}

	e.EvaluateMetrics(ctx, f.hosts[0], f.org, 20, 10, nil) // çözülme bildirimi normal yazılır
	if pending, _ := f.outboxRows(t); pending != 1 {
		t.Fatalf("pending = %d after the alert resolved, want the resolution notification", pending)
	}
}
