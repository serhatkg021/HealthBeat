package authsvc

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken   = errors.New("geçersiz ya da süresi dolmuş token")
	ErrWrongTokenType = errors.New("token beklenen türde değil")
)

const (
	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"
)

// Claims bilerek yalnızca yetkilendirmenin ihtiyacı olanı taşır (kullanıcı kimliği + rol), tam
// kullanıcı kaydını değil — başka her şey için doğruluk kaynağı hâlâ veritabanıdır.
type Claims struct {
	UserID    uuid.UUID `json:"uid"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	TokenType string    `json:"typ"`
	// MustChangePassword, kullanıcının bayrağını token üretildiği andaki haliyle yansıtır: ayarlıyken
	// API sahibine yalnızca kendi profilini okuma ve şifresini değiştirme izni verir.
	MustChangePassword bool `json:"mcp,omitempty"`
	jwt.RegisteredClaims
}

// TokenService, panelin access/refresh JWT çiftini üretir ve doğrular. Access ve refresh
// token'lar ayrı secret'larla imzalanır ve bir TokenType claim'i taşır; böylece biri
// diğeri olarak asla yeniden kullanılamaz.
type TokenService struct {
	accessSecret  []byte
	refreshSecret []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
}

func NewTokenService(accessSecret, refreshSecret []byte, accessTTL, refreshTTL time.Duration) *TokenService {
	return &TokenService{
		accessSecret:  accessSecret,
		refreshSecret: refreshSecret,
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
	}
}

func (s *TokenService) IssueAccessToken(userID uuid.UUID, email, role string, mustChangePassword bool) (string, time.Time, error) {
	return s.issue(userID, email, role, tokenTypeAccess, s.accessSecret, s.accessTTL, "", mustChangePassword)
}

// IssueRefreshToken, server bu belirli token'ı izleyebilsin (ve iptal edebilsin) diye jti'yi
// JWT ID olarak gömer — bkz. store.RefreshTokens.
func (s *TokenService) IssueRefreshToken(userID uuid.UUID, email, role string, jti uuid.UUID) (string, time.Time, error) {
	return s.issue(userID, email, role, tokenTypeRefresh, s.refreshSecret, s.refreshTTL, jti.String(), false)
}

func (s *TokenService) issue(userID uuid.UUID, email, role, typ string, secret []byte, ttl time.Duration, id string, mustChangePassword bool) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(ttl)
	claims := Claims{
		UserID:    userID,
		Email:     email,
		Role:      role,
		TokenType: typ,

		MustChangePassword: mustChangePassword,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        id,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	return signed, exp, err
}

func (s *TokenService) ParseAccessToken(tokenStr string) (*Claims, error) {
	return s.parse(tokenStr, s.accessSecret, tokenTypeAccess)
}

func (s *TokenService) ParseRefreshToken(tokenStr string) (*Claims, error) {
	return s.parse(tokenStr, s.refreshSecret, tokenTypeRefresh)
}

func (s *TokenService) parse(tokenStr string, secret []byte, wantType string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.TokenType != wantType {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}
