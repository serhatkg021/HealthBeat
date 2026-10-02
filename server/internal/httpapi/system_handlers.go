package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/logging"
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

// ---------------------------------------------------------------- log analiz

const (
	defaultLogPageSize = 200
	maxLogPageSize     = 500
	maxLogQueryLen     = 200
	// logScanTimeout, bir günün taranmasına verilen süredir (bir gün yüzlerce MB olabilir).
	logScanTimeout = 15 * time.Second
)

type logFilesResponse struct {
	// Enabled false ise server dosyaya loglamıyor (LOG_FILE boş ya da dosya açılamadı): gösterilecek bir şey yoktur.
	Enabled bool               `json:"enabled"`
	Days    []logging.DayFiles `json:"days"`
	// TotalBytes diskteki toplam boyut, MaxTotalBytes ve MaxAgeDays saklama sınırlarıdır (Ayarlar → Loglama).
	TotalBytes    int64 `json:"total_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	MaxAgeDays    int   `json:"max_age_days"`
}

// handleLogFiles, GET /api/v1/system/logs: log dosyası olan günler (en yeni önce), boyutları ve saklama sınırları.
func (d *Deps) handleLogFiles(w http.ResponseWriter, r *http.Request) error {
	resp := logFilesResponse{Days: []logging.DayFiles{}}
	if d.logFiles != nil {
		resp.Enabled = true
		resp.Days = d.logFiles.Days()
		for _, day := range resp.Days {
			resp.TotalBytes += day.Bytes
		}
		resp.MaxAgeDays, resp.MaxTotalBytes, _ = d.logFiles.Limits()
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// logReadError, okuyucunun hatasını yanıta çevirir.
func logReadError(err error, op string) error {
	switch {
	case errors.Is(err, logging.ErrBadDay):
		return badRequest("day YYYY-AA-GG biçiminde olmalı")
	case errors.Is(err, logging.ErrNoSuchDay):
		return notFound("o güne ait log dosyası yok")
	case errors.Is(err, context.DeadlineExceeded):
		return newError(http.StatusServiceUnavailable, "log taraması zaman aşımına uğradı: o günün logu çok büyük; günün dosyasını indirip inceleyin")
	}
	return serverErr("log okunamadı", op, err)
}

// handleLogEntries, GET /api/v1/system/logs/entries: bir günün satırları, en yeni önce. ?day= zorunludur; ?level= (en
// düşük seviye), ?q= (metin), ?request_id=, ?from= ve ?to= (RFC3339) ile süzülür; ?before= bir önceki sayfanın
// next_before değeridir. Bir günün ilk sayfasının açılması denetim kaydına yazılır.
func (d *Deps) handleLogEntries(w http.ResponseWriter, r *http.Request) error {
	if d.logFiles == nil {
		return notFound("server dosyaya loglamıyor (LOG_FILE boş)")
	}
	p := r.URL.Query()
	q := logging.Query{Day: p.Get("day"), MinLevel: p.Get("level"), Text: p.Get("q"), RequestID: p.Get("request_id"), Limit: defaultLogPageSize}
	if q.MinLevel != "" {
		if _, err := logging.ParseLevel(q.MinLevel); err != nil {
			return badRequest("level debug, info, warn ya da error olmalı")
		}
	}
	if len(q.Text) > maxLogQueryLen || len(q.RequestID) > maxLogQueryLen {
		return badRequest(fmt.Sprintf("q ve request_id en çok %d karakter olabilir", maxLogQueryLen))
	}
	if v := p.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLogPageSize {
			return badRequest(fmt.Sprintf("limit 1 ile %d arasında olmalı", maxLogPageSize))
		}
		q.Limit = n
	}
	if v := p.Get("before"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return badRequest("geçersiz before")
		}
		q.Before = n
	}
	for name, dst := range map[string]**time.Time{"from": &q.From, "to": &q.To} {
		if v := p.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return badRequest(name + " RFC3339 biçiminde olmalı")
			}
			*dst = &t
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), logScanTimeout)
	defer cancel()
	res, err := d.logFiles.Read(ctx, q)
	if err != nil {
		return logReadError(err, "log entries: read")
	}
	if q.Before == 0 {
		details := map[string]any{}
		for k, v := range map[string]string{"level": q.MinLevel, "q": q.Text, "request_id": q.RequestID, "from": p.Get("from"), "to": p.Get("to")} {
			if v != "" {
				details[k] = v
			}
		}
		d.logAudit(r, "system.logs.view", "log", &q.Day, details)
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// handleLogDownload, GET /api/v1/system/logs/download?day=: günün logu düz metin olarak (parçalar birleştirilmiş, gzip
// açılmış). Denetim kaydına yazılır.
func (d *Deps) handleLogDownload(w http.ResponseWriter, r *http.Request) error {
	if d.logFiles == nil {
		return notFound("server dosyaya loglamıyor (LOG_FILE boş)")
	}
	day := r.URL.Query().Get("day")
	// Başlıklar yazılmadan önce günün var olduğu doğrulanır: gövde başladıktan sonra hata yanıtı verilemez.
	if err := d.logFiles.HasDay(day); err != nil {
		return logReadError(err, "log download")
	}
	d.logAudit(r, "system.logs.download", "log", &day, nil)

	// Büyük bir günün gönderimi server'ın olağan yazma süresini aşabilir.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="healthbeat-server-%s.log"`, day))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := d.logFiles.WriteDay(r.Context(), day, w); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(r.Context(), "log download: interrupted", "day", day, "err", err)
	}
	return nil
}
