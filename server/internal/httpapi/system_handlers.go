package httpapi

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"healthbeat-server/internal/outbox"
	"healthbeat-server/internal/store"
)

// Sistem Araçları (panel): server'ın iç durumunu salt okunur gösteren uç noktalar. Her araç kendi izniyle korunur
// (system.queue.view, system.cache.view, system.logs.view); varsayılan olarak yalnızca super_admin'dedir.

const (
	defaultQueuePageSize = 50
	maxQueuePageSize     = 200
)

// dbPoolStats, veritabanı bağlantı havuzunun anlık kullanımıdır (Max: DB_MAX_CONNS).
type dbPoolStats struct {
	Acquired int32 `json:"acquired"` // şu an bir sorgunun kullandığı bağlantılar
	Idle     int32 `json:"idle"`
	Total    int32 `json:"total"` // açık bağlantılar (acquired + idle + kurulmakta olanlar)
	Max      int32 `json:"max"`
}

type queueStatusResponse struct {
	store.OutboxSummary
	// MaxAttempts, bir satırdan vazgeçilmeden önceki en çok deneme sayısıdır ("deneme 3/10").
	MaxAttempts int `json:"max_attempts"`
	// RetainFinishedDays, bitmiş satırların tabloda tutulduğu gün sayısıdır (alert bildirimleri alert'leri durdukça kalır).
	RetainFinishedDays int         `json:"retain_finished_days"`
	DBPool             dbPoolStats `json:"db_pool"`
}

// handleQueueStatus, GET /api/v1/system/queue: bildirim kuyruğunun özeti ve veritabanı bağlantı havuzu.
func (d *Deps) handleQueueStatus(w http.ResponseWriter, r *http.Request) error {
	sum, err := d.outbox.Summary(r.Context())
	if err != nil {
		return serverErr("kuyruk durumu alınamadı", "queue status: summary", err)
	}
	st := d.pool.Stat()
	writeJSON(w, http.StatusOK, queueStatusResponse{
		OutboxSummary:      sum,
		MaxAttempts:        outbox.MaxAttempts,
		RetainFinishedDays: int(outbox.RetainFinished.Hours() / 24),
		DBPool:             dbPoolStats{Acquired: st.AcquiredConns(), Idle: st.IdleConns(), Total: st.TotalConns(), Max: st.MaxConns()},
	})
	return nil
}

type queueItemsResponse struct {
	Items []store.OutboxRow `json:"items"`
	// NextCursor, daha fazla (daha eski) satır varsa ayarlanır; ?cursor= olarak geri gönderilir.
	NextCursor *string `json:"next_cursor"`
}

var (
	queueStatuses = []string{store.OutboxStatusActive, store.OutboxStatusPending, store.OutboxStatusRetrying, store.OutboxStatusSent, store.OutboxStatusFailed}
	queueKinds    = []string{store.OutboxKindAlert, store.OutboxKindPasswordReset, store.OutboxKindPasswordChanged}
)

// handleQueueItems, GET /api/v1/system/queue/items: kuyruk satırları, en yeni önce, keyset ile sayfalı. İleti gövdesi
// hiçbir zaman dönmez. ?status= (active | pending | retrying | sent | failed) ve ?kind= ile daraltılır.
func (d *Deps) handleQueueItems(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := store.OutboxFilter{Status: q.Get("status"), Kind: q.Get("kind"), Limit: defaultQueuePageSize}
	if f.Status != "" && !slices.Contains(queueStatuses, f.Status) {
		return badRequest("geçersiz status")
	}
	if f.Kind != "" && !slices.Contains(queueKinds, f.Kind) {
		return badRequest("geçersiz kind")
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxQueuePageSize {
			return badRequest(fmt.Sprintf("limit 1 ile %d arasında olmalı", maxQueuePageSize))
		}
		f.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		c, err := decodeAuditCursor(v)
		if err != nil {
			return badRequest("geçersiz imleç")
		}
		f.Before = &c
	}

	items, err := d.outbox.List(r.Context(), f)
	if err != nil {
		return serverErr("kuyruk listelenemedi", "queue items: list", err)
	}
	resp := queueItemsResponse{Items: items}
	if len(items) > f.Limit { // store, sonraki sayfayı anlamak için bir fazladan satır getirdi
		resp.Items = items[:f.Limit]
		last := resp.Items[len(resp.Items)-1]
		c := encodeAuditCursor(store.OutboxCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		resp.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
