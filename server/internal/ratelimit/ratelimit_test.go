package ratelimit

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(perMinute float64, burst int) (*Limiter, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return newWithClock(perMinute, burst, c.now), c
}

func TestAllowBurstThenBlock(t *testing.T) {
	l, _ := newTestLimiter(60, 3) // 1 jeton/sn, burst 3

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d within burst was denied", i+1)
		}
	}
	ok, retry := l.Allow("a")
	if ok {
		t.Fatal("request beyond burst was allowed")
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("retryAfter = %v, want (0, 1s]", retry)
	}
}

func TestRefillOverTime(t *testing.T) {
	l, clk := newTestLimiter(60, 2)

	l.Allow("a")
	l.Allow("a")
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("expected bucket to be empty")
	}

	clk.advance(1 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("expected one token after 1s")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("expected only one token after 1s")
	}

	clk.advance(time.Hour) // burst'te sınırlanmalı, birikmemeli
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d after long idle was denied", i+1)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("tokens accumulated beyond burst")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(60, 1)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first key denied")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("first key should be exhausted")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("second key affected by first key's usage")
	}
}

func TestCheckDoesNotConsume(t *testing.T) {
	l, _ := newTestLimiter(60, 1)

	for i := 0; i < 5; i++ {
		if ok, _ := l.Check("a"); !ok {
			t.Fatalf("Check #%d denied although nothing was consumed", i+1)
		}
	}
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("Allow denied after Check calls")
	}
	if ok, _ := l.Check("a"); ok {
		t.Fatal("Check allowed on an exhausted bucket")
	}
}

func TestCheckDoesNotCreateBuckets(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	l.Check("never-charged")
	if len(l.buckets) != 0 {
		t.Fatalf("Check allocated %d bucket(s); scanners could grow memory unbounded", len(l.buckets))
	}
}

func TestDisabled(t *testing.T) {
	for _, l := range []*Limiter{New(0, 10), New(10, 0)} {
		for i := 0; i < 100; i++ {
			if ok, _ := l.Allow("a"); !ok {
				t.Fatal("disabled limiter denied a request")
			}
		}
	}
}

func TestSweepDropsIdleBuckets(t *testing.T) {
	l, clk := newTestLimiter(60, 2) // tam dolum 2 sn sürer

	l.Allow("idle")
	l.Allow("busy")
	if len(l.buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(l.buckets))
	}

	clk.advance(2 * time.Minute)
	l.Allow("busy") // sweep'i tetikler; "idle" çoktan tamamen dolmuştur

	if _, ok := l.buckets["idle"]; ok {
		t.Fatal("idle bucket was not swept")
	}
	if _, ok := l.buckets["busy"]; !ok {
		t.Fatal("active bucket was swept")
	}
}

func TestSetRate(t *testing.T) {
	l, clk := newTestLimiter(60, 1) // 1 jeton/sn

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first request denied")
	}
	// Daha yavaş hız: harcanan hak korunur, yeni hızla dolar (dakikada 6 = 10 sn'de bir).
	l.SetRate(6)
	clk.advance(5 * time.Second)
	if ok, retry := l.Allow("a"); ok || retry != 5*time.Second {
		t.Fatalf("after slowing down: ok=%v retry=%v, want denied with 5s left", ok, retry)
	}

	// 0 kapatır (kovalar bırakılır); yeniden açınca anahtar dolu bir kovayla başlar.
	l.SetRate(0)
	for range 5 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("disabled limiter denied a request")
		}
	}
	if n := len(l.buckets); n != 0 {
		t.Fatalf("%d buckets kept while disabled", n)
	}
	l.SetRate(60)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("re-enabled limiter denied the first request")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("re-enabled limiter allowed a request beyond burst")
	}
}

func TestSetRateConcurrentWithAllow(t *testing.T) {
	l := New(60, 5)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			l.SetRate(float64(i % 3 * 30))
		}
	}()
	for range 200 {
		l.Allow("a")
		l.Check("b")
	}
	<-done
}

// Snapshot hakkı eksik anahtarları en az kalandan başlayarak listeler, dolumu hesaba katar ve hiçbir kovayı değiştirmez.
func TestSnapshotListsKeysWithMissingTokens(t *testing.T) {
	l, clock := newTestLimiter(60, 5) // 1 jeton/sn, burst 5
	for range 4 {
		l.Allow("b")
	}
	for range 2 {
		l.Allow("a")
	}
	l.Allow("c")

	snap := l.Snapshot(10)
	if !snap.Enabled || snap.PerMinute != 60 || snap.Burst != 5 || snap.Keys != 3 || len(snap.Entries) != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Entries[0].Key != "b" || snap.Entries[0].Remaining != 1 || snap.Entries[1].Key != "a" || snap.Entries[1].Remaining != 3 ||
		snap.Entries[2].Key != "c" || !snap.Entries[0].LastUsedAt.Equal(clock.now()) {
		t.Fatalf("entries = %+v", snap.Entries)
	}

	// Sınır: en dolu olmayanlar öne; toplam yine bildirilir.
	if top := l.Snapshot(1); top.Keys != 3 || len(top.Entries) != 1 || top.Entries[0].Key != "b" {
		t.Fatalf("limited snapshot = %+v", top)
	}

	// Dolum hesaba katılır; tamamen dolan anahtar listeden düşer. Snapshot kovaları değiştirmez.
	clock.advance(2 * time.Second)
	snap = l.Snapshot(10)
	if snap.Keys != 1 || snap.Entries[0].Key != "b" || snap.Entries[0].Remaining != 3 {
		t.Fatalf("after 2s = %+v", snap)
	}
	if again := l.Snapshot(10); again.Entries[0].Remaining != 3 {
		t.Fatalf("a snapshot changed the bucket: %+v", again)
	}
	for range 3 {
		if ok, _ := l.Allow("b"); !ok {
			t.Fatal("b lost tokens to a snapshot")
		}
	}
	if ok, _ := l.Allow("b"); ok {
		t.Fatal("b gained tokens from a snapshot")
	}
}

func TestSnapshotOfADisabledLimiter(t *testing.T) {
	l, _ := newTestLimiter(0, 5)
	l.Allow("a")
	if snap := l.Snapshot(10); snap.Enabled || snap.Keys != 0 || snap.Entries == nil || len(snap.Entries) != 0 {
		t.Fatalf("disabled snapshot = %+v", snap)
	}
}
