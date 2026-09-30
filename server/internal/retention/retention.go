// Package retention, sınırsız büyüyen tabloları saklama sürelerine göre temizler: metrik örnekleri, denetim kayıtları ve
// çözülmüş alert'ler. Agent'lar birkaç saniyede bir rapor verir; metrik temizliği olmadan tablo sınırsız büyür (10 sn
// interval x 100 host yılda ~315M satırdır).
package retention

import (
	"context"
	"log/slog"
	"sync/atomic"
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

// Days, her tablonun saklama süresidir (gün); 0 o tabloyu sonsuza dek tutar. Panelden değişir (bkz. SetDays).
type Days struct {
	Metrics        int // metrics_retention_days
	Audit          int // audit_retention_days
	ResolvedAlerts int // resolved_alert_retention_days
}

// Stores, temizliğin sildiği tablolardır.
type Stores struct {
	Metrics *store.Metrics
	Audit   *store.Audit
	Alerts  *store.Alerts
}

// target, saklama süresi olan bir tablodur.
type target struct {
	what    string // log için: "metric samples", "audit log entries", "resolved alerts"
	setting string // süreyi veren ayar
	days    func(Days) int
	purge   func(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
}

type Purger struct {
	targets []target
	days    atomic.Pointer[Days]
	now     func() time.Time
}

func New(st Stores, days Days) *Purger {
	p := &Purger{now: time.Now}
	p.days.Store(&days)
	add := func(what, setting string, d func(Days) int, purge func(context.Context, time.Time, int) (int64, error)) {
		p.targets = append(p.targets, target{what: what, setting: setting, days: d, purge: purge})
	}
	if st.Metrics != nil {
		add("metric samples", "metrics_retention_days", func(d Days) int { return d.Metrics }, st.Metrics.PurgeOlderThan)
	}
	if st.Audit != nil {
		add("audit log entries", "audit_retention_days", func(d Days) int { return d.Audit }, st.Audit.PurgeOlderThan)
	}
	if st.Alerts != nil {
		add("resolved alerts", "resolved_alert_retention_days", func(d Days) int { return d.ResolvedAlerts }, st.Alerts.PurgeResolvedBefore)
	}
	return p
}

// SetDays, saklama sürelerini çalışırken değiştirir; bir sonraki temizlik turu yeni değerleri kullanır.
func (p *Purger) SetDays(days Days) { p.days.Store(&days) }

// RunOnce, saklama süresi dolan satırları siler ve tablo başına kaç tane kaldırdığını döndürür (anahtar: target.what).
// Saklaması kapalı (0 gün) tablolara dokunmaz. Bir tablo başarısız olursa diğerleri yine denenir; ilk hata döner.
func (p *Purger) RunOnce(ctx context.Context) (map[string]int64, error) {
	deleted := map[string]int64{}
	var firstErr error
	days := *p.days.Load()
	for _, t := range p.targets {
		d := t.days(days)
		if d <= 0 {
			continue
		}
		cutoff := p.now().Add(-time.Duration(d) * 24 * time.Hour)
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

// Run, ctx iptal edilene kadar periyodik olarak temizler. Kendi goroutine'inde çalıştırın. Bütün süreler 0 olsa da
// çalışmaya devam eder: bir süre panelden sonradan açılabilir.
func (p *Purger) Run(ctx context.Context) {
	days := *p.days.Load()
	for _, t := range p.targets {
		if d := t.days(days); d > 0 {
			slog.InfoContext(ctx, "retention: keeping "+t.what, "days", d)
		} else {
			slog.InfoContext(ctx, "retention: "+t.setting+"=0, "+t.what+" are kept forever")
		}
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
