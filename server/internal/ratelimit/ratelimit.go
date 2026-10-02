// Package ratelimit küçük, bellek içi, anahtarlı bir token-bucket sınırlayıcı sağlar (yalnızca
// stdlib). Kovalar süreç başınadır — birden çok server kopyasında her biri kendi sınırını uygular.
package ratelimit

import (
	"cmp"
	"math"
	"slices"
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

// disabled, l.mu altında çağrılır.
func (l *Limiter) disabled() bool { return l.rate <= 0 || l.burst <= 0 }

// SetRate, dolma hızını çalışırken değiştirir (panelden değişen hız sınırı); perMinute <= 0 sınırlamayı kapatır.
// Mevcut kovalar korunur: bir anahtarın o ana kadar harcadığı hak sıfırlanmaz, yalnızca yeni hızla dolmaya devam eder.
func (l *Limiter) SetRate(perMinute float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rate = perMinute / 60
	if l.disabled() {
		clear(l.buckets)
	}
}

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
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.disabled() {
		return true, 0
	}

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

// Entry, bir anahtarın anlık durumudur (bkz. Snapshot).
type Entry struct {
	Key string `json:"key"`
	// Remaining, anahtarın şu an kullanabileceği hak sayısıdır (0 ile Burst arası; dolum hesaba katılmıştır).
	Remaining  float64   `json:"remaining"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Snapshot, sınırlayıcının salt okunur anlık görüntüsüdür (Sistem Araçları → Cache Durumu).
type Snapshot struct {
	PerMinute float64 `json:"per_minute"`
	Burst     int     `json:"burst"`
	// Enabled false ise sınırlama kapalıdır (hız ya da kapasite 0).
	Enabled bool `json:"enabled"`
	// Keys, hakkı eksik olan anahtar sayısıdır; Entries bunların en azı kalanlardan başlayarak en çok maxEntries tanesidir.
	Keys    int     `json:"keys"`
	Entries []Entry `json:"entries"`
}

// Snapshot, hakkı eksik anahtarları (en az hakkı kalan önce, sonra anahtar sırasıyla) döndürür; hiçbir kovayı
// değiştirmez. Dolumu tamamlanmış kovalar listelenmez: sınırlayıcı için hiç var olmamış bir anahtarla aynıdırlar.
func (l *Limiter) Snapshot(maxEntries int) Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap := Snapshot{PerMinute: l.rate * 60, Burst: int(l.burst), Enabled: !l.disabled(), Entries: []Entry{}}
	if !snap.Enabled {
		return snap
	}
	now := l.now()
	for k, b := range l.buckets {
		remaining := math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		if remaining >= l.burst {
			continue
		}
		snap.Entries = append(snap.Entries, Entry{Key: k, Remaining: remaining, LastUsedAt: b.last})
	}
	slices.SortFunc(snap.Entries, func(a, b Entry) int {
		if c := cmp.Compare(a.Remaining, b.Remaining); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	snap.Keys = len(snap.Entries)
	if maxEntries >= 0 && len(snap.Entries) > maxEntries {
		snap.Entries = snap.Entries[:maxEntries]
	}
	return snap
}
