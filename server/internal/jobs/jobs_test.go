package jobs

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Wait, iptalden sonra işler dönene kadar bekler.
func TestWaitReturnsOnceJobsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx)
	stopped := make(chan struct{})
	r.Go("worker", func(ctx context.Context) {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // kapanışta yarım kalan işi bitiriyor
		close(stopped)
	})
	if got := r.Running(); len(got) != 1 || got[0] != "worker" {
		t.Fatalf("Running = %v, want [worker]", got)
	}
	cancel()
	wait, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := r.Wait(wait); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Wait returned before the job finished")
	}
	if got := r.Running(); len(got) != 0 {
		t.Fatalf("Running after Wait = %v, want none", got)
	}
}

// Süre dolarsa Wait bitmeyen işlerin adını söyler.
func TestWaitNamesJobsThatDoNotStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx)
	release := make(chan struct{})
	defer close(release)
	r.Go("stuck", func(context.Context) { <-release })
	r.Go("polite", func(ctx context.Context) { <-ctx.Done() })
	cancel()
	wait, done := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer done()
	err := r.Wait(wait)
	if err == nil || !strings.Contains(err.Error(), "stuck") || strings.Contains(err.Error(), "polite") {
		t.Fatalf("Wait error = %v, want it to name only the stuck job", err)
	}
}

// Panic'leyen bir iş süreci düşürmez ve iptalden sonra Wait yine döner.
func TestPanickingJobDoesNotBlockShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx)
	panicked := make(chan struct{})
	r.Go("flaky", func(context.Context) {
		close(panicked)
		panic("boom")
	})
	<-panicked
	cancel()
	wait, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := r.Wait(wait); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}
