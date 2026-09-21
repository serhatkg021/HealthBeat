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
	testdb.User(t, pool, "root@x.test", "super_admin", "pw") // bildirilecek biri
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

// Regresyon: e-posta eskiden ingest çağrısının içinde gönderiliyordu; bu yüzden takılan bir
// SMTP relay metrik alımını durduruyordu (ölçüldü: 8 sn sonra hâlâ bloklu).
func TestHungSMTPDoesNotBlockIngest(t *testing.T) {
	f := newFixture(t, 1)
	hung := startHungSMTP(t)
	e := New(f.pool, hung.mailer())

	start := time.Now()
	e.EvaluateMetrics(context.Background(), f.hosts[0], f.org, 97, 10, nil)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ingest took %v with a hung SMTP relay; it must not wait for e-mail", elapsed)
	}
	if f.alertCount(t) != 1 {
		t.Fatal("the alert was not stored")
	}

	// Kapanış, sonsuza dek takılmak yerine takılı teslimattan vazgeçer.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); err == nil {
		t.Fatal("Close reported a clean drain while a delivery is stuck")
	}
	hung.release() // işçinin engelini kaldır ki test arkasında bir şey bırakmasın
	e.Flush()
}

func TestFullMailQueueDropsWithoutBlocking(t *testing.T) {
	f := newFixture(t, 4)
	hung := startHungSMTP(t)
	e := newEngine(f.pool, hung.mailer(), 2, 1) // 1 işçi (takılı) + 2 tane daha için yer

	start := time.Now()
	for _, c := range f.hosts { // 4 host x (cpu + ram) = 8 alert, kuyruğun alabileceğinden çok daha fazla
		e.EvaluateMetrics(context.Background(), c, f.org, 97, 97, nil)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("ingest took %v; a full mail queue must drop, not block", elapsed)
	}
	if n := f.alertCount(t); n != 8 {
		t.Fatalf("%d alerts stored, want 8 (dropping a notification must not lose the alert)", n)
	}

	// Atılan bildirimler Flush'ı sonsuza dek bekletmemeli.
	hung.release()
	done := make(chan struct{})
	go func() { e.Flush(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Flush never returned: pending accounting leaked on the drop path")
	}
}

func TestCloseDeliversQueuedMailAndLaterNotificationsAreIgnored(t *testing.T) {
	f := newFixture(t, 3)
	smtp := testsmtp.Start(t)
	e := New(f.pool, notify.New(notify.Config{Host: smtp.Host, Port: smtp.Port, From: "hb@x.test"}))

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

	// Close'dan sonra yeni bir alert yine kaydedilir, bildirimi sessizce atlanır ve hiçbir şey
	// panik yapmaz (kapalı kanala gönderme).
	e.EvaluateMetrics(context.Background(), f.hosts[2], f.org, 97, 10, nil)
	if f.alertCount(t) != 3 {
		t.Fatal("alert not stored after Close")
	}
	if n := len(smtp.Messages()); n != 2 {
		t.Fatalf("%d e-mails after Close, want still 2", n)
	}
	if err := e.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
