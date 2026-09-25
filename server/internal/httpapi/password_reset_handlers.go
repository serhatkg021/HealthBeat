package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// E-posta ile şifre sıfırlama. Akış: kullanıcı e-postasını girer → server tek kullanımlık, kısa ömürlü bir bağlantı
// e-postalar → bağlantıdaki token ile yeni şifre seçilir.
//
// Güvenlik kararları:
//   - "Bu e-posta kayıtlı mı?" sorusu yanıttan öğrenilemez: geçerli biçimdeki her e-posta için aynı 204 döner.
//   - Ham token yalnızca e-postada bulunur; veritabanında yalnızca SHA-256 özeti saklanır.
//   - Bağlantı adresin #parçasında taşınır (?sorgu değil): tarayıcı bunu server'a, proxy günlüklerine ya da
//     Referer başlığına göndermez.
//   - Bağlantının kökü PANEL_BASE_URL'den gelir, isteğin Host/Origin başlığından değil (başlık enjeksiyonuyla
//     saldırgan bir alan adına bağlantı üretilmesini önler).
//   - Kullanıcı başına yalnızca son bağlantı geçerlidir; kullanım sonrası tüm oturumlar kapatılır.
//   - IP başına ve e-posta başına sınır: posta bombası ve token tahmini denemeleri kısılır.

// errorCodeResetLinkInvalid, panelin "yeni bağlantı iste" durumunu mesaj metnine bakmadan ayırt etmesini sağlar.
const errorCodeResetLinkInvalid = "reset_link_invalid"

// passwordResetTTL, sıfırlama bağlantısının geçerlilik süresidir.
const passwordResetTTL = 30 * time.Minute

const (
	resetIPBurst      = 5
	resetEmailBurst   = 3
	maxResetTokenLen  = 200
	mailSendTimeout   = 45 * time.Second
	resetMailSubject  = "HealthBeat şifre sıfırlama"
	resetDoneSubject  = "HealthBeat şifreniz değiştirildi"
	resetLinkPathFmt  = "%s/reset-password#token=%s"
	maxForgotEmailLen = 254
)

// resetIPPerMinute/resetEmailPerMinute, sınırlayıcıların dolum hızıdır; AuthFailuresPerMinute 0 ise (sınırlar
// kapalı) ikisi de kapanır. IP başına dakikada 2, e-posta başına 10 dakikada 1 istek dolar.
func resetIPPerMinute(l RateLimits) float64 {
	if l.AuthFailuresPerMinute <= 0 {
		return 0
	}
	return 2
}

func resetEmailPerMinute(l RateLimits) float64 {
	if l.AuthFailuresPerMinute <= 0 {
		return 0
	}
	return 0.1
}

// Mailer, sıfırlama e-postalarını gönderen bileşendir (notify.Mailer ile uyumlu).
type Mailer interface {
	Send(ctx context.Context, to []string, subject, body string) error
	Enabled() bool
}

// SetPasswordReset, e-posta ile şifre sıfırlamayı açar. baseURL panelin dış adresidir ("https://panel.example.com");
// boşsa ya da mailer gerçek bir SMTP sunucusuna bağlı değilse özellik kapalı kalır (istekler 204 döner ama posta gitmez).
func (d *Deps) SetPasswordReset(m Mailer, baseURL string) {
	d.mailer = m
	d.panelBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func (d *Deps) passwordResetEnabled() bool {
	return d.mailer != nil && d.mailer.Enabled() && d.panelBaseURL != ""
}

// WaitForMail, arka planda giden e-postaların bitmesini bekler (testler ve düzgün kapanış için).
func (d *Deps) WaitForMail(ctx context.Context) error {
	done := make(chan struct{})
	go func() { d.mailWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sendMailAsync e-postayı isteğin dışında gönderir; böylece yanıt süresi "e-posta kayıtlı mı" bilgisini sızdırmaz
// ve yavaş bir SMTP sunucusu isteği bekletmez. Hata yalnızca loglanır (kullanıcıya bildirilemez).
func (d *Deps) sendMailAsync(to, subject, body string) {
	d.mailWG.Add(1)
	go func() {
		defer d.mailWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), mailSendTimeout)
		defer cancel()
		defer logging.Recover(ctx, "password reset mail")
		if err := d.mailer.Send(ctx, []string{to}, subject, body); err != nil {
			slog.ErrorContext(ctx, "password reset: send mail to user failed", "err", err)
		}
	}()
}

type authOptionsResponse struct {
	PasswordResetEnabled bool `json:"password_reset_enabled"`
}

// handleAuthOptions, giriş sayfasının "Şifremi unuttum" akışının çalışıp çalışmayacağını öğrenmesini sağlar
// (SMTP ve PANEL_BASE_URL yapılandırılmış mı). Kimlik doğrulamasızdır ve başka bir şey söylemez.
func (d *Deps) handleAuthOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authOptionsResponse{PasswordResetEnabled: d.passwordResetEnabled()})
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

func (d *Deps) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if ok, retry := d.resetIPs.Allow(ip); !ok {
		writeTooManyRequests(w, retry)
		return
	}

	var req forgotPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !plausibleEmail(email) {
		writeError(w, http.StatusBadRequest, "geçerli bir e-posta adresi girin")
		return
	}

	// Bundan sonra yanıt her zaman 204'tür: hesabın varlığı, sınırlama ve yapılandırma durumu dışarıdan ayırt edilemez.
	respond := func() { w.WriteHeader(http.StatusNoContent) }

	if !d.passwordResetEnabled() {
		slog.WarnContext(r.Context(), "password reset requested but not available: set SMTP_HOST and PANEL_BASE_URL to enable it")
		respond()
		return
	}
	if ok, _ := d.resetEmails.Allow(email); !ok {
		respond()
		return
	}

	user, err := d.users.GetByEmail(r.Context(), email)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.ErrorContext(r.Context(), "password reset: lookup user", "err", err)
		}
		respond()
		return
	}

	token, err := newResetToken()
	if err != nil {
		slog.ErrorContext(r.Context(), "password reset: generate token", "err", err)
		respond()
		return
	}
	if err := d.resets.Issue(r.Context(), user.ID, hashResetToken(token), time.Now().Add(passwordResetTTL)); err != nil {
		slog.ErrorContext(r.Context(), "password reset: store token", "err", err)
		respond()
		return
	}

	targetID := user.ID.String()
	if err := d.audit.Write(r.Context(), &user.ID, user.Email, "auth.password_reset_requested", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}

	d.sendMailAsync(user.Email, resetMailSubject, resetMailBody(user.Email, fmt.Sprintf(resetLinkPathFmt, d.panelBaseURL, token), passwordResetTTL))
	respond()
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (d *Deps) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return
	}

	var req resetPasswordRequest
	if err := decodeJSON(r, &req); err != nil || req.Token == "" || req.NewPassword == "" || len(req.Token) > maxResetTokenLen {
		writeError(w, http.StatusBadRequest, "token ve new_password zorunlu")
		return
	}
	// Şifre politikası token'dan ÖNCE denetlenir: zayıf bir şifre denemesi bağlantıyı yakmaz.
	if err := model.ValidatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := authsvc.HashPassword(req.NewPassword)
	if err != nil {
		slog.ErrorContext(r.Context(), "password reset: hash", "err", err)
		writeError(w, http.StatusInternalServerError, "şifre değiştirilemedi")
		return
	}

	user, err := d.resets.Complete(r.Context(), hashResetToken(req.Token), hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.loginFailures.Allow(ip)
			writeErrorCode(w, http.StatusBadRequest, "sıfırlama bağlantısı geçersiz ya da süresi dolmuş; yeni bir bağlantı isteyin", errorCodeResetLinkInvalid)
			return
		}
		slog.ErrorContext(r.Context(), "password reset: complete", "err", err)
		writeError(w, http.StatusInternalServerError, "şifre değiştirilemedi")
		return
	}

	targetID := user.ID.String()
	if err := d.audit.Write(r.Context(), &user.ID, user.Email, "auth.password_reset", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}
	if d.mailer != nil && d.mailer.Enabled() {
		d.sendMailAsync(user.Email, resetDoneSubject, resetDoneMailBody(user.Email))
	}
	w.WriteHeader(http.StatusNoContent)
}

// newResetToken, 256 bitlik rastgele bir token üretir (URL'de taşınabilir biçimde).
func newResetToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// plausibleEmail yalnızca biçimi denetler ("x@y"): gerçek doğrulama e-postanın kullanıcıya ulaşmasıdır.
func plausibleEmail(s string) bool {
	if s == "" || len(s) > maxForgotEmailLen || strings.ContainsAny(s, " \t\r\n<>") {
		return false
	}
	at := strings.LastIndex(s, "@")
	return at > 0 && at < len(s)-1
}

func resetMailBody(email, link string, ttl time.Duration) string {
	return fmt.Sprintf(`Merhaba,

%s hesabı için bir şifre sıfırlama isteği aldık. Yeni şifre belirlemek için aşağıdaki bağlantıyı açın:

%s

Bağlantı %d dakika geçerlidir ve yalnızca bir kez kullanılabilir. Yeni bir bağlantı istenirse bu bağlantı geçersiz olur.

Bu isteği siz yapmadıysanız bu e-postayı yok sayabilirsiniz; şifreniz değişmez.

HealthBeat
`, email, link, int(ttl.Minutes()))
}

func resetDoneMailBody(email string) string {
	return fmt.Sprintf(`Merhaba,

%s hesabının şifresi az önce e-posta ile sıfırlanarak değiştirildi ve tüm açık oturumlar kapatıldı.

Bu değişikliği siz yapmadıysanız hemen sistem yöneticinize başvurun.

HealthBeat
`, email)
}
