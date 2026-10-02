package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/outbox"
	"healthbeat-server/internal/ratelimit"
	"healthbeat-server/internal/rbac"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/tlsreload"
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

// ---------------------------------------------------------------- cache durumu

// maxLimiterEntries, bir hız sınırlayıcının yanıtta listelenen en çok anahtar sayısıdır (toplam ayrıca verilir).
const maxLimiterEntries = 200

// Hız sınırlayıcı anahtarlarının türleri: panel anahtarı buna göre etiketler.
const (
	limiterKeyIP    = "ip"
	limiterKeyEmail = "email"
	limiterKeyHost  = "host"
)

type limiterEntryView struct {
	ratelimit.Entry
	// Label, anahtarın okunur adıdır (host anahtarında sunucu başlığı); yoksa boş.
	Label string `json:"label"`
}

type limiterView struct {
	ID      string  `json:"id"`
	KeyKind string  `json:"key_kind"`
	PerMin  float64 `json:"per_minute"`
	Burst   int     `json:"burst"`
	Enabled bool    `json:"enabled"`
	// Keys, hakkı eksik olan anahtar sayısıdır; Entries en çok maxLimiterEntries tanesini taşır.
	Keys    int                `json:"keys"`
	Entries []limiterEntryView `json:"entries"`
}

type polledHostView struct {
	HostID       uuid.UUID `json:"host_id"`
	Title        string    `json:"title"` // host silinmişse boş
	LastPolledAt time.Time `json:"last_polled_at"`
	InFlight     bool      `json:"in_flight"`
}

type pullSchedulerView struct {
	VerifiesTLS bool             `json:"verifies_tls"`
	Hosts       []polledHostView `json:"hosts"`
}

// cacheStatusResponse, server sürecinin bellekte tuttuğu durumdur. Yalnızca bu isteği karşılayan süreci gösterir.
// Bağlı olmayan bir kaynak (ör. TRUSTED_PROXIES boş) null döner. Hiçbir sır (şifre, token, özel anahtar) içermez.
type cacheStatusResponse struct {
	GeneratedAt    time.Time           `json:"generated_at"`
	Permissions    rbac.CacheSnapshot  `json:"permissions"`
	RateLimiters   []limiterView       `json:"rate_limiters"`
	PullScheduler  *pullSchedulerView  `json:"pull_scheduler"`
	TrustedProxies *clientip.Snapshot  `json:"trusted_proxies"`
	TLSCertificate *tlsreload.Snapshot `json:"tls_certificate"`
}

// handleCacheStatus, GET /api/v1/system/cache: izin önbelleği, hız sınırlayıcılar, pull zamanlayıcı, güvenilir
// proxy'ler ve sunulan TLS sertifikası.
func (d *Deps) handleCacheStatus(w http.ResponseWriter, r *http.Request) error {
	resp := cacheStatusResponse{GeneratedAt: time.Now(), Permissions: d.perms.Snapshot()}

	limiters := []struct {
		id, kind string
		l        *ratelimit.Limiter
	}{
		{"login_failures", limiterKeyIP, d.loginFailures},
		{"ingest_failures", limiterKeyIP, d.ingestFailures},
		{"ingest_rate", limiterKeyHost, d.ingestRate},
		{"reset_ips", limiterKeyIP, d.resetIPs},
		{"reset_emails", limiterKeyEmail, d.resetEmails},
	}
	var pull *pullSchedulerView
	if d.pullScheduler != nil {
		snap := d.pullScheduler.Snapshot()
		pull = &pullSchedulerView{VerifiesTLS: snap.VerifiesTLS, Hosts: make([]polledHostView, 0, len(snap.Hosts))}
		for _, h := range snap.Hosts {
			pull.Hosts = append(pull.Hosts, polledHostView{HostID: h.HostID, LastPolledAt: h.LastPolledAt, InFlight: h.InFlight})
		}
	}

	// Host kimliği taşıyan satırlar sunucu başlığıyla gösterilir (tek sorgu).
	var hostIDs []uuid.UUID
	for _, lim := range limiters {
		snap := lim.l.Snapshot(maxLimiterEntries)
		view := limiterView{ID: lim.id, KeyKind: lim.kind, PerMin: snap.PerMinute, Burst: snap.Burst, Enabled: snap.Enabled,
			Keys: snap.Keys, Entries: make([]limiterEntryView, 0, len(snap.Entries))}
		for _, e := range snap.Entries {
			view.Entries = append(view.Entries, limiterEntryView{Entry: e})
			if id, err := uuid.Parse(e.Key); lim.kind == limiterKeyHost && err == nil {
				hostIDs = append(hostIDs, id)
			}
		}
		resp.RateLimiters = append(resp.RateLimiters, view)
	}
	if pull != nil {
		for _, h := range pull.Hosts {
			hostIDs = append(hostIDs, h.HostID)
		}
	}
	titles := map[string]string{}
	if len(hostIDs) > 0 {
		hosts, err := d.hosts.ListByIDs(r.Context(), hostIDs)
		if err != nil {
			return serverErr("cache durumu alınamadı", "cache status: host titles", err)
		}
		for _, h := range hosts {
			titles[h.ID.String()] = h.Title
		}
	}
	for i := range resp.RateLimiters {
		if resp.RateLimiters[i].KeyKind != limiterKeyHost {
			continue
		}
		for j := range resp.RateLimiters[i].Entries {
			resp.RateLimiters[i].Entries[j].Label = titles[resp.RateLimiters[i].Entries[j].Key]
		}
	}
	if pull != nil {
		for i := range pull.Hosts {
			pull.Hosts[i].Title = titles[pull.Hosts[i].HostID.String()]
		}
		resp.PullScheduler = pull
	}

	if d.clientIPs != nil {
		snap := d.clientIPs.Snapshot()
		resp.TrustedProxies = &snap
	}
	if d.tlsCerts != nil {
		if snap, err := d.tlsCerts.Snapshot(); err != nil {
			slog.WarnContext(r.Context(), "cache status: tls certificate", "err", err)
		} else {
			resp.TLSCertificate = &snap
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
