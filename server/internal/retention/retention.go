// Package retention, metrics tablosu sınırlı kalsın diye eski metrik örneklerini siler.
// Agent'lar birkaç saniyede bir rapor verir; bu olmadan tablo sınırsız büyür (10 sn interval x
// 100 host yılda ~315M satırdır).
package retention

import (
	"context"
	"log/slog"
	"time"

	"healthbeat-server/internal/store"
)

const (
	// purgeEvery, temizliğin ne sıklıkla çalıştığıdır; saklama gün cinsinden ölçülür, bu yüzden
	// saatlik yeterlidir ve her çalıştırmayı küçük tutar.
	purgeEvery = time.Hour
	// firstRunDelay, temizliği kritik açılış yolundan uzak tutar.
	firstRunDelay = time.Minute
	batchSize     = 10000
)

type Purger struct {
	metrics *store.Metrics
	// days, metrik örneklerinin ne kadar saklandığıdır; 0 temizliği kapatır.
	days int

	now func() time.Time
}

func New(metrics *store.Metrics, days int) *Purger {
	return &Purger{metrics: metrics, days: days, now: time.Now}
}

// RunOnce, saklama penceresinden eski örnekleri siler ve kaç tane kaldırdığını döndürür.
// Saklama kapalıyken (days == 0) etkisizdir.
func (p *Purger) RunOnce(ctx context.Context) (int64, error) {
	if p.days <= 0 {
		return 0, nil
	}
	cutoff := p.now().Add(-time.Duration(p.days) * 24 * time.Hour)
	return p.metrics.PurgeOlderThan(ctx, cutoff, batchSize)
}

// Run, ctx iptal edilene kadar periyodik olarak temizler. Kendi goroutine'inde çalıştırın.
func (p *Purger) Run(ctx context.Context) {
	if p.days <= 0 {
		slog.InfoContext(ctx, "retention: METRICS_RETENTION_DAYS=0, metric samples are kept forever")
		return
	}
	slog.InfoContext(ctx, "retention: keeping metric samples", "days", p.days)

	timer := time.NewTimer(firstRunDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if n, err := p.RunOnce(ctx); err != nil {
				slog.ErrorContext(ctx, "retention: purge failed", "deleted", n, "err", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "retention: deleted expired metric samples", "count", n)
			}
			timer.Reset(purgeEvery)
		}
	}
}
