package authsvc

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// GenerateOpaqueSecret, yüksek entropili rastgele bir token üretir; hem push modu host
// api_token'ı hem pull modu paylaşılan secret'ı için kullanılır (bkz. docs/MIMARI.md
// bölüm 3 ve 5). Kullanıcı şifrelerinden farklı olarak bunlar server tarafında tam entropiyle
// üretilir; bu yüzden doğrulamada bcrypt değil hızlı bir hash uygundur.
func GenerateOpaqueSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashOpaqueSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func VerifyOpaqueSecret(hash, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashOpaqueSecret(secret))) == 1
}
