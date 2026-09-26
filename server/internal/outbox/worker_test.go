package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/outbox"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestBackoff(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i, w := range want {
		if got := outbox.Backoff(i + 1); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

// fakeChannel, gönderilenleri kaydeder; fail doluysa her gönderim o hatayla başarısız olur.
type fakeChannel struct {
	mu   sync.Mutex
	name string
	fail error
	sent []notify.Message
	to   [][]string
}

func (f *fakeChannel) Channel() string { return f.name }

func (f *fakeChannel) Send(_ context.Context, to []string, msg notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, msg)
	f.to = append(f.to, to)
	return nil
}

func (f *fakeChannel) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *store.Outbox
	email *fakeChannel
	w     *outbox.Worker
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testdb.New(t)
	st := store.NewOutbox(pool, testdb.SecretBox(t))
	email := &fakeChannel{name: "email"}
	return &env{t: t, pool: pool, store: st, email: email,
		w: outbox.NewWorker(st, []string{store.OutboxKindAlert, store.OutboxKindPasswordReset}, email)}
}

func (e *env) enqueue(m store.OutboxMessage) uuid.UUID {
	e.t.Helper()
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
	id, err := e.store.Enqueue(context.Background(), m)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

type rowState struct {
	attempts  int
	sent      bool
	failed    bool
	lastError string
	nextIn    time.Duration
}

func (e *env) state(id uuid.UUID) rowState {
	e.t.Helper()
	var s rowState
	var sentAt, failedAt *time.Time
	var lastErr *string
	var next time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT attempts, sent_at, failed_at, last_error, next_attempt_at FROM notification_outbox WHERE id = $1`, id).
		Scan(&s.attempts, &sentAt, &failedAt, &lastErr, &next); err != nil {
		e.t.Fatal(err)
	}
	s.sent, s.failed, s.nextIn = sentAt != nil, failedAt != nil, time.Until(next)
	if lastErr != nil {
		s.lastError = *lastErr
	}
	return s
}

func (e *env) deliver() int {
	e.t.Helper()
	n, err := e.w.DeliverDue(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestWorkerDeliversAndMarksSent(t *testing.T) {
	e := newEnv(t)
	plain := e.enqueue(store.OutboxMessage{Subject: "alert", Body: "gövde", Recipients: []string{"a@x.test", "b@x.test"}})
	sealed := e.enqueue(store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Subject: "sıfırlama", Body: "#token=abc", Seal: true})
	other := e.enqueue(store.OutboxMessage{Kind: store.OutboxKindPasswordChanged, Body: "başka işçinin"})

	if n := e.deliver(); n != 2 {
		t.Fatalf("delivered %d, want 2", n)
	}
	got := map[string]string{} // gövde → alıcılar (teslim sırası aynı zamanlı satırlar arasında belirsizdir)
	for i, m := range e.email.sent {
		got[m.Body] = strings.Join(e.email.to[i], ",")
	}
	if len(got) != 2 || got["gövde"] != "a@x.test,b@x.test" || got["#token=abc"] != "ops@x.test" {
		t.Fatalf("sent = %+v to %v", e.email.sent, e.email.to)
	}
	for _, id := range []uuid.UUID{plain, sealed} {
		if s := e.state(id); !s.sent || s.attempts != 1 {
			t.Fatalf("row %s = %+v, want sent after one attempt", id, s)
		}
	}
	if s := e.state(other); s.sent || s.attempts != 0 {
		t.Fatalf("a kind this worker does not own was touched: %+v", s)
	}
	if n := e.deliver(); n != 0 {
		t.Fatalf("a sent row was delivered again (%d)", n)
	}
}

// Başarısız teslim geri çekilerek yeniden denenir; kanal düzelince gider. Log satırı bildirimi doğuran isteğin
// kimliğini taşır.
func TestWorkerRetriesWithBackoff(t *testing.T) {
	e := newEnv(t)
	lines := captureLog(t)
	id := e.enqueue(store.OutboxMessage{Body: "b", RequestID: "req-ingest"})

	e.email.setFail(errors.New("connection refused"))
	if n := e.deliver(); n != 0 {
		t.Fatalf("delivered %d while the channel fails", n)
	}
	s := e.state(id)
	if s.sent || s.failed || s.attempts != 1 || s.lastError != "connection refused" || s.nextIn < 25*time.Second || s.nextIn > 35*time.Second {
		t.Fatalf("after one failure = %+v, want a retry in ~30s", s)
	}
	got := lines("notification delivery failed; will retry")
	if len(got) != 1 {
		t.Fatalf("got %d retry lines, want 1", len(got))
	}
	if rec := decode(t, got[0]); rec["level"] != "WARN" || rec["request_id"] != "req-ingest" || rec["outbox_id"] != id.String() || rec["attempt"] != 1.0 {
		t.Fatalf("retry record = %v", rec)
	}

	// Henüz zamanı gelmedi: tur onu almaz.
	if n := e.deliver(); n != 0 || e.state(id).attempts != 1 {
		t.Fatal("a row was retried before its backoff elapsed")
	}

	e.email.setFail(nil)
	e.makeDue(id)
	if n := e.deliver(); n != 1 {
		t.Fatalf("delivered %d after the channel recovered, want 1", n)
	}
	if s := e.state(id); !s.sent || s.attempts != 2 || s.lastError != "" {
		t.Fatalf("after recovery = %+v", s)
	}
}

func (e *env) makeDue(id uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE notification_outbox SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		e.t.Fatal(err)
	}
}

// Deneme sınırında vazgeçilir (ERROR); şifreli gövde silinir.
func TestWorkerGivesUpAfterMaxAttempts(t *testing.T) {
	e := newEnv(t)
	lines := captureLog(t)
	id := e.enqueue(store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Body: "#token=abc", Seal: true})
	e.email.setFail(errors.New("550 mailbox unavailable"))

	for i := 0; i < outbox.MaxAttempts; i++ {
		e.makeDue(id)
		e.deliver()
	}
	s := e.state(id)
	if !s.failed || s.sent || s.attempts != outbox.MaxAttempts || s.lastError != "550 mailbox unavailable" {
		t.Fatalf("after %d attempts = %+v, want failed", outbox.MaxAttempts, s)
	}
	var sealed *string
	if err := e.pool.QueryRow(context.Background(), `SELECT body_sealed FROM notification_outbox WHERE id = $1`, id).Scan(&sealed); err != nil || sealed != nil {
		t.Fatalf("body_sealed after giving up = %v, %v", sealed, err)
	}
	if got := lines("notification delivery failed; giving up"); len(got) != 1 || decode(t, got[0])["level"] != "ERROR" {
		t.Fatalf("give-up lines = %v", got)
	}
	e.makeDue(id)
	if e.deliver(); e.state(id).attempts != outbox.MaxAttempts {
		t.Fatal("a failed row was attempted again")
	}
}

// Kanalı olmayan satır ve süresi dolan satır gönderilmeden kapanır.
func TestWorkerClosesUndeliverableRows(t *testing.T) {
	e := newEnv(t)
	sms := e.enqueue(store.OutboxMessage{Channel: "sms", Body: "b"})
	past := time.Now().Add(-time.Second)
	expired := e.enqueue(store.OutboxMessage{Kind: store.OutboxKindPasswordReset, Body: "#token=old", Seal: true, ExpiresAt: &past})

	if n := e.deliver(); n != 0 || len(e.email.sent) != 0 {
		t.Fatalf("delivered %d, sent %+v; want nothing", n, e.email.sent)
	}
	if s := e.state(sms); !s.failed || s.lastError != "no notifier for channel sms" {
		t.Fatalf("sms row = %+v", s)
	}
	if s := e.state(expired); !s.failed || s.attempts != 0 || s.lastError != "expired before delivery" {
		t.Fatalf("expired row = %+v", s)
	}
}

// Wake ile uyandırılan Run, satırı yoklama aralığını beklemeden teslim eder.
func TestWorkerRunDeliversOnWake(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(100 * time.Millisecond) // ilk (boş) tur geçsin
	id := e.enqueue(store.OutboxMessage{Body: "b"})
	e.w.Wake()
	deadline := time.Now().Add(outbox.PollInterval / 2)
	for time.Now().Before(deadline) {
		if e.state(id).sent {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("woken worker did not deliver before the poll interval")
}

// --- log yardımcıları

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func captureLog(t *testing.T) func(msg string) []string {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(logging.New(buf, slog.LevelDebug, logging.FormatJSON))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func(msg string) []string {
		buf.mu.Lock()
		defer buf.mu.Unlock()
		var out []string
		for _, line := range strings.Split(buf.buf.String(), "\n") {
			if strings.Contains(line, `"msg":"`+msg+`"`) {
				out = append(out, line)
			}
		}
		return out
	}
}

func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("not JSON: %q", line)
	}
	return rec
}
