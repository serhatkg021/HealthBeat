package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenPairResponse struct {
	AccessToken           string      `json:"access_token"`
	AccessTokenExpiresAt  time.Time   `json:"access_token_expires_at"`
	RefreshToken          string      `json:"refresh_token,omitempty"`
	RefreshTokenExpiresAt *time.Time  `json:"refresh_token_expires_at,omitempty"`
	User                  *model.User `json:"user,omitempty"`
}

func (d *Deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return
	}

	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "e-posta ve şifre zorunlu")
		return
	}

	user, err := d.users.GetByEmail(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			authsvc.BurnPasswordCheck(req.Password) // yanlış şifreyle aynı maliyet
			d.loginFailures.Allow(ip)
			writeError(w, http.StatusUnauthorized, "e-posta veya şifre hatalı")
			return
		}
		slog.ErrorContext(r.Context(), "login: lookup user", "err", err)
		writeError(w, http.StatusInternalServerError, "giriş yapılamadı")
		return
	}

	if !authsvc.VerifyPassword(user.PasswordHash, req.Password) {
		d.loginFailures.Allow(ip)
		writeError(w, http.StatusUnauthorized, "e-posta veya şifre hatalı")
		return
	}

	pair, err := d.issueTokenPair(r.Context(), user, uuid.New()) // yeni giriş = yeni token ailesi
	if err != nil {
		slog.ErrorContext(r.Context(), "login: issue tokens", "err", err)
		writeError(w, http.StatusInternalServerError, "giriş yapılamadı")
		return
	}

	if err := d.users.TouchLastLogin(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "login: touch last_login_at", "err", err)
	}

	targetID := user.ID.String()
	if err := d.audit.Write(r.Context(), &user.ID, user.Email, "auth.login", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}

	user.PasswordHash = ""
	pair.User = &user
	writeJSON(w, http.StatusOK, pair)
}

// issueTokenPair, bir access token ile yeni bir refresh token üretir ve ikincisini (jti ile)
// family içine kaydeder; böylece sonradan döndürülebilir ya da iptal edilebilir.
func (d *Deps) issueTokenPair(ctx context.Context, user model.User, family uuid.UUID) (tokenPairResponse, error) {
	accessToken, accessExp, err := d.tokenSvc.IssueAccessToken(user.ID, user.Email, user.Role, user.MustChangePassword)
	if err != nil {
		return tokenPairResponse{}, err
	}
	jti := uuid.New()
	refreshToken, refreshExp, err := d.tokenSvc.IssueRefreshToken(user.ID, user.Email, user.Role, jti)
	if err != nil {
		return tokenPairResponse{}, err
	}
	if err := d.refreshTokens.Create(ctx, jti, user.ID, family, refreshExp); err != nil {
		return tokenPairResponse{}, err
	}
	return tokenPairResponse{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExp,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: &refreshExp,
	}, nil
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshTokenGrace, az önce döndürülmüş bir refresh token'ın birkaç saniye içinde yeniden
// sunulmasına (iki sekmenin aynı anda yenilemesi) hırsızlık saymak yerine tolerans gösterir.
// Pencere geçince yeniden kullanım tüm token ailesini iptal eder.
const refreshTokenGrace = 10 * time.Second

// handleRefresh, bir refresh token'ı yeni bir access token VE yeni bir refresh token ile
// değiştirir (rotasyon): sunulan token kullanılmış işaretlenir; böylece çalınmış bir kopya
// meşru istemci yenilediği anda çalışmayı bırakır ve eski birinin tekrar oynatılması
// algılanıp oturum ailesini öldürür.
func (d *Deps) handleRefresh(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return
	}

	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil || req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token zorunlu")
		return
	}

	deny := func() {
		d.loginFailures.Allow(ip)
		writeError(w, http.StatusUnauthorized, "geçersiz ya da süresi dolmuş refresh token")
	}

	claims, err := d.tokenSvc.ParseRefreshToken(req.RefreshToken)
	if err != nil {
		deny()
		return
	}
	jti, err := uuid.Parse(claims.ID)
	if err != nil { // jti yok: token izleme başlamadan önce üretilmiş
		deny()
		return
	}

	consumed, err := d.refreshTokens.Consume(r.Context(), jti, refreshTokenGrace)
	switch {
	case errors.Is(err, store.ErrTokenReuse):
		slog.WarnContext(r.Context(), "SECURITY: refresh token reuse detected; token family revoked", "token_user_id", claims.UserID.String())
		targetID := claims.UserID.String()
		if err := d.audit.Write(r.Context(), &claims.UserID, claims.Email, "auth.token_reuse_detected", "user", &targetID, map[string]any{"ip": ip}, remoteIP(r)); err != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
		}
		deny()
		return
	case errors.Is(err, store.ErrNotFound):
		deny()
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "refresh: consume token", "err", err)
		writeError(w, http.StatusInternalServerError, "token yenilenemedi")
		return
	}

	// Token'ın gömülü rolüne güvenmek yerine kullanıcıyı yeniden getir: token üretildikten sonra
	// değişmiş olabilir (ya da kullanıcı silinmiş olabilir).
	user, err := d.users.GetByID(r.Context(), consumed.UserID)
	if err != nil {
		deny()
		return
	}

	pair, err := d.issueTokenPair(r.Context(), user, consumed.FamilyID)
	if err != nil {
		slog.ErrorContext(r.Context(), "refresh: issue tokens", "err", err)
		writeError(w, http.StatusInternalServerError, "token yenilenemedi")
		return
	}
	writeJSON(w, http.StatusOK, pair)
}

// handleLogout, verilen refresh token'ın ait olduğu oturumu (token ailesini) iptal eder.
// İdempotenttir ve access token gerektirmez; böylece access token'ı zaten süresi dolmuş
// bir istemci yine de çıkış yapabilir.
func (d *Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return
	}

	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil || req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token zorunlu")
		return
	}

	claims, err := d.tokenSvc.ParseRefreshToken(req.RefreshToken)
	if err != nil {
		d.loginFailures.Allow(ip)
		writeError(w, http.StatusUnauthorized, "geçersiz ya da süresi dolmuş refresh token")
		return
	}
	if jti, err := uuid.Parse(claims.ID); err == nil {
		if err := d.refreshTokens.RevokeFamilyOf(r.Context(), jti); err != nil {
			slog.ErrorContext(r.Context(), "logout: revoke token family", "err", err)
			writeError(w, http.StatusInternalServerError, "çıkış yapılamadı")
			return
		}
	}

	targetID := claims.UserID.String()
	if err := d.audit.Write(r.Context(), &claims.UserID, claims.Email, "auth.logout", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangeOwnPassword, oturum açmış her kullanıcının yeni bir şifre seçmesini sağlar.
// must_change_password'dan çıkışın tek yoludur ve mevcut tüm oturumları sonlandırır (şifre
// değişikliği genellikle "başkası bilebilir"e bir yanıttır) — çağıran yeni bir token çifti
// alır; böylece panel devam edebilir.
func (d *Deps) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	// Çalınmış bir access token ile yapılan yanlış "mevcut şifre" denemeleri giriş denemeleri
	// kadar değerlidir, bu yüzden aynı başarısızlık bütçesinden düşer.
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return
	}

	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "current_password ve new_password zorunlu")
		return
	}

	userID, _ := userIDFromContext(r.Context())
	user, err := d.users.GetByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "geçersiz ya da süresi dolmuş token")
		return
	}

	// 401 değil 403: panel 401'i "oturum süresi doldu" sayar ve mesajı göstermek yerine
	// kullanıcının oturumunu kapatırdı.
	if !authsvc.VerifyPassword(user.PasswordHash, req.CurrentPassword) {
		d.loginFailures.Allow(ip)
		writeError(w, http.StatusForbidden, "mevcut şifre hatalı")
		return
	}
	if err := model.ValidatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NewPassword == req.CurrentPassword {
		writeError(w, http.StatusBadRequest, "yeni şifre mevcut şifreden farklı olmalı")
		return
	}

	hash, err := authsvc.HashPassword(req.NewPassword)
	if err != nil {
		slog.ErrorContext(r.Context(), "change password: hash", "err", err)
		writeError(w, http.StatusInternalServerError, "şifre değiştirilemedi")
		return
	}
	if err := d.users.SetOwnPassword(r.Context(), userID, hash); err != nil {
		slog.ErrorContext(r.Context(), "change password: store", "err", err)
		writeError(w, http.StatusInternalServerError, "şifre değiştirilemedi")
		return
	}
	if err := d.refreshTokens.RevokeAllForUser(r.Context(), userID); err != nil {
		slog.ErrorContext(r.Context(), "change password: revoke sessions", "err", err)
		writeError(w, http.StatusInternalServerError, "şifre değişti ancak mevcut oturumlar sonlandırılamadı")
		return
	}

	targetID := userID.String()
	if err := d.audit.Write(r.Context(), &userID, user.Email, "auth.password_changed", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}

	user.MustChangePassword = false
	pair, err := d.issueTokenPair(r.Context(), user, uuid.New())
	if err != nil {
		slog.ErrorContext(r.Context(), "change password: issue tokens", "err", err)
		writeError(w, http.StatusInternalServerError, "şifre değişti; lütfen yeniden giriş yapın")
		return
	}
	user.PasswordHash = ""
	pair.User = &user
	writeJSON(w, http.StatusOK, pair)
}
