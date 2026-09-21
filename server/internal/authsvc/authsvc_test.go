package authsvc

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func newSvc(accessTTL, refreshTTL time.Duration) *TokenService {
	return NewTokenService([]byte("access-secret"), []byte("refresh-secret"), accessTTL, refreshTTL)
}

func TestAccessTokenRoundTrip(t *testing.T) {
	svc := newSvc(time.Minute, time.Hour)
	uid := uuid.New()

	tok, exp, err := svc.IssueAccessToken(uid, "a@example.com", "org_admin", false)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry %v is not in the future", exp)
	}

	claims, err := svc.ParseAccessToken(tok)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if claims.UserID != uid || claims.Email != "a@example.com" || claims.Role != "org_admin" {
		t.Fatalf("claims = %+v", claims)
	}
}

// Bir access token, refresh token beklenen yerde asla kabul edilmemeli (tersi de) — farklı
// secret'lar KULLANIRLAR ve bir tür claim'i taşırlar.
func TestTokenTypesAreNotInterchangeable(t *testing.T) {
	svc := newSvc(time.Minute, time.Hour)
	access, _, _ := svc.IssueAccessToken(uuid.New(), "a@example.com", "operator", false)
	refresh, _, _ := svc.IssueRefreshToken(uuid.New(), "a@example.com", "operator", uuid.New())

	if _, err := svc.ParseRefreshToken(access); err == nil {
		t.Error("access token accepted as refresh token")
	}
	if _, err := svc.ParseAccessToken(refresh); err == nil {
		t.Error("refresh token accepted as access token")
	}
}

// İki tür için aynı secret: aralarında yalnızca typ claim'i durur.
func TestWrongTypeRejectedEvenWithSharedSecret(t *testing.T) {
	svc := NewTokenService([]byte("same"), []byte("same"), time.Minute, time.Hour)
	refresh, _, _ := svc.IssueRefreshToken(uuid.New(), "a@example.com", "operator", uuid.New())

	if _, err := svc.ParseAccessToken(refresh); err != ErrWrongTokenType {
		t.Fatalf("err = %v, want ErrWrongTokenType", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	svc := newSvc(-time.Minute, time.Hour)
	tok, _, _ := svc.IssueAccessToken(uuid.New(), "a@example.com", "operator", false)
	if _, err := svc.ParseAccessToken(tok); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestTamperedAndForeignTokensRejected(t *testing.T) {
	svc := newSvc(time.Minute, time.Hour)
	tok, _, _ := svc.IssueAccessToken(uuid.New(), "a@example.com", "operator", false)

	parts := strings.Split(tok, ".")
	tampered := parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))
	if _, err := svc.ParseAccessToken(tampered); err == nil {
		t.Error("token with forged signature accepted")
	}

	other := NewTokenService([]byte("other"), []byte("other"), time.Minute, time.Hour)
	foreign, _, _ := other.IssueAccessToken(uuid.New(), "a@example.com", "super_admin", false)
	if _, err := svc.ParseAccessToken(foreign); err == nil {
		t.Error("token signed with a different secret accepted")
	}

	for _, junk := range []string{"", "not-a-jwt", "a.b.c"} {
		if _, err := svc.ParseAccessToken(junk); err == nil {
			t.Errorf("junk token %q accepted", junk)
		}
	}
}

func TestAlgNoneRejected(t *testing.T) {
	svc := newSvc(time.Minute, time.Hour)
	claims := Claims{
		UserID: uuid.New(), Role: "super_admin", TokenType: tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ParseAccessToken(unsigned); err == nil {
		t.Fatal("alg=none token accepted")
	}
}

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse" {
		t.Fatal("password stored in plaintext")
	}
	if !VerifyPassword(hash, "correct horse") {
		t.Error("correct password rejected")
	}
	if VerifyPassword(hash, "wrong") || VerifyPassword(hash, "") {
		t.Error("wrong password accepted")
	}
	if VerifyPassword("not-a-bcrypt-hash", "correct horse") {
		t.Error("malformed hash accepted")
	}

	other, _ := HashPassword("correct horse")
	if other == hash {
		t.Error("hashes are not salted")
	}
}

func TestOpaqueSecret(t *testing.T) {
	a, err := GenerateOpaqueSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateOpaqueSecret()
	if a == b {
		t.Fatal("generated secrets collide")
	}
	if len(a) < 40 {
		t.Fatalf("secret %q looks too short for 32 random bytes", a)
	}

	hash := HashOpaqueSecret(a)
	if hash == a {
		t.Fatal("hash equals secret")
	}
	if !VerifyOpaqueSecret(hash, a) {
		t.Error("correct secret rejected")
	}
	if VerifyOpaqueSecret(hash, b) || VerifyOpaqueSecret(hash, "") || VerifyOpaqueSecret("", a) {
		t.Error("wrong secret / empty hash accepted")
	}
}

func TestMustChangePasswordClaim(t *testing.T) {
	svc := newSvc(time.Minute, time.Hour)
	uid := uuid.New()

	flagged, _, _ := svc.IssueAccessToken(uid, "a@example.com", "super_admin", true)
	claims, err := svc.ParseAccessToken(flagged)
	if err != nil || !claims.MustChangePassword {
		t.Fatalf("flagged token: claims=%+v err=%v", claims, err)
	}
	plain, _, _ := svc.IssueAccessToken(uid, "a@example.com", "super_admin", false)
	if claims, _ := svc.ParseAccessToken(plain); claims.MustChangePassword {
		t.Fatal("an unflagged token claims must-change")
	}
	// Refresh token'lar bunu asla taşımaz: refresh kullanıcıyı yeniden okur, bu yüzden bayrak her zaman veritabanından gelir.
	refresh, _, _ := svc.IssueRefreshToken(uid, "a@example.com", "super_admin", uuid.New())
	if claims, _ := svc.ParseRefreshToken(refresh); claims.MustChangePassword {
		t.Fatal("a refresh token carries the must-change flag")
	}
}
