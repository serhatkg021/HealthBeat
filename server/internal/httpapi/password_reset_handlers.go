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
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/outbox"
	"healthbeat-server/internal/store"
)

// E-posta ile şifre sıfırlama. Akış: kullanıcı e-postasını girer → server tek kullanımlık, kısa ömürlü bir bağlantı
// e-postalar → bağlantıdaki token ile yeni şifre seçilir.
//
// Güvenlik kararları:
//   - "Bu e-posta kayıtlı mı?" sorusu yanıttan öğrenilemez: geçerli biçimdeki her e-posta için aynı 204 döner.
//   - Ham token yalnızca e-postada bulunur; veritabanında yalnızca SHA-256 özeti saklanır. E-posta gönderilene kadar
//     bildirim kuyruğunda şifreli (secretbox) durur ve gönderilince (ya da süresi dolunca) şifreli hâli de silinir.
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
	d.mailWorker = nil
	if m != nil {
		d.mailWorker = outbox.NewWorker(d.outbox, accountMailKinds, mailerChannel{m})
	}
}

// accountMailKinds, httpapi'nin bildirim kuyruğuna yazdığı ve kendi işçisiyle teslim ettiği türlerdir (alert
// bildirimlerini alert motoru teslim eder).
var accountMailKinds = []string{store.OutboxKindPasswordReset, store.OutboxKindPasswordChanged}

// mailerChannel, Mailer'ı e-posta kanalı olarak sunar.
type mailerChannel struct{ m Mailer }

func (mailerChannel) Channel() string { return model.ChannelEmail }

func (c mailerChannel) Send(ctx context.Context, to []string, msg notify.Message) error {
	return c.m.Send(ctx, to, msg.Subject, msg.Body)
}

func (d *Deps) passwordResetEnabled() bool {
	return d.mailer != nil && d.mailer.Enabled() && d.panelBaseURL != ""
}

// RunMailOutbox, ctx bitene kadar şifre e-postalarını kuyruktan teslim eder. Kendi goroutine'inde çalıştırın.
func (d *Deps) RunMailOutbox(ctx context.Context) {
	if d.mailWorker != nil {
		d.mailWorker.Run(ctx)
	}
}

// WaitForMail, teslim zamanı gelmiş şifre e-postalarını göndermeyi dener ve sürmekte olan bir teslim turunu bekler
// (testler ve kapanış için). Gönderilemeyenler kuyrukta kalır.
func (d *Deps) WaitForMail(ctx context.Context) error {
	if d.mailWorker == nil {
		return nil
	}
	_, err := d.mailWorker.DeliverDue(ctx)
	return err
}

// queueMail, e-postayı kuyruğa yazar ve işçiyi uyandırır; gönderim isteğin dışında olur, böylece yanıt süresi
// SMTP'ye bağlı olmaz. Hata yalnızca loglanır (kullanıcıya bildirilemez: yanıt her durumda aynıdır).
func (d *Deps) queueMail(ctx context.Context, m store.OutboxMessage) {
	m.Channel, m.RequestID = model.ChannelEmail, logging.RequestID(ctx)
	if _, err := d.outbox.Enqueue(ctx, m); err != nil {
		slog.ErrorContext(ctx, "password mail: enqueue failed", "kind", m.Kind, "err", err)
		return
	}
	if d.mailWorker != nil {
		d.mailWorker.Wake()
	}
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

// Validate, e-postayı küçük harfe çevirip kırpar (req.Email).
func (req *forgotPasswordRequest) Validate() error {
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !plausibleEmail(req.Email) {
		return errors.New("geçerli bir e-posta adresi girin")
	}
	return nil
}

func (d *Deps) handleForgotPassword(w http.ResponseWriter, r *http.Request) error {
	ip := remoteIP(r)
	if ok, retry := d.resetIPs.Allow(ip); !ok {
		writeTooManyRequests(w, retry)
		return nil
	}

	req, err := bind[forgotPasswordRequest](r)
	if err != nil {
		return err
	}
	email := req.Email

	// Bundan sonra yanıt her zaman 204'tür: hesabın varlığı, sınırlama ve yapılandırma durumu dışarıdan ayırt edilemez.
	respond := func() error {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}

	if !d.passwordResetEnabled() {
		slog.WarnContext(r.Context(), "password reset requested but not available: set SMTP_HOST and PANEL_BASE_URL to enable it")
		return respond()
	}
	if ok, _ := d.resetEmails.Allow(email); !ok {
		return respond()
	}

	user, err := d.users.GetByEmail(r.Context(), email)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.ErrorContext(r.Context(), "password reset: lookup user", "err", err)
		}
		return respond()
	}

	token, err := newResetToken()
	if err != nil {
		slog.ErrorContext(r.Context(), "password reset: generate token", "err", err)
		return respond()
	}
	expiresAt := time.Now().Add(passwordResetTTL)
	if err := d.resets.Issue(r.Context(), user.ID, hashResetToken(token), expiresAt); err != nil {
		slog.ErrorContext(r.Context(), "password reset: store token", "err", err)
		return respond()
	}

	targetID := user.ID.String()
	if err := d.audit.Write(r.Context(), &user.ID, user.Email, "auth.password_reset_requested", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}

	// Önceki bağlantıyı taşıyan, henüz gitmemiş e-posta artık gönderilmez (o token Issue ile geçersizleşti). Yeni
	// bağlantı şifreli saklanır ve token'la aynı anda geçersiz olur: süresi dolmuş bağlantıyı göndermek anlamsız.
	if _, err := d.outbox.Supersede(r.Context(), store.OutboxKindPasswordReset, user.Email); err != nil {
		slog.ErrorContext(r.Context(), "password reset: supersede pending mail", "err", err)
	}
	d.queueMail(r.Context(), store.OutboxMessage{
		Kind: store.OutboxKindPasswordReset, Recipients: []string{user.Email}, Subject: resetMailSubject,
		Body: resetMailBody(user.Email, fmt.Sprintf(resetLinkPathFmt, d.panelBaseURL, token), passwordResetTTL),
		Seal: true, ExpiresAt: &expiresAt,
	})
	return respond()
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (req *resetPasswordRequest) Validate() error {
	if req.Token == "" || req.NewPassword == "" || len(req.Token) > maxResetTokenLen {
		return errMissingResetFields
	}
	// Şifre politikası token'dan ÖNCE denetlenir: zayıf bir şifre denemesi bağlantıyı yakmaz.
	return model.ValidatePassword(req.NewPassword)
}

var errMissingResetFields = errors.New("token ve new_password zorunlu")

func (d *Deps) handleResetPassword(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("şifre değiştirilemedi")
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return nil
	}

	req, err := bind[resetPasswordRequest](r)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.Message == "geçersiz istek gövdesi" {
		return badRequest(errMissingResetFields.Error()) // çözülemeyen gövde de eksik alanla aynı yanıtı alır
	}
	if err != nil {
		return err
	}
	hash, err := authsvc.HashPassword(req.NewPassword)
	if err != nil {
		return fail("password reset: hash", err)
	}

	user, err := d.resets.Complete(r.Context(), hashResetToken(req.Token), hash)
	if errors.Is(err, store.ErrNotFound) {
		d.loginFailures.Allow(ip)
		return &apiError{
			Status:  http.StatusBadRequest,
			Message: "sıfırlama bağlantısı geçersiz ya da süresi dolmuş; yeni bir bağlantı isteyin",
			Code:    errorCodeResetLinkInvalid,
		}
	}
	if err != nil {
		return fail("password reset: complete", err)
	}

	targetID := user.ID.String()
	if err := d.audit.Write(r.Context(), &user.ID, user.Email, "auth.password_reset", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}
	// Kullanılan bağlantının (varsa hâlâ bekleyen) e-postası artık gönderilmez.
	if _, err := d.outbox.Supersede(r.Context(), store.OutboxKindPasswordReset, user.Email); err != nil {
		slog.ErrorContext(r.Context(), "password reset: supersede pending mail", "err", err)
	}
	if d.mailer != nil && d.mailer.Enabled() {
		d.queueMail(r.Context(), store.OutboxMessage{
			Kind: store.OutboxKindPasswordChanged, Recipients: []string{user.Email}, Subject: resetDoneSubject,
			Body: resetDoneMailBody(user.Email),
		})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
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
