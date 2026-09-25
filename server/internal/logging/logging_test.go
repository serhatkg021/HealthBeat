package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{"": slog.LevelInfo, "info": slog.LevelInfo, "DEBUG": slog.LevelDebug,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn, " error ": slog.LevelError}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Fatalf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("ParseLevel(verbose): expected an error")
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]string{"": FormatText, "text": FormatText, "JSON": FormatJSON} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Fatalf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Fatal("ParseFormat(xml): expected an error")
	}
}

// syncBuffer, goroutine'lerin yazdığı logu testin güvenle okumasını sağlar.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureDefault, testin süresince varsayılan logger'ı buf'a JSON yazan bir logger'la değiştirir.
func captureDefault(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(New(buf, slog.LevelDebug, FormatJSON))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestContextAttrsAreAdded(t *testing.T) {
	buf := captureDefault(t)

	info := &RequestInfo{ID: "req-1"}
	ctx := WithRequestInfo(context.Background(), info)
	// Kimlikler kayıt anında okunur: bağlam kurulduktan sonra doldurulan alanlar da görünür.
	info.SetUser("user-1")
	info.SetIP("10.0.0.5")
	slog.InfoContext(ctx, "hello")
	slog.Info("no context")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %s", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"request_id": "req-1", "user_id": "user-1", "ip": "10.0.0.5"} {
		if rec[k] != want {
			t.Fatalf("%s = %v, want %q (line: %s)", k, rec[k], want, lines[0])
		}
	}
	if _, ok := rec["host_id"]; ok {
		t.Fatalf("host_id must be omitted when unset: %s", lines[0])
	}
	if strings.Contains(lines[1], "request_id") {
		t.Fatalf("a record without request context must not carry request_id: %s", lines[1])
	}
}

func TestNilRequestInfoIsSafe(t *testing.T) {
	RequestInfoFrom(context.Background()).SetUser("x") // panic etmemeli
	if RequestID(context.Background()) != "" {
		t.Fatal("RequestID without info should be empty")
	}
}

func TestGoRecoversPanic(t *testing.T) {
	buf := captureDefault(t)
	done := make(chan struct{})
	Go(context.Background(), "test worker", func() {
		defer close(done)
		panic("boom")
	})
	<-done
	// Recover, fn'in defer'lerinden sonra çalışır; logun yazılmasını bekle.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "panic recovered") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	out := buf.String()
	if !strings.Contains(out, `"where":"test worker"`) || !strings.Contains(out, `"panic":"boom"`) || !strings.Contains(out, "stack") {
		t.Fatalf("panic not logged with where/stack: %s", out)
	}
}

func TestRunLoopRestartsAfterPanic(t *testing.T) {
	buf := captureDefault(t)
	prev := loopRestartDelay
	loopRestartDelay = time.Millisecond
	t.Cleanup(func() { loopRestartDelay = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	returned := make(chan struct{})
	go func() {
		RunLoop(ctx, "test loop", func(ctx context.Context) {
			if runs.Add(1) < 3 {
				panic("boom")
			}
			<-ctx.Done() // üçüncü çalıştırma normal bir döngü gibi iptale kadar sürer
		})
		close(returned)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runs.Load() != 3 {
		t.Fatalf("loop ran %d times, want 3 (restarted twice)", runs.Load())
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("RunLoop did not return after cancel")
	}
	if n := strings.Count(buf.String(), "panic recovered"); n != 2 {
		t.Fatalf("logged %d panics, want 2: %s", n, buf.String())
	}
}

func TestRunLoopReturnsWhenFnReturns(t *testing.T) {
	calls := 0
	RunLoop(context.Background(), "once", func(context.Context) { calls++ })
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}
