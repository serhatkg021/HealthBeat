package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/rbac"
	"healthbeat-server/internal/store"
)

// requireAuth, bearer access token'ı çıkarır ve doğrular, sonra kimliği doğrulanmış kullanıcının
// kimliğini/rolünü sonraki handler'lar için istek bağlamına ekler.
//
// Şifresini değiştirmesi gereken bir kullanıcı (Claims.MustChangePassword),
// requireAuthEvenIfPasswordChangeDue ile sarılan iki rota dışında her yerde
// 403 password_change_required ile reddedilir.
func (d *Deps) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return d.authenticated(next, false)
}

// requireAuthEvenIfPasswordChangeDue, şifre değiştirme kapısı olmayan requireAuth'tur:
// yalnızca kişinin kendi profilini okuması ve şifreyi değiştirmesi içindir.
func (d *Deps) requireAuthEvenIfPasswordChangeDue(next http.HandlerFunc) http.HandlerFunc {
	return d.authenticated(next, true)
}

func (d *Deps) authenticated(next http.HandlerFunc, allowPasswordChangeDue bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "Bearer token eksik")
			return
		}

		claims, err := d.tokenSvc.ParseAccessToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "geçersiz ya da süresi dolmuş token")
			return
		}

		if claims.MustChangePassword && !allowPasswordChangeDue {
			writeErrorCode(w, http.StatusForbidden, "devam etmeden önce şifrenizi değiştirmelisiniz", errorCodePasswordChangeRequired)
			return
		}

		ctx := withUser(r.Context(), claims.UserID, claims.Email, claims.Role)
		next(w, r.WithContext(ctx))
	}
}

// requirePermission requireAuth'u içerir, sonra çağıranın rolünü permKey için role_permissions
// tablosuna karşı denetler (bkz. internal/rbac — burada asla sabit kodlanmaz).
func (d *Deps) requirePermission(permKey string, next http.HandlerFunc) http.HandlerFunc {
	return d.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		role, _ := roleFromContext(r.Context())

		ok, err := rbac.HasPermission(r.Context(), d.pool, role, permKey)
		if err != nil {
			log.Printf("permission check error: %v", err)
			writeError(w, http.StatusInternalServerError, "yetki denetimi başarısız")
			return
		}
		if !ok {
			writeError(w, http.StatusForbidden, "yetkiniz yok")
			return
		}
		next(w, r)
	})
}

// requireHostAuth, push modundaki bir agent'ı (host kimliği + token) doğrular; panelin JWT kimlik
// doğrulamasından tamamen ayrıdır (bkz. docs/MIMARI.md bölüm 5: "Host auth
// (push/pull) — panel auth'undan tamamen farklı bir konu"). Host kendini X-Host-ID
// ile bir Bearer api_token ile tanıtır; her istekte tüm push modu host'ların hash'ini
// taramak yerine o tek host'ı kimliğiyle bulur ve token'ı saklı hash'ine karşı doğrularız.
func (d *Deps) requireHostAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := remoteIP(r)
		if rejectIfThrottled(w, d.ingestFailures, ip) {
			return
		}

		// Aşağıdaki her 401 kaynak IP'nin başarısızlık bütçesinden düşer; böylece token tahmin eden
		// biri, aynı NAT'ın arkasında doğru kimlik doğrulayan diğer host'ları etkilemeden kısılır.
		reject := func(message string) {
			d.ingestFailures.Allow(ip)
			writeError(w, http.StatusUnauthorized, message)
		}

		hostID, err := uuid.Parse(r.Header.Get("X-Host-ID"))
		if err != nil {
			reject("X-Host-ID başlığı eksik ya da geçersiz")
			return
		}

		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			reject("Bearer token eksik")
			return
		}

		mode, orgID, apiTokenHash, err := d.hosts.GetAuthByID(r.Context(), hostID)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				log.Printf("host auth: lookup host: %v", err)
			}
			reject("geçersiz sunucu kimlik bilgisi")
			return
		}
		if mode != model.HostModePush || apiTokenHash == nil || !authsvc.VerifyOpaqueSecret(*apiTokenHash, token) {
			reject("geçersiz sunucu kimlik bilgisi")
			return
		}

		// Host kimliğine göre anahtarlanır ve yalnızca token doğrulandıktan sonra ulaşılır; bu yüzden
		// bir host'ın kimliğini yalnızca bilen bir saldırgan onun bütçesini tüketemez.
		if ok, retry := d.ingestRate.Allow(hostID.String()); !ok {
			writeTooManyRequests(w, retry)
			return
		}

		next(w, r.WithContext(withHostID(r.Context(), hostID, orgID)))
	}
}

// resolveClientIP, istemci IP'sini istek başına bir kez belirleyip context'e koyar; remoteIP (hız sınırları, denetim
// kaydı) onu okur. Güvenilir proxy yoksa TCP eşidir (bkz. clientip.Resolver.ClientIP).
func (d *Deps) resolveClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(withClientIP(r.Context(), d.clientIPs.ClientIP(r))))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}
