package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// Bakım penceresi hataları: alanlar migration 000008'deki kısıtlara uymuyor, kapsamdaki bir sunucu ya da organizasyon
// yok, pencere zaten bitirilmiş.
var (
	ErrMaintenanceWindowInvalid = reason(ErrConflict, "invalid maintenance window")
	ErrMaintenanceScopeMissing  = reason(ErrNotFound, "maintenance window host or organization does not exist")
	ErrMaintenanceWindowEnded   = reason(ErrConflict, "maintenance window already ended")
)

// MaintenanceWindows, bakım pencerelerini, kapsamlarını ve tek tek tekrarların istisnalarını yönetir. Pencerenin şu an
// sürüp sürmediğini burası değil internal/maintenance hesaplar (tekrarlar kurulumun saat dilimine bağlıdır).
type MaintenanceWindows struct {
	pool *pgxpool.Pool
}

func NewMaintenanceWindows(pool *pgxpool.Pool) *MaintenanceWindows {
	return &MaintenanceWindows{pool: pool}
}

const maintenanceColumns = `id, title, recurrence, starts_at, ends_at, start_minute, duration_minutes, repeat_every,
	weekdays, month_day, month_week, month_weekday, valid_from, valid_until, ended_at, created_by, created_at, updated_at`

func scanMaintenanceWindow(row interface{ Scan(...any) error }) (model.MaintenanceWindow, error) {
	var w model.MaintenanceWindow
	err := row.Scan(&w.ID, &w.Title, &w.Recurrence, &w.StartsAt, &w.EndsAt, &w.StartMinute, &w.DurationMinutes, &w.RepeatEvery,
		&w.Weekdays, &w.MonthDay, &w.MonthWeek, &w.MonthWeekday, &w.ValidFrom, &w.ValidUntil, &w.EndedAt, &w.CreatedBy,
		&w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func mapMaintenanceError(err error) error {
	switch pgErrorCode(err) {
	case pgCheckViolation:
		return ErrMaintenanceWindowInvalid
	case pgForeignKeyViolation:
		return ErrMaintenanceScopeMissing
	}
	return err
}

// Create, pencereyi kapsamıyla birlikte ekler ve kaydedilmiş hâlini döndürür. ID, CreatedAt, UpdatedAt, EndedAt ve
// Overrides yok sayılır.
func (s *MaintenanceWindows) Create(ctx context.Context, w model.MaintenanceWindow) (model.MaintenanceWindow, error) {
	var id uuid.UUID
	err := InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO maintenance_windows (title, recurrence, starts_at, ends_at, start_minute, duration_minutes, repeat_every,
			     weekdays, month_day, month_week, month_weekday, valid_from, valid_until, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
			w.Title, w.Recurrence, w.StartsAt, w.EndsAt, w.StartMinute, w.DurationMinutes, w.RepeatEvery,
			w.Weekdays, w.MonthDay, w.MonthWeek, w.MonthWeekday, w.ValidFrom, w.ValidUntil, w.CreatedBy).Scan(&id); err != nil {
			return err
		}
		return insertMaintenanceScope(ctx, tx, id, w.HostIDs, w.OrgIDs)
	})
	if err != nil {
		return model.MaintenanceWindow{}, mapMaintenanceError(err)
	}
	return s.Get(ctx, id)
}

// Update, pencerenin alanlarını ve kapsamını w'dekilerle değiştirir. Bitirilmiş pencere değiştirilemez. İstisnalar
// korunur: artık var olmayan bir tekrarın istisnası hiçbir şeyi etkilemez.
func (s *MaintenanceWindows) Update(ctx context.Context, w model.MaintenanceWindow) (model.MaintenanceWindow, error) {
	err := InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var ended *time.Time
		if err := tx.QueryRow(ctx, `SELECT ended_at FROM maintenance_windows WHERE id = $1 FOR UPDATE`, w.ID).Scan(&ended); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if ended != nil {
			return ErrMaintenanceWindowEnded
		}
		if _, err := tx.Exec(ctx,
			`UPDATE maintenance_windows SET title = $2, recurrence = $3, starts_at = $4, ends_at = $5, start_minute = $6,
			     duration_minutes = $7, repeat_every = $8, weekdays = $9, month_day = $10, month_week = $11,
			     month_weekday = $12, valid_from = $13, valid_until = $14, updated_at = now()
			 WHERE id = $1`,
			w.ID, w.Title, w.Recurrence, w.StartsAt, w.EndsAt, w.StartMinute, w.DurationMinutes, w.RepeatEvery,
			w.Weekdays, w.MonthDay, w.MonthWeek, w.MonthWeekday, w.ValidFrom, w.ValidUntil); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM maintenance_window_hosts WHERE window_id = $1`, w.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM maintenance_window_orgs WHERE window_id = $1`, w.ID); err != nil {
			return err
		}
		return insertMaintenanceScope(ctx, tx, w.ID, w.HostIDs, w.OrgIDs)
	})
	if err != nil {
		return model.MaintenanceWindow{}, mapMaintenanceError(err)
	}
	return s.Get(ctx, w.ID)
}

func insertMaintenanceScope(ctx context.Context, tx pgx.Tx, id uuid.UUID, hostIDs, orgIDs []uuid.UUID) error {
	if len(hostIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO maintenance_window_hosts (window_id, host_id) SELECT $1, h FROM unnest($2::uuid[]) AS h ON CONFLICT DO NOTHING`,
			id, hostIDs); err != nil {
			return err
		}
	}
	if len(orgIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO maintenance_window_orgs (window_id, organization_id) SELECT $1, o FROM unnest($2::uuid[]) AS o ON CONFLICT DO NOTHING`,
			id, orgIDs); err != nil {
			return err
		}
	}
	return nil
}

// Delete pencereyi kapsamı ve istisnalarıyla siler.
func (s *MaintenanceWindows) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM maintenance_windows WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get pencereyi kapsamı ve istisnalarıyla okur.
func (s *MaintenanceWindows) Get(ctx context.Context, id uuid.UUID) (model.MaintenanceWindow, error) {
	w, err := scanMaintenanceWindow(s.pool.QueryRow(ctx, `SELECT `+maintenanceColumns+` FROM maintenance_windows WHERE id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return model.MaintenanceWindow{}, ErrNotFound
		}
		return model.MaintenanceWindow{}, err
	}
	out := []model.MaintenanceWindow{w}
	if err := s.loadDetails(ctx, out, true); err != nil {
		return model.MaintenanceWindow{}, err
	}
	return out[0], nil
}

// MaintenanceVisibility, bir listenin görülebilen kapsamıdır: pencere, kapsamındaki sunuculardan ya da
// organizasyonlardan en az biri burada varsa görünür. nil = süzgeç yok (her pencere).
type MaintenanceVisibility struct {
	HostIDs []uuid.UUID
	OrgIDs  []uuid.UUID
}

// List pencereleri (en yenisi önce) kapsamları ve istisnalarıyla döndürür.
func (s *MaintenanceWindows) List(ctx context.Context, vis *MaintenanceVisibility) ([]model.MaintenanceWindow, error) {
	query := `SELECT ` + maintenanceColumns + ` FROM maintenance_windows w`
	var args []any
	if vis != nil {
		query += ` WHERE EXISTS (SELECT 1 FROM maintenance_window_hosts h WHERE h.window_id = w.id AND h.host_id = ANY($1))
		              OR EXISTS (SELECT 1 FROM maintenance_window_orgs o WHERE o.window_id = w.id AND o.organization_id = ANY($2))`
		args = []any{nonNilIDs(vis.HostIDs), nonNilIDs(vis.OrgIDs)}
	}
	rows, err := s.pool.Query(ctx, query+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	out, err := collect(rows, scanMaintenanceWindow)
	if err != nil {
		return nil, err
	}
	return out, s.loadDetails(ctx, out, true)
}

// ForHost, hostID'yi doğrudan ya da doğrudan bağlı olduğu organizasyon üzerinden kapsayan ve now'da henüz geride
// kalmamış pencereleri istisnalarıyla döndürür (kapsam listeleri doldurulmaz). Sunucunun şu an bakımda olup olmadığını
// bunlardan internal/maintenance hesaplar. Geride kalmış: bitirilmiş, bitişi geçmiş tek seferlik ya da son tekrar günü
// en uzun süreden (7 gün) daha eski tekrarlı pencere.
func (s *MaintenanceWindows) ForHost(ctx context.Context, hostID uuid.UUID, now time.Time) ([]model.MaintenanceWindow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+maintenanceColumns+` FROM maintenance_windows w
		WHERE (w.ended_at IS NULL OR w.ended_at > $2)
		  AND (w.recurrence <> 'once' OR w.ends_at > $2)
		  AND (w.valid_until IS NULL OR w.valid_until >= ($2::timestamptz - interval '8 days')::date)
		  AND (EXISTS (SELECT 1 FROM maintenance_window_hosts h WHERE h.window_id = w.id AND h.host_id = $1)
		       OR EXISTS (SELECT 1 FROM maintenance_window_orgs o JOIN hosts ho ON ho.organization_id = o.organization_id
		                  WHERE o.window_id = w.id AND ho.id = $1))
		ORDER BY w.created_at, w.id`, hostID, now)
	if err != nil {
		return nil, err
	}
	out, err := collect(rows, scanMaintenanceWindow)
	if err != nil {
		return nil, err
	}
	return out, s.loadDetails(ctx, out, false)
}

// Open, now'da henüz geride kalmamış bütün pencereleri kapsamları ve istisnalarıyla döndürür (ForHost'un süzgeci,
// sunucu süzgeci olmadan): sunucu listelerinde hangi sunucunun bakımda olduğunu tek sorguyla bulmak için.
func (s *MaintenanceWindows) Open(ctx context.Context, now time.Time) ([]model.MaintenanceWindow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+maintenanceColumns+` FROM maintenance_windows w
		WHERE (w.ended_at IS NULL OR w.ended_at > $1)
		  AND (w.recurrence <> 'once' OR w.ends_at > $1)
		  AND (w.valid_until IS NULL OR w.valid_until >= ($1::timestamptz - interval '8 days')::date)
		ORDER BY w.created_at, w.id`, now)
	if err != nil {
		return nil, err
	}
	out, err := collect(rows, scanMaintenanceWindow)
	if err != nil {
		return nil, err
	}
	return out, s.loadDetails(ctx, out, true)
}

// End pencereyi at anında bitirir: seri kapanır, süren tekrar da biter.
func (s *MaintenanceWindows) End(ctx context.Context, id uuid.UUID, at time.Time) (model.MaintenanceWindow, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE maintenance_windows SET ended_at = $2, updated_at = now() WHERE id = $1 AND ended_at IS NULL`, id, at)
	if err != nil {
		return model.MaintenanceWindow{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return model.MaintenanceWindow{}, err
		}
		return model.MaintenanceWindow{}, ErrMaintenanceWindowEnded
	}
	return s.Get(ctx, id)
}

// SetOverride, occurrenceStart'ta başlayan tekrarın istisnasını yazar: endedAt nil ise tekrar atlanır, doluysa o anda
// erken biter. Aynı tekrar için önceki istisnanın yerine geçer.
func (s *MaintenanceWindows) SetOverride(ctx context.Context, id uuid.UUID, occurrenceStart time.Time, endedAt *time.Time, by *uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO maintenance_occurrence_overrides (window_id, occurrence_start, ended_at, created_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (window_id, occurrence_start) DO UPDATE SET ended_at = EXCLUDED.ended_at, created_by = EXCLUDED.created_by, created_at = now()`,
		id, occurrenceStart, endedAt, by)
	if pgErrorCode(err) == pgForeignKeyViolation {
		return ErrNotFound
	}
	return err
}

// loadDetails, pencerelerin istisnalarını (ve scope true ise kapsamlarını) toplu okuyup doldurur.
func (s *MaintenanceWindows) loadDetails(ctx context.Context, ws []model.MaintenanceWindow, scope bool) error {
	if len(ws) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(ws))
	index := make(map[uuid.UUID]int, len(ws))
	for i := range ws {
		ids[i] = ws[i].ID
		index[ws[i].ID] = i
		if scope {
			ws[i].HostIDs, ws[i].OrgIDs = []uuid.UUID{}, []uuid.UUID{}
		}
	}
	rows, err := s.pool.Query(ctx,
		`SELECT window_id, occurrence_start, ended_at FROM maintenance_occurrence_overrides
		 WHERE window_id = ANY($1) ORDER BY occurrence_start`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var o model.MaintenanceOverride
		if err := rows.Scan(&id, &o.OccurrenceStart, &o.EndedAt); err != nil {
			rows.Close()
			return err
		}
		ws[index[id]].Overrides = append(ws[index[id]].Overrides, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !scope {
		return nil
	}
	for _, q := range []struct {
		sql string
		add func(i int, id uuid.UUID)
	}{
		{`SELECT window_id, host_id FROM maintenance_window_hosts WHERE window_id = ANY($1) ORDER BY host_id`,
			func(i int, id uuid.UUID) { ws[i].HostIDs = append(ws[i].HostIDs, id) }},
		{`SELECT window_id, organization_id FROM maintenance_window_orgs WHERE window_id = ANY($1) ORDER BY organization_id`,
			func(i int, id uuid.UUID) { ws[i].OrgIDs = append(ws[i].OrgIDs, id) }},
	} {
		rows, err := s.pool.Query(ctx, q.sql, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var wid, id uuid.UUID
			if err := rows.Scan(&wid, &id); err != nil {
				rows.Close()
				return err
			}
			q.add(index[wid], id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// nonNilIDs, nil dilimi boş dizi olarak gönderir (ANY(NULL) hiçbir şeyle eşleşmez ama açık olsun).
func nonNilIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
