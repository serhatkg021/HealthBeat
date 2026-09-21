package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

type Audit struct {
	pool *pgxpool.Pool
}

func NewAudit(pool *pgxpool.Pool) *Audit {
	return &Audit{pool: pool}
}

// Write, kritik bir eylemi kaydeder (bkz. docs/MIMARI.md bölüm 5). Hata durumunda
// çağıranın isteğini asla başarısız kılmaz — denetim kaydı işlemsel bir garanti değil, en iyi
// çaba gözlemlenebilirliğidir — bu yüzden çağıranlar dönen hatayı loglamalı, iptal etmemeli.
// ip isteğin geldiği adrestir (boş = bilinmiyor).
func (s *Audit) Write(ctx context.Context, actorUserID *uuid.UUID, actorEmail, action, targetType string, targetID *string, details any, ip string) error {
	// pgx onu bir bytea parametresi yerine jsonb metin girdisi olarak kodlasın diye string'e
	// serileştirilir ([]byte bırakılmaz).
	var detailsParam any
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return err
		}
		detailsParam = string(b)
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO audit_logs (user_id, actor_email, action, target_type, target_id, details, ip)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7::text, '')::inet)`,
		actorUserID, actorEmail, action, targetType, targetID, detailsParam, ip,
	)
	return err
}

// AuditCursor, (created_at DESC, id DESC) sıralamasında bir keyset konumudur.
type AuditCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// AuditFilter Audit.List'i daraltır; sıfır alanlar "filtre yok" demektir.
type AuditFilter struct {
	ActionPrefix string // "host." her host eylemiyle eşleşir
	TargetType   string
	TargetID     string
	UserID       *uuid.UUID
	From, To     *time.Time // created_at >= From, created_at < To
	Before       *AuditCursor
	Limit        int
}

// List, denetim satırlarını en yeniden başlayarak döndürür. Çağıran ikinci bir sorgu olmadan
// başka bir sayfa olup olmadığını anlayabilsin diye veritabanından Limit+1 satır ister; fazla
// satırı çağıran kırpar.
func (s *Audit) List(ctx context.Context, f AuditFilter) ([]model.AuditLog, error) {
	var beforeTime *time.Time
	var beforeID *uuid.UUID
	if f.Before != nil {
		beforeTime, beforeID = &f.Before.CreatedAt, &f.Before.ID
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, actor_email, action, target_type, target_id, details, host(ip), created_at
		 FROM audit_logs
		 WHERE ($1::text IS NULL OR starts_with(action, $1))
		   AND ($2::text IS NULL OR target_type = $2)
		   AND ($3::text IS NULL OR target_id = $3)
		   AND ($4::uuid IS NULL OR user_id = $4)
		   AND ($5::timestamptz IS NULL OR created_at >= $5)
		   AND ($6::timestamptz IS NULL OR created_at < $6)
		   AND ($7::timestamptz IS NULL OR (created_at, id) < ($7, $8::uuid))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $9`,
		nullIfEmpty(f.ActionPrefix), nullIfEmpty(f.TargetType), nullIfEmpty(f.TargetID),
		f.UserID, f.From, f.To, beforeTime, beforeID, f.Limit+1,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := []model.AuditLog{}
	for rows.Next() {
		var l model.AuditLog
		var details []byte
		if err := rows.Scan(&l.ID, &l.UserID, &l.ActorEmail, &l.Action, &l.TargetType, &l.TargetID, &details, &l.IP, &l.CreatedAt); err != nil {
			return nil, err
		}
		if len(details) > 0 {
			l.Details = json.RawMessage(details)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
