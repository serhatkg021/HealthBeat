package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

const (
	defaultAuditPageSize = 50
	maxAuditPageSize     = 200
)

type auditListResponse struct {
	Items []model.AuditLog `json:"items"`
	// NextCursor, daha fazla (daha eski) satır varsa ayarlanır; ?cursor= olarak geri gönderilir.
	NextCursor *string `json:"next_cursor"`
}

// handleListAuditLogs, GET /api/v1/audit-logs'u sunar (audit.view ile korunur; seed bunu yalnızca
// super_admin'e verir). En yeni önce, keyset ile sayfalanır; böylece yeni satırlar gelmeye
// devam ederken sayfalama doğru ve ucuz kalır.
func (d *Deps) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{
		ActionPrefix: q.Get("action"),
		TargetType:   q.Get("target_type"),
		TargetID:     q.Get("target_id"),
		Limit:        defaultAuditPageSize,
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxAuditPageSize {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit 1 ile %d arasında olmalı", maxAuditPageSize))
			return
		}
		f.Limit = n
	}
	if v := q.Get("user_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "geçersiz user_id")
			return
		}
		f.UserID = &id
	}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, name+" RFC3339 biçiminde olmalı")
				return
			}
			*dst = &t
		}
	}
	if v := q.Get("cursor"); v != "" {
		c, err := decodeAuditCursor(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "geçersiz imleç")
			return
		}
		f.Before = &c
	}

	logs, err := d.audit.List(r.Context(), f)
	if err != nil {
		slog.ErrorContext(r.Context(), "list audit logs", "err", err)
		writeError(w, http.StatusInternalServerError, "denetim kayıtları listelenemedi")
		return
	}

	resp := auditListResponse{Items: logs}
	if len(logs) > f.Limit { // store, sonraki sayfayı anlamak için bir fazladan satır getirdi
		resp.Items = logs[:f.Limit]
		last := resp.Items[len(resp.Items)-1]
		c := encodeAuditCursor(store.AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		resp.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

// İmleç istemciler için opaktır: base64url("<RFC3339Nano created_at>|<id>").
func encodeAuditCursor(c store.AuditCursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeAuditCursor(s string) (store.AuditCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return store.AuditCursor{}, err
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return store.AuditCursor{}, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return store.AuditCursor{}, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return store.AuditCursor{}, err
	}
	return store.AuditCursor{CreatedAt: t, ID: u}, nil
}
