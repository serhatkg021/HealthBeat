package collector

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Background, yavaş bir kaynağı (alt süreç, Docker API'si gibi) arka planda kendi aralığıyla toplar ve son başarılı
// sonucu tutar. Rapor döngüsü yalnızca Latest ile okur, toplamayı hiç beklemez: yavaş ya da takılan bir kaynak raporu
// geciktiremez ve kaybettiremez (bkz. docs/AGENT.md, "Toplama düzeni").
//
// Kurallar:
//   - Aynı anda tek toplama: bir sonraki, öncekinin bitmesinden bir aralık sonra başlar; takılan bir kaynakta çağrılar
//     üst üste yığılmaz. Her toplama kendi zaman aşımıyla çalışır.
//   - Başarısız toplama son başarılı değeri silmez; ama son başarı "bayat" sınırından eskiyse Latest ok=false döner ve
//     alan gönderilmez (panelde "bilinmiyor"), bozulmuş bir kaynak eski veriyi güncel gibi göstermez.
//   - Hata her döngüde değil, yalnızca durum değiştiğinde loglanır.
type Background[T any] struct {
	name     string
	interval func() time.Duration // pull modunda server'ın sorgu aralığından öğrenildiği için sabit değil
	timeout  time.Duration
	collect  func(ctx context.Context) (T, error)
	now      func() time.Time
	logf     func(format string, args ...any)

	started   atomic.Bool   // Start çağrıldı mı; çağrılmadıysa WaitReady beklemez
	ready     chan struct{} // ilk deneme (başarılı ya da değil) bitince kapanır
	readyOnce sync.Once

	mu      sync.Mutex
	value   T
	at      time.Time // son başarılı toplama; sıfır = hiç olmadı
	failing bool
}

// NewBackground, bir arka plan toplayıcısı kurar; Start (ya da Run) çağrılana kadar hiçbir şey toplamaz.
func NewBackground[T any](name string, interval func() time.Duration, timeout time.Duration, collect func(ctx context.Context) (T, error)) *Background[T] {
	return &Background[T]{
		name: name, interval: interval, timeout: timeout, collect: collect,
		now: time.Now, logf: log.Printf, ready: make(chan struct{}),
	}
}

// Start, Run'ı ayrı bir goroutine'de başlatır. "Başladı" bilgisi hemen işaretlenir: hemen ardından gelen bir WaitReady
// ilk toplamayı bekler.
func (b *Background[T]) Start(ctx context.Context) {
	b.started.Store(true)
	go b.Run(ctx)
}

// Run, ctx iptal edilene kadar toplar: önce hemen bir kez, sonra her toplamanın bitişinden bir aralık sonra.
func (b *Background[T]) Run(ctx context.Context) {
	b.started.Store(true)
	for {
		b.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.interval()):
		}
	}
}

func (b *Background[T]) runOnce(ctx context.Context) {
	defer b.readyOnce.Do(func() { close(b.ready) })
	cctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	v, err := b.collect(cctx)
	if ctx.Err() != nil {
		return // kapanış: yarım kalan toplama hata sayılmaz
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		if !b.failing {
			b.logf("collect %s: %v", b.name, err)
		}
		b.failing = true
		return
	}
	if b.failing {
		b.logf("collect %s: recovered", b.name)
	}
	b.failing = false
	b.value, b.at = v, b.now()
}

// Latest, son başarılı sonucu döndürür; hiç başarılı olmadıysa ya da sonuç bayatsa ok=false.
func (b *Background[T]) Latest() (T, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.at.IsZero() || b.now().Sub(b.at) > b.staleAfter() {
		var zero T
		return zero, false
	}
	return b.value, true
}

// staleAfter: üç aralık boyunca yenilenemeyen sonuç bayattır. Zaman aşımı da eklenir: aralığı kısa, toplaması uzun
// süren bir kaynak (çok container'lı Docker) sağlıklıyken bayat sayılmasın.
func (b *Background[T]) staleAfter() time.Duration {
	return 3*b.interval() + b.timeout
}

// WaitReady, ilk toplama denemesi bitene kadar en çok d kadar bekler. Açılıştaki ilk raporun, normal bir makinede,
// yavaş kaynaklar dahil eksiksiz gitmesi içindir. Toplayıcı hiç başlatılmadıysa beklemez.
func (b *Background[T]) WaitReady(ctx context.Context, d time.Duration) {
	if !b.started.Load() {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-b.ready:
	case <-t.C:
	case <-ctx.Done():
	}
}
