// Package ratelimit küçük, bellek içi, anahtarlı bir token-bucket sınırlayıcı sağlar (yalnızca
// stdlib). Kovalar süreç başınadır — birden çok server kopyasında her biri kendi sınırını uygular.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter, anahtar başına `rate` jeton/sn hızında, `burst`'e kadar jeton dağıtır.
type Limiter struct {
	rate  float64
	burst float64

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time

	now func() time.Time
}

// New, perMinute jeton/dakika hızında dolan ve verilen burst kapasitesine sahip bir Limiter
// döndürür. perMinute <= 0 ya da burst <= 0 sınırlamayı kapatır.
func New(perMinute float64, burst int) *Limiter {
	return newWithClock(perMinute, burst, time.Now)
}

func newWithClock(perMinute float64, burst int, now func() time.Time) *Limiter {
	return &Limiter{
		rate:    perMinute / 60,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     now,
	}
}

func (l *Limiter) disabled() bool { return l.rate <= 0 || l.burst <= 0 }

// Allow, key için bir jeton tüketir. Hiç yoksa birinin ne zaman olacağını bildirir.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	return l.take(key, true)
}

// Check, Allow(key)'in başarılı olup olmayacağını jeton tüketmeden bildirir. Yalnızca
// başarısızlıkta ücretlendirilen bir sınırlayıcı üzerinde işi kapılamak için kullanın.
func (l *Limiter) Check(key string) (ok bool, retryAfter time.Duration) {
	return l.take(key, false)
}

func (l *Limiter) take(key string, consume bool) (bool, time.Duration) {
	if l.disabled() {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b, exists := l.buckets[key]
	if !exists {
		b = &bucket{tokens: l.burst, last: now}
		if consume {
			l.buckets[key] = b
		}
	}

	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now

	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		return false, wait
	}
	if consume {
		b.tokens--
	}
	return true, 0
}

// sweep, tamamen dolacak kadar uzun süre boşta kalmış kovaları bırakır (olmayan bir kova
// aynı davranır); böylece bellek yakın zamanda aktif anahtar sayısıyla sınırlı kalır.
// Dakikada en fazla bir kez çalışır.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now

	refill := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) >= refill {
			delete(l.buckets, k)
		}
	}
}
