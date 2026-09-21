package authsvc

import (
	"sync"

	"golang.org/x/crypto/bcrypt"
)

func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// dummyHash, gerçeklerle aynı maliyetle üretilmiş atılabilir bir hash'tir.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("timing-equaliser-not-a-real-password")
	if err != nil {
		return ""
	}
	return h
})

// BurnPasswordCheck, gerçek bir hesaba karşı VerifyPassword ile aynı miktarda iş yapar. Giriş
// e-postası bilinmiyorsa çağırın: aksi halde "böyle kullanıcı yok" mikrosaniyede döner,
// "yanlış şifre" ise tam bir bcrypt turu (~50 ms) sürer ve yanıt süresi hangi e-postaların
// hesabı olduğunu ele verir.
func BurnPasswordCheck(plain string) {
	VerifyPassword(dummyHash(), plain)
}
