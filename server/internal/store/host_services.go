package store

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

// restartHistoryWindow, servis yeniden başlatma geçmişinin ne kadar tutulduğudur (alert 10 dakikaya bakar).
const restartHistoryWindow = time.Hour

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

	// restart_history: sayaç arttıkça [an, önceki, yeni] eklenir, son 1 saat tutulur; sayaç geri giderse (servis elle
	// yeniden başlatıldı, makine yeniden açıldı) ya da ilk kez bilinirse boşalır (artış sayılmaz).
	if _, err := tx.Exec(ctx,
		`INSERT INTO host_services (host_id, name, description, active, sub, since, restarts, enabled, restart_history, updated_at)
		 SELECT $1, t.name, NULLIF(t.descr, ''), t.active, NULLIF(t.sub, ''), t.since, t.restarts, NULLIF(t.enabled, ''),
		        '[]'::jsonb, now()
		 FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::timestamptz[], $7::int[], $8::text[])
		      AS t(name, descr, active, sub, since, restarts, enabled)
		 ON CONFLICT (host_id, name) DO UPDATE SET
		     description = EXCLUDED.description, active = EXCLUDED.active, sub = EXCLUDED.sub, since = EXCLUDED.since,
		     restarts = COALESCE(EXCLUDED.restarts, host_services.restarts), -- bilinmiyorsa son bilinen kalır
		     enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at,
		     restart_history = CASE
		         WHEN EXCLUDED.restarts IS NULL THEN host_services.restart_history
		         WHEN host_services.restarts IS NULL OR EXCLUDED.restarts < host_services.restarts THEN '[]'::jsonb
		         WHEN EXCLUDED.restarts > host_services.restarts THEN
		             jsonb_path_query_array(COALESCE(host_services.restart_history, '[]'::jsonb), 'strict $[*] ? (@[0] >= $min)',
		                 jsonb_build_object('min', extract(epoch FROM now())::bigint - $9::bigint))
		             || jsonb_build_array(jsonb_build_array(extract(epoch FROM now())::bigint, host_services.restarts, EXCLUDED.restarts))
		         ELSE host_services.restart_history END
		 WHERE (host_services.description, host_services.active, host_services.sub, host_services.since,
		        host_services.restarts, host_services.enabled)
		       IS DISTINCT FROM
		       (EXCLUDED.description, EXCLUDED.active, EXCLUDED.sub, EXCLUDED.since,
		        COALESCE(EXCLUDED.restarts, host_services.restarts), EXCLUDED.enabled)`,
		hostID, names, descs, actives, subs, since, restarts, enabled, int64(restartHistoryWindow/time.Second),
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

// Services, sunucunun saklanan servislerini ada göre sıralı döndürür; Watched izlenen servis seçiminden gelir.
func (s *Hosts) Services(ctx context.Context, hostID uuid.UUID) ([]model.HostService, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.name, COALESCE(s.description, ''), s.active, COALESCE(s.sub, ''), s.since, s.restarts, COALESCE(s.enabled, ''),
		        s.updated_at, w.name IS NOT NULL, COALESCE(s.restart_history, '[]'::jsonb)
		 FROM host_services s
		 LEFT JOIN host_watched_services w ON w.host_id = s.host_id AND w.name = s.name
		 WHERE s.host_id = $1
		 ORDER BY s.name`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.HostService{}
	for rows.Next() {
		var sv model.HostService
		var history []byte
		if err := rows.Scan(&sv.Name, &sv.Description, &sv.Active, &sv.Sub, &sv.Since, &sv.Restarts, &sv.Enabled,
			&sv.UpdatedAt, &sv.Watched, &history); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(history, &sv.RestartHistory); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// WatchedServices, sunucunun izlenen servis seçimini ada göre sıralı döndürür (şu an raporlanmayanlar dahil).
func (s *Hosts) WatchedServices(ctx context.Context, hostID uuid.UUID) ([]string, error) {
	var names []string
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(array_agg(name ORDER BY name), '{}'::text[]) FROM host_watched_services WHERE host_id = $1`, hostID).
		Scan(&names)
	return names, err
}

// SetWatchedServices, izlenen servis seçimini names ile değiştirir (boş liste = hiçbiri). Sunucu yoksa ErrNotFound.
func (s *Hosts) SetWatchedServices(ctx context.Context, hostID uuid.UUID, names []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Sunucu satırı kilitlenir: aynı sunucu için eşzamanlı iki seçim sırayla uygulanır ve silinmiş sunucu ErrNotFound
	// olur. NO KEY: metrik yazan ingest'in (yabancı anahtar kontrolü) önünü kesmez.
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT true FROM hosts WHERE id = $1 FOR NO KEY UPDATE`, hostID).Scan(&exists); err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM host_watched_services WHERE host_id = $1`, hostID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO host_watched_services (host_id, name) SELECT $1, unnest($2::text[])`, hostID, names); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
