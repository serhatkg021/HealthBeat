package collector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// logRecorder, Background'ın log satırlarını toplar.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logRecorder) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func fixedInterval(d time.Duration) func() time.Duration { return func() time.Duration { return d } }

func TestBackgroundLatestBeforeFirstRunIsUnknown(t *testing.T) {
	b := NewBackground("x", fixedInterval(time.Minute), time.Second, func(context.Context) (int, error) { return 7, nil })
	if v, ok := b.Latest(); ok || v != 0 {
		t.Fatalf("Latest = %v, %v before any collection; want unknown", v, ok)
	}
}

func TestBackgroundCollectsAtOnceAndKeepsTheLastGoodValue(t *testing.T) {
	var mu sync.Mutex
	calls, fail := 0, false
	b := NewBackground("thing", fixedInterval(5*time.Millisecond), time.Second, func(context.Context) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if fail {
			return 0, errors.New("boom")
		}
		return calls, nil
	})
	logs := &logRecorder{}
	b.logf = logs.logf

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)
	b.WaitReady(ctx, time.Second)
	if v, ok := b.Latest(); !ok || v < 1 {
		t.Fatalf("Latest = %v, %v after the first collection", v, ok)
	}

	mu.Lock()
	fail = true
	last := calls
	mu.Unlock()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return calls >= last+3 })
	if v, ok := b.Latest(); !ok || v > last {
		t.Fatalf("Latest = %v, %v while failing; want the last good value (<= %d), still fresh", v, ok, last)
	}
	if n := logs.count(); n != 1 {
		t.Fatalf("logged %d lines over repeated failures; want one (only on the change)", n)
	}

	mu.Lock()
	fail = false
	mu.Unlock()
	waitFor(t, func() bool { return logs.count() == 2 })
}

func TestBackgroundResultGoesStale(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	b := NewBackground("x", fixedInterval(time.Minute), 10*time.Second, func(context.Context) (int, error) { return 1, nil })
	b.now = func() time.Time { return now }
	b.runOnce(context.Background())

	now = now.Add(3*time.Minute + 10*time.Second) // tam sınırda: hâlâ geçerli
	if _, ok := b.Latest(); !ok {
		t.Fatal("result reported stale at exactly 3 intervals + timeout")
	}
	now = now.Add(time.Second)
	if _, ok := b.Latest(); ok {
		t.Fatal("a result older than 3 intervals + timeout must be unknown, not shown as current")
	}
}

func TestBackgroundTimesOutAHangingSource(t *testing.T) {
	b := NewBackground("hang", fixedInterval(time.Hour), 20*time.Millisecond, func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	logs := &logRecorder{}
	b.logf = logs.logf
	done := make(chan struct{})
	go func() { b.runOnce(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a hanging source was not cut off by its timeout")
	}
	if _, ok := b.Latest(); ok || logs.count() != 1 {
		t.Fatalf("after a timeout: known=%v, logged %d lines; want unknown and one log line", ok, logs.count())
	}
}

func TestBackgroundStopsOnShutdownWithoutLoggingAnError(t *testing.T) {
	started := make(chan struct{})
	b := NewBackground("slow", fixedInterval(time.Hour), time.Hour, func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	logs := &logRecorder{}
	b.logf = logs.logf
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { b.Run(ctx); close(stopped) }()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
	if logs.count() != 0 {
		t.Fatalf("shutdown logged %v; an interrupted collection is not a failure", logs.lines)
	}
}

func TestBackgroundWaitReadyGivesUp(t *testing.T) {
	b := NewBackground("hang", fixedInterval(time.Hour), time.Hour, func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	begin := time.Now()
	b.WaitReady(context.Background(), time.Hour) // hiç başlamadı: beklemez
	if time.Since(begin) > time.Second {
		t.Fatal("WaitReady waited for a collector that was never started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)
	begin = time.Now()
	b.WaitReady(ctx, 30*time.Millisecond)
	if d := time.Since(begin); d < 30*time.Millisecond || d > time.Second {
		t.Fatalf("WaitReady returned after %s; want it to wait for the first run, then give up at its deadline", d)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
