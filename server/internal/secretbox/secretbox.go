// Package secretbox küçük secret'ları (şu an hosts.pull_secret) uygulama katmanında
// AES-256-GCM ile şifreler; böylece anahtar veritabanına asla ulaşmaz ve tek başına bir DB
// dökümü onları ifşa etmez.
//
// Mühürlenmiş bir değer "enc:v1:<base64url(nonce || ciphertext)>" gibi görünür. Çağıran,
// doğrulanan ama saklanmayan ilişkili veri (sahip satırın kimliği) verir; böylece başka bir
// satıra kopyalanmış mühürlü değer açılamaz.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	prefix  = "enc:v1:"
	keySize = 32
)

var ErrNotSealed = errors.New("value is not in sealed format")

type Box struct {
	aead cipher.AEAD
}

// New, ham 32 baytlık bir anahtardan Box kurar.
func New(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secretbox: key must be %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// ParseKey, base64 (`openssl rand -base64 32`) ya da 64 hex karakter (`openssl rand -hex 32`)
// olarak verilen bir anahtarı çözer.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == keySize {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == keySize {
		return b, nil
	}
	return nil, fmt.Errorf("secretbox: key must be %d random bytes encoded as base64 or hex (try: openssl rand -base64 32)", keySize)
}

// IsSealed, s'nin mühürlü değer önekine sahip olup olmadığını bildirir (gerçekten açılıp
// açılamayacağını doğrulamaz).
func IsSealed(s string) bool { return strings.HasPrefix(s, prefix) }

// Seal, plaintext'i şifreler ve aad'e bağlar.
func (b *Box) Seal(plaintext, aad string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad)) // nonce || ciphertext||tag
	return prefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open, aynı aad ile Seal'in ürettiği bir değeri çözer. Yanlış anahtar, yanlış aad, kurcalama
// ya da hiç mühürlenmemiş bir değer için başarısız olur.
func (b *Box) Open(sealed, aad string) (string, error) {
	enc, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return "", ErrNotSealed
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(raw) < b.aead.NonceSize()+b.aead.Overhead() {
		return "", errors.New("secretbox: malformed sealed value")
	}
	nonce, ct := raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():]
	pt, err := b.aead.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return "", errors.New("secretbox: cannot decrypt (wrong key, wrong row, or corrupted data)")
	}
	return string(pt), nil
}
