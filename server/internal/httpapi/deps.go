// Package httpapi, panelin REST API'sini bağlar: yönlendirme, kimlik doğrulama/izin
// middleware'i ve istek işleyicilerinin kendisi.
package httpapi

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/ratelimit"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/store"
)

type Deps struct {
	pool     *pgxpool.Pool
	tokenSvc *authsvc.TokenService

	agentPolicy AgentPolicy

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

	// loginFailures / ingestFailures yalnızca kimlik doğrulama başarısız olduğunda kaynak IP başına
	// sayılır (panel giriş+yenileme, push-host doğrulaması); ingestRate doğrulanmış push host
	// başına sayılır. Bkz. ratelimit ve bunları kullanan handler'lar/middleware.
	loginFailures  *ratelimit.Limiter
	ingestFailures *ratelimit.Limiter
	ingestRate     *ratelimit.Limiter

	// E-posta ile şifre sıfırlama (bkz. password_reset_handlers.go). mailer nil ya da panelBaseURL boşsa özellik kapalıdır.
	mailer       Mailer
	panelBaseURL string
	mailWG       sync.WaitGroup
	// resetIPs sıfırlama isteklerini kaynak IP başına, resetEmails hedef e-posta başına sınırlar (posta bombası önlemi).
	resetIPs    *ratelimit.Limiter
	resetEmails *ratelimit.Limiter
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

// AgentPolicy, panelin agent'ları sınıflandırdığı sürüm politikasıdır (bkz. config.Config).
type AgentPolicy struct {
	Latest string // en güncel agent sürümü; "" = tanımsız
	Min    string // desteklenen en düşük agent sürümü; "" = tanımsız
}

// SetAgentPolicy, GET /api/v1/meta'nın döndürdüğü sürüm politikasını ayarlar.
func (d *Deps) SetAgentPolicy(p AgentPolicy) { d.agentPolicy = p }

func NewDeps(pool *pgxpool.Pool, tokenSvc *authsvc.TokenService, alertEngine *alertengine.Engine, limits RateLimits, secrets *secretbox.Box) *Deps {
	return &Deps{
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
	}
}

// RunTokenPurge, ctx iptal edilene kadar süresi çoktan dolmuş refresh token kayıtlarını
// periyodik olarak siler. Kendi goroutine'inde çalıştırın.
func (d *Deps) RunTokenPurge(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := d.refreshTokens.PurgeExpired(ctx); err != nil {
				log.Printf("purge refresh tokens: %v", err)
			} else if n > 0 {
				log.Printf("purged %d expired refresh token record(s)", n)
			}
			if n, err := d.resets.PurgeExpired(ctx); err != nil {
				log.Printf("purge password reset tokens: %v", err)
			} else if n > 0 {
				log.Printf("purged %d expired password reset record(s)", n)
			}
		}
	}
}
