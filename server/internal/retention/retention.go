// Package retention, sınırsız büyüyen tabloları saklama sürelerine göre temizler: metrik örnekleri, denetim kayıtları ve
// çözülmüş alert'ler. Agent'lar birkaç saniyede bir rapor verir; metrik temizliği olmadan tablo sınırsız büyür (10 sn
// interval x 100 host yılda ~315M satırdır).
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

// Days, her tablonun saklama süresidir (gün); 0 o tabloyu sonsuza dek tutar.
type Days struct {
	Metrics        int // METRICS_RETENTION_DAYS
	Audit          int // AUDIT_RETENTION_DAYS
	ResolvedAlerts int // RESOLVED_ALERT_RETENTION_DAYS
}

// Stores, temizliğin sildiği tablolardır.
type Stores struct {
	Metrics *store.Metrics
	Audit   *store.Audit
	Alerts  *store.Alerts
}

// target, saklama süresi olan bir tablodur.
type target struct {
	what  string // log için: "metric samples", "audit log entries", "resolved alerts"
	env   string // süreyi veren ortam değişkeni
	days  int
	purge func(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
}

type Purger struct {
	targets []target
	now     func() time.Time
}

func New(st Stores, days Days) *Purger {
	p := &Purger{now: time.Now}
	add := func(what, env string, d int, purge func(context.Context, time.Time, int) (int64, error)) {
		p.targets = append(p.targets, target{what: what, env: env, days: d, purge: purge})
	}
	if st.Metrics != nil {
		add("metric samples", "METRICS_RETENTION_DAYS", days.Metrics, st.Metrics.PurgeOlderThan)
	}
	if st.Audit != nil {
		add("audit log entries", "AUDIT_RETENTION_DAYS", days.Audit, st.Audit.PurgeOlderThan)
	}
	if st.Alerts != nil {
		add("resolved alerts", "RESOLVED_ALERT_RETENTION_DAYS", days.ResolvedAlerts, st.Alerts.PurgeResolvedBefore)
	}
	return p
}

// RunOnce, saklama süresi dolan satırları siler ve tablo başına kaç tane kaldırdığını döndürür (anahtar: target.what).
// Saklaması kapalı (0 gün) tablolara dokunmaz. Bir tablo başarısız olursa diğerleri yine denenir; ilk hata döner.
func (p *Purger) RunOnce(ctx context.Context) (map[string]int64, error) {
	deleted := map[string]int64{}
	var firstErr error
	for _, t := range p.targets {
		if t.days <= 0 {
			continue
		}
		cutoff := p.now().Add(-time.Duration(t.days) * 24 * time.Hour)
		n, err := t.purge(ctx, cutoff, batchSize)
		deleted[t.what] = n
		if err != nil {
			slog.ErrorContext(ctx, "retention: purge failed", "table", t.what, "deleted", n, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if n > 0 {
			slog.InfoContext(ctx, "retention: deleted expired "+t.what, "count", n)
		}
	}
	return deleted, firstErr
}

// Run, ctx iptal edilene kadar periyodik olarak temizler. Kendi goroutine'inde çalıştırın.
func (p *Purger) Run(ctx context.Context) {
	enabled := false
	for _, t := range p.targets {
		if t.days > 0 {
			enabled = true
			slog.InfoContext(ctx, "retention: keeping "+t.what, "days", t.days)
		} else {
			slog.InfoContext(ctx, "retention: "+t.env+"=0, "+t.what+" are kept forever")
		}
	}
	if !enabled {
		return
	}

	timer := time.NewTimer(firstRunDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = p.RunOnce(ctx) // hatalar RunOnce'ta loglanır
			timer.Reset(purgeEvery)
		}
	}
}
