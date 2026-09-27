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

func (req *loginRequest) Validate() error {
	if req.Email == "" || req.Password == "" {
		return errors.New("e-posta ve şifre zorunlu")
	}
	return nil
}

func (d *Deps) handleLogin(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("giriş yapılamadı")
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return nil
	}

	req, err := bind[loginRequest](r)
	if err != nil {
		return err
	}
	// Bilinmeyen e-posta ve yanlış şifre aynı yanıtı alır ve kaynak IP'nin başarısızlık bütçesinden düşer.
	deny := func() error {
		d.loginFailures.Allow(ip)
		return newError(http.StatusUnauthorized, "e-posta veya şifre hatalı")
	}

	user, hash, err := d.users.GetByEmailWithHash(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	if errors.Is(err, store.ErrNotFound) {
		authsvc.BurnPasswordCheck(req.Password) // yanlış şifreyle aynı maliyet
		return deny()
	}
	if err != nil {
		return fail("login: lookup user", err)
	}

	if !authsvc.VerifyPassword(hash, req.Password) {
		return deny()
	}

	pair, err := d.issueTokenPair(r.Context(), user, uuid.New()) // yeni giriş = yeni token ailesi
	if err != nil {
		return fail("login: issue tokens", err)
	}

	if err := d.users.TouchLastLogin(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "login: touch last_login_at", "err", err)
	}

	targetID := user.ID.String()
	if err := d.audit.Write(r.Context(), &user.ID, user.Email, "auth.login", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}

	pair.User = &user
	writeJSON(w, http.StatusOK, pair)
	return nil
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

func (req *refreshRequest) Validate() error {
	if req.RefreshToken == "" {
		return errMissingRefreshToken
	}
	return nil
}

// bindRefreshRequest, çözülemeyen gövdeye de eksik token'la aynı yanıtı verir.
func bindRefreshRequest(r *http.Request) (refreshRequest, error) {
	req, err := bind[refreshRequest](r)
	if err != nil {
		return req, errMissingRefreshToken
	}
	return req, nil
}

var errMissingRefreshToken = errors.New("refresh_token zorunlu")

// refreshTokenGrace, az önce döndürülmüş bir refresh token'ın birkaç saniye içinde yeniden
// sunulmasına (iki sekmenin aynı anda yenilemesi) hırsızlık saymak yerine tolerans gösterir.
// Pencere geçince yeniden kullanım tüm token ailesini iptal eder.
const refreshTokenGrace = 10 * time.Second

// handleRefresh, bir refresh token'ı yeni bir access token VE yeni bir refresh token ile
// değiştirir (rotasyon): sunulan token kullanılmış işaretlenir; böylece çalınmış bir kopya
// meşru istemci yenilediği anda çalışmayı bırakır ve eski birinin tekrar oynatılması
// algılanıp oturum ailesini öldürür.
func (d *Deps) handleRefresh(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("token yenilenemedi")
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return nil
	}

	req, err := bindRefreshRequest(r)
	if err != nil {
		return badRequest(err.Error())
	}

	deny := func() error {
		d.loginFailures.Allow(ip)
		return newError(http.StatusUnauthorized, "geçersiz ya da süresi dolmuş refresh token")
	}

	claims, err := d.tokenSvc.ParseRefreshToken(req.RefreshToken)
	if err != nil {
		return deny()
	}
	jti, err := uuid.Parse(claims.ID)
	if err != nil { // jti yok: token izleme başlamadan önce üretilmiş
		return deny()
	}

	consumed, err := d.refreshTokens.Consume(r.Context(), jti, refreshTokenGrace)
	switch {
	case errors.Is(err, store.ErrTokenReuse):
		slog.WarnContext(r.Context(), "SECURITY: refresh token reuse detected; token family revoked", "token_user_id", claims.UserID.String())
		targetID := claims.UserID.String()
		if err := d.audit.Write(r.Context(), &claims.UserID, claims.Email, "auth.token_reuse_detected", "user", &targetID, map[string]any{"ip": ip}, remoteIP(r)); err != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
		}
		return deny()
	case errors.Is(err, store.ErrNotFound):
		return deny()
	case err != nil:
		return fail("refresh: consume token", err)
	}

	// Token'ın gömülü rolüne güvenmek yerine kullanıcıyı yeniden getir: token üretildikten sonra
	// değişmiş olabilir (ya da kullanıcı silinmiş olabilir).
	user, err := d.users.GetByID(r.Context(), consumed.UserID)
	if err != nil {
		return deny()
	}

	pair, err := d.issueTokenPair(r.Context(), user, consumed.FamilyID)
	if err != nil {
		return fail("refresh: issue tokens", err)
	}
	writeJSON(w, http.StatusOK, pair)
	return nil
}

// handleLogout, verilen refresh token'ın ait olduğu oturumu (token ailesini) iptal eder.
// İdempotenttir ve access token gerektirmez; böylece access token'ı zaten süresi dolmuş
// bir istemci yine de çıkış yapabilir.
func (d *Deps) handleLogout(w http.ResponseWriter, r *http.Request) error {
	ip := remoteIP(r)
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return nil
	}

	req, err := bindRefreshRequest(r)
	if err != nil {
		return badRequest(err.Error())
	}

	claims, err := d.tokenSvc.ParseRefreshToken(req.RefreshToken)
	if err != nil {
		d.loginFailures.Allow(ip)
		return newError(http.StatusUnauthorized, "geçersiz ya da süresi dolmuş refresh token")
	}
	if jti, err := uuid.Parse(claims.ID); err == nil {
		if err := d.refreshTokens.RevokeFamilyOf(r.Context(), jti); err != nil {
			return serverErr("çıkış yapılamadı", "logout: revoke token family", err)
		}
	}

	targetID := claims.UserID.String()
	if err := d.audit.Write(r.Context(), &claims.UserID, claims.Email, "auth.logout", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// Validate yalnızca varlığı denetler; yeni şifrenin kuralları mevcut şifre doğrulandıktan sonra denetlenir (yanlış
// mevcut şifre her zaman 403 alır).
func (req *changePasswordRequest) Validate() error {
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return errMissingPasswords
	}
	return nil
}

var errMissingPasswords = errors.New("current_password ve new_password zorunlu")

// handleChangeOwnPassword, oturum açmış her kullanıcının yeni bir şifre seçmesini sağlar.
// must_change_password'dan çıkışın tek yoludur ve mevcut tüm oturumları sonlandırır (şifre
// değişikliği genellikle "başkası bilebilir"e bir yanıttır) — çağıran yeni bir token çifti
// alır; böylece panel devam edebilir.
func (d *Deps) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("şifre değiştirilemedi")
	ip := remoteIP(r)
	// Çalınmış bir access token ile yapılan yanlış "mevcut şifre" denemeleri giriş denemeleri
	// kadar değerlidir, bu yüzden aynı başarısızlık bütçesinden düşer.
	if rejectIfThrottled(w, d.loginFailures, ip) {
		return nil
	}

	req, err := bind[changePasswordRequest](r)
	if err != nil {
		return badRequest(errMissingPasswords.Error()) // çözülemeyen gövde de aynı yanıtı alır
	}

	userID, _ := userIDFromContext(r.Context())
	user, currentHash, err := d.users.GetByIDWithHash(r.Context(), userID)
	if err != nil {
		return newError(http.StatusUnauthorized, "geçersiz ya da süresi dolmuş token")
	}

	// 401 değil 403: panel 401'i "oturum süresi doldu" sayar ve mesajı göstermek yerine
	// kullanıcının oturumunu kapatırdı.
	if !authsvc.VerifyPassword(currentHash, req.CurrentPassword) {
		d.loginFailures.Allow(ip)
		return newError(http.StatusForbidden, "mevcut şifre hatalı")
	}
	if err := model.ValidatePassword(req.NewPassword); err != nil {
		return badRequest(err.Error())
	}
	if req.NewPassword == req.CurrentPassword {
		return badRequest("yeni şifre mevcut şifreden farklı olmalı")
	}

	hash, err := authsvc.HashPassword(req.NewPassword)
	if err != nil {
		return fail("change password: hash", err)
	}
	if err := d.users.SetOwnPassword(r.Context(), userID, hash); err != nil {
		return fail("change password: store", err)
	}
	if err := d.refreshTokens.RevokeAllForUser(r.Context(), userID); err != nil {
		return serverErr("şifre değişti ancak mevcut oturumlar sonlandırılamadı", "change password: revoke sessions", err)
	}

	targetID := userID.String()
	if err := d.audit.Write(r.Context(), &userID, user.Email, "auth.password_changed", "user", &targetID, nil, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}

	user.MustChangePassword = false
	pair, err := d.issueTokenPair(r.Context(), user, uuid.New())
	if err != nil {
		return serverErr("şifre değişti; lütfen yeniden giriş yapın", "change password: issue tokens", err)
	}
	pair.User = &user
	writeJSON(w, http.StatusOK, pair)
	return nil
}
