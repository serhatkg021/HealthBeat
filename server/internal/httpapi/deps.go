// Package httpapi, panelin REST API'sini bağlar: yönlendirme, kimlik doğrulama/izin
// middleware'i ve istek işleyicilerinin kendisi.
package httpapi

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"sync/atomic"

	"healthbeat-server/internal/access"
	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/ingest"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/outbox"
	"healthbeat-server/internal/pullscheduler"
	"healthbeat-server/internal/ratelimit"
	"healthbeat-server/internal/rbac"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/settings"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/tlsreload"
)

type Deps struct {
	pool     *pgxpool.Pool
	tokenSvc *authsvc.TokenService

	// agentPolicy, panelBaseURL ve errorBodyBytes panelden çalışırken değişebilir (Set… yöntemleri); istek başına okunur.
	agentPolicy atomic.Pointer[AgentPolicy]

	// perms, rol izinlerinin önbelleğidir (bkz. rbac.Cache); access, istek başına kapsamı (access.Scope) üretir.
	perms  *rbac.Cache
	access *access.Resolver

	// clientIPs, istemci IP'sini TRUSTED_PROXIES'e göre belirler; nil = her zaman TCP eşi (bkz. remoteIP).
	clientIPs *clientip.Resolver

	// pullScheduler ve tlsCerts yalnızca Sistem Araçları'nda (Cache Durumu) gösterilmek için tutulur; nil olabilir.
	pullScheduler *pullscheduler.Scheduler
	tlsCerts      *tlsreload.Reloader
	// logFiles, kalıcı log dosyalarının yazıcısıdır (Sistem Araçları → Log Analiz okur); LOG_FILE boşsa nil.
	logFiles *logging.FileWriter

	users         *store.Users
	organizations *store.Organizations
	hosts         *store.Hosts
	userOrgs      *store.UserOrganizations
	userHosts     *store.UserHosts
	audit         *store.Audit
	contacts      *store.Contacts
	notifs        *store.Notifications
	metrics       *store.Metrics
	thresholds    *store.Thresholds
	alerts        *store.Alerts
	refreshTokens *store.RefreshTokens
	resets        *store.PasswordResets

	// alertEngine, pullscheduler/offlinemonitor ile paylaşılır (main.go'da bir kez kurulur); böylece
	// iki alım yolu da eşikleri aynı şekilde değerlendirir.
	alertEngine *alertengine.Engine
	// ingest, push raporunu kaydeder ve değerlendirir; pull scheduler'ınkiyle aynı yol (bkz. internal/ingest).
	ingest *ingest.Service

	// loginFailures / ingestFailures yalnızca kimlik doğrulama başarısız olduğunda kaynak IP başına
	// sayılır (panel giriş+yenileme, push-host doğrulaması); ingestRate doğrulanmış push host
	// başına sayılır. Bkz. ratelimit ve bunları kullanan handler'lar/middleware.
	loginFailures  *ratelimit.Limiter
	ingestFailures *ratelimit.Limiter
	ingestRate     *ratelimit.Limiter

	// E-posta ile şifre sıfırlama (bkz. password_reset_handlers.go). mailer nil ya da panelBaseURL boşsa özellik kapalıdır.
	// E-postalar bildirim kuyruğuna (outbox) yazılır, mailWorker teslim eder; sıfırlama bağlantısı şifreli saklanır.
	mailer       Mailer // açılışta bir kez ayarlanır (SetPasswordReset)
	panelBaseURL atomic.Pointer[string]
	outbox       *store.Outbox
	mailWorker   *outbox.Worker
	// resetIPs sıfırlama isteklerini kaynak IP başına, resetEmails hedef e-posta başına sınırlar (posta bombası önlemi).
	resetIPs    *ratelimit.Limiter
	resetEmails *ratelimit.Limiter

	// appSettings ve channels, çalışma zamanı ayarları ve bildirim kanallarıdır (SetSettings); owners sistem sahipleridir.
	appSettings *settings.Service
	channels    *settings.Channels
	owners      *store.NotificationOwners
	// maintenance, bakım pencereleridir (bkz. maintenance_handlers.go).
	maintenance *store.MaintenanceWindows

	// errorBodyBytes, hata alan (4xx/5xx) bir isteğin loga yazılan istek/yanıt gövdesinin azami boyutudur; 0 gövde
	// yazmaz. Bkz. requestlog.go.
	errorBodyBytes atomic.Int64
}

// RateLimits, API'nin sınırlayıcılarını yapılandırır (dakikada; 0 kapatır).
type RateLimits struct {
	AuthFailuresPerMinute int
	IngestPerMinute       int
}

const (
	// authFailureBurst, bir IP'nin kararlı hıza kısılmadan önce kaç başarısız deneme hakkı
	// olduğudur; ingestBurst bir ağ aksamasından sonra yetişen host'ı karşılar.
	authFailureBurst = 10
	ingestBurst      = 20
)

// AgentPolicy, panelin agent'ları sınıflandırdığı sürüm politikasıdır (app_settings; bkz. internal/settings).
type AgentPolicy struct {
	Latest string // en güncel agent sürümü; "" = tanımsız
	Min    string // desteklenen en düşük agent sürümü; "" = tanımsız
}

// SetAgentPolicy, GET /api/v1/meta'nın döndürdüğü ve ingest yanıtında önerilen sürüm politikasını ayarlar.
func (d *Deps) SetAgentPolicy(p AgentPolicy) { d.agentPolicy.Store(&p) }

// SetSystemSources, Sistem Araçları'nın (Cache Durumu) API katmanının dışında yaşayan kaynaklarını bağlar: pull
// zamanlayıcı ve TLS sertifika yükleyici. Verilmeyen (nil) kaynak yanıtta yer almaz.
func (d *Deps) SetSystemSources(pull *pullscheduler.Scheduler, certs *tlsreload.Reloader) {
	d.pullScheduler, d.tlsCerts = pull, certs
}

// SetLogFiles, Sistem Araçları'nın (Log Analiz) okuyacağı log dosyası yazıcısını bağlar; nil (LOG_FILE boş) ise
// özellik "kapalı" görünür.
func (d *Deps) SetLogFiles(w *logging.FileWriter) { d.logFiles = w }

// SetClientIPResolver, istemci IP'sinin güvenilir proxy'lerin X-Forwarded-For'undan okunmasını açar (bkz. clientip).
func (d *Deps) SetClientIPResolver(r *clientip.Resolver) { d.clientIPs = r }

// SetErrorBodyLogging, hata alan isteklerin loga yazılan gövdelerinin azami boyutunu ayarlar (log_error_body_bytes);
// 0 gövde yazmaz. Gövdeler her zaman maskelenir (bkz. requestlog.go).
func (d *Deps) SetErrorBodyLogging(maxBytes int) { d.errorBodyBytes.Store(int64(maxBytes)) }

// SetRateLimits, hız sınırlarını çalışırken değiştirir (panelden). Şifre sıfırlama sınırlayıcıları başarısız giriş
// sınırından türer: o kapalıysa onlar da kapalıdır.
func (d *Deps) SetRateLimits(limits RateLimits) {
	d.loginFailures.SetRate(float64(limits.AuthFailuresPerMinute))
	d.ingestFailures.SetRate(float64(limits.AuthFailuresPerMinute))
	d.ingestRate.SetRate(float64(limits.IngestPerMinute))
	d.resetIPs.SetRate(resetIPPerMinute(limits))
	d.resetEmails.SetRate(resetEmailPerMinute(limits))
}

func NewDeps(pool *pgxpool.Pool, tokenSvc *authsvc.TokenService, alertEngine *alertengine.Engine, limits RateLimits, secrets *secretbox.Box) *Deps {
	d := &Deps{
		pool:        pool,
		tokenSvc:    tokenSvc,
		alertEngine: alertEngine,

		loginFailures:  ratelimit.New(float64(limits.AuthFailuresPerMinute), authFailureBurst),
		ingestFailures: ratelimit.New(float64(limits.AuthFailuresPerMinute), authFailureBurst),
		ingestRate:     ratelimit.New(float64(limits.IngestPerMinute), ingestBurst),
		resetIPs:       ratelimit.New(resetIPPerMinute(limits), resetIPBurst),
		resetEmails:    ratelimit.New(resetEmailPerMinute(limits), resetEmailBurst),

		users:         store.NewUsers(pool),
		organizations: store.NewOrganizations(pool),
		hosts:         store.NewHosts(pool, secrets),
		userOrgs:      store.NewUserOrganizations(pool),
		userHosts:     store.NewUserHosts(pool),
		audit:         store.NewAudit(pool),
		contacts:      store.NewContacts(pool),
		notifs:        store.NewNotifications(pool),
		metrics:       store.NewMetrics(pool),
		thresholds:    store.NewThresholds(pool),
		alerts:        store.NewAlerts(pool),
		refreshTokens: store.NewRefreshTokens(pool),
		resets:        store.NewPasswordResets(pool),
		owners:        store.NewNotificationOwners(pool),
		maintenance:   store.NewMaintenanceWindows(pool),
	}
	d.SetAgentPolicy(AgentPolicy{})
	d.SetPanelBaseURL("")
	d.SetErrorBodyLogging(DefaultErrorBodyBytes)
	d.perms = rbac.NewCache(pool, rbac.DefaultCacheTTL)
	d.access = access.NewResolver(d.userOrgs, d.userHosts, d.hosts)
	d.ingest = ingest.New(d.metrics, d.hosts, alertEngine)
	d.outbox = store.NewOutbox(pool, secrets)
	return d
}
