package store

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

// SaveServices, agent'ın bildirdiği systemd servislerini saklar (protokol 4). Tam rapor (Full) saklanan listeyi
// değiştirir: listede olmayan servisler silinir. Kısmi rapor yalnızca gelen servisleri ekler/günceller. İçeriği
// değişmeyen satır yeniden yazılmaz (tam liste birkaç yüz servistir ve dakikalar içinde bir gelir). svc nil ise
// (bilinmiyor) hiçbir şey yapılmaz.
func (s *Hosts) SaveServices(ctx context.Context, hostID uuid.UUID, svc *model.Services) error {
	if svc == nil {
		return nil
	}
	items := append([]model.Service(nil), svc.Items...)
	// Kararlı sıra: aynı host için eşzamanlı iki rapor satırları aynı sırayla kilitler.
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	n := len(items)
	names, descs, actives, subs, enabled := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	since, restarts := make([]*time.Time, n), make([]*int, n)
	for i, it := range items {
		names[i], descs[i], actives[i], subs[i], enabled[i] = it.Name, it.Description, it.Active, it.Sub, it.Enabled
		if t, err := time.Parse(time.RFC3339, it.Since); err == nil {
			since[i] = &t
		}
		restarts[i] = it.Restarts
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO host_services (host_id, name, description, active, sub, since, restarts, enabled, updated_at)
		 SELECT $1, t.name, NULLIF(t.descr, ''), t.active, NULLIF(t.sub, ''), t.since, t.restarts, NULLIF(t.enabled, ''), now()
		 FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::timestamptz[], $7::int[], $8::text[])
		      AS t(name, descr, active, sub, since, restarts, enabled)
		 ON CONFLICT (host_id, name) DO UPDATE SET
		     description = EXCLUDED.description, active = EXCLUDED.active, sub = EXCLUDED.sub, since = EXCLUDED.since,
		     restarts = EXCLUDED.restarts, enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at
		 WHERE (host_services.description, host_services.active, host_services.sub, host_services.since,
		        host_services.restarts, host_services.enabled)
		       IS DISTINCT FROM
		       (EXCLUDED.description, EXCLUDED.active, EXCLUDED.sub, EXCLUDED.since, EXCLUDED.restarts, EXCLUDED.enabled)`,
		hostID, names, descs, actives, subs, since, restarts, enabled,
	); err != nil {
		return err
	}
	if svc.Full {
		if _, err := tx.Exec(ctx, `DELETE FROM host_services WHERE host_id = $1 AND name <> ALL($2::text[])`, hostID, names); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
