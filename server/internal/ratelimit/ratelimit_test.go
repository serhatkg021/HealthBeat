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
