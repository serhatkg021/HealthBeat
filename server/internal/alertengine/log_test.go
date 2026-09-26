package alertengine_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/notify"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// captureLog, testin süresince varsayılan logger'ı JSON'a çevirir; dönen fonksiyon msg'si verilen satırları verir.
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

// requestCtx, bir ingest isteğinin bağlamı gibidir: istek kimliği ve (kimlik doğrulamadan) host.
func requestCtx(parent context.Context, id, host string) context.Context {
	info := &logging.RequestInfo{ID: id}
	info.SetHost(host)
	return logging.WithRequestInfo(parent, info)
}

func TestEngineErrorsAreLoggedAtErrorWithTheRequestContext(t *testing.T) {
	e := newEnv(t)
	lines := captureLog(t)

	// İptal edilmiş bağlam veritabanı hatası üretir: motor onu loglayıp döner.
	ctx, cancel := context.WithCancel(requestCtx(e.ctx, "req-offline", e.host.String()))
	cancel()
	e.engine.ResolveOffline(ctx, e.host, e.org)

	got := lines("alert engine: resolve offline alert")
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	rec := decode(t, got[0])
	if rec["level"] != "ERROR" || rec["request_id"] != "req-offline" || rec["host_id"] != e.host.String() || rec["err"] == nil {
		t.Fatalf("record = %v", rec)
	}
	// Motor host_id'yi kendisi yazar; bağlamdaki aynı anahtar ikinci kez eklenmemeli.
	if n := strings.Count(got[0], `"host_id"`); n != 1 {
		t.Fatalf("host_id written %d times: %s", n, got[0])
	}
}

func TestFailedAlertEmailIsLoggedWithTheRequestThatRaisedTheAlert(t *testing.T) {
	e := newEnv(t)
	lines := captureLog(t)
	// Bağlantıyı hemen reddeden bir SMTP adresi: teslim başarısız olur.
	engine := alertengine.New(e.pool, notify.New(notify.Config{Host: "127.0.0.1", Port: "1", From: "hb@x.test"}), "")

	engine.EvaluateMetrics(requestCtx(e.ctx, "req-ingest", e.host.String()), e.host, e.org, 97, 10, nil)
	engine.Flush()

	got := lines("alert engine: send email")
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	rec := decode(t, got[0])
	// Teslim, isteğin bitmesinden sonra ayrı bir işçide olur; yine de alert'i açan isteğin kimliğini taşır.
	if rec["level"] != "ERROR" || rec["request_id"] != "req-ingest" || rec["alert_id"] == nil {
		t.Fatalf("record = %v", rec)
	}
}
