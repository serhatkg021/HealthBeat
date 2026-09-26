// Package offlinemonitor implements docs/MIMARI.md section 8's offline
// detection: "pull modunda host'a ulaşılamazsa veya push modunda beklenen
// sürede veri gelmezse ayrı bir 'host offline' alert'i üretilir."
package offlinemonitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/store"
)

const (
	checkInterval = 10 * time.Second
	// graceMultiplier: bir host, kendi interval_seconds değerinin bu kadar katı süre sessiz
	// kaldıysa bayat sayılır; böylece normal ağ/zamanlama titremesini kurt gelmedi demeden önce
	// sönümler.
	graceMultiplier = 3
)

type Monitor struct {
	hosts  *store.Hosts
	engine *alertengine.Engine
}

func New(pool *pgxpool.Pool, engine *alertengine.Engine) *Monitor {
	return &Monitor{
		hosts:  store.NewHosts(pool, nil), // pull secret'lara asla dokunmaz
		engine: engine,
	}
}

func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	stale, err := m.hosts.ListStale(ctx, graceMultiplier)
	if err != nil {
		slog.ErrorContext(ctx, "offline monitor: list stale hosts", "err", err)
		return
	}

	for _, c := range stale {
		if err := m.hosts.MarkOffline(ctx, c.ID); err != nil {
			slog.ErrorContext(ctx, "offline monitor: mark host offline", "host_id", c.ID.String(), "err", err)
			continue
		}
		m.engine.RaiseOffline(ctx, c.ID, c.OrganizationID)
	}
}
