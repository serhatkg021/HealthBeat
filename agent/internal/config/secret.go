package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Sırların (push api_token, pull secret) agent.json'daki şifreli biçimi: "enc:v1:" + base64(nonce || AES-256-GCM
// şifreli metni). Anahtar makinenin kimliğinden (/etc/machine-id) türetilir; ayrı bir anahtar dosyası yoktur. Amaç,
// dosyanın tek başına kopyalandığı yerlerde (yedek, yapılandırma deposu, destek paketi) sırrın okunamamasıdır: başka bir
// makinede ya da klonlanmış bir VM'de çözülmez. Makinenin root'u sırrı her zaman çözebilir; agent onu gözetimsiz
// kullanmak zorundadır. Düz yazılmış sır da kabul edilir (bkz. Config.PlaintextSecret).
const encPrefix = "enc:v1:"

// hkdfInfo, türetilen anahtarı bu kullanıma bağlar; makine kimliği başka bir amaçla da anahtar türetilirse çakışmaz.
const hkdfInfo = "healthbeat-agent config secret v1"

// machineIDPaths, makine kimliğinin arandığı dosyalardır; sıra önceliktir (ikincisi systemd'siz eski dağıtımlar için).
var machineIDPaths = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// RepairHint, şifreli sır okunamadığında gösterilen komuttur (yeni değer gerekir); EncryptHint, sır düz durduğunda
// (değer aynı kalır, yerinde şifrelenir).
const (
	RepairHint  = "sudo healthbeat-agent-setup token"
	EncryptHint = "sudo healthbeat-agent-setup encrypt"
)

func machineKey() ([]byte, error) {
	for _, path := range machineIDPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(data)); id != "" {
			return hkdf.Key(sha256.New, []byte(id), nil, hkdfInfo, 32)
		}
	}
	return nil, fmt.Errorf("makine kimliği bulunamadı (%s); bu makinede sır şifrelenemez ya da çözülemez", strings.Join(machineIDPaths, ", "))
}

func machineAEAD() (cipher.AEAD, error) {
	key, err := machineKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptSecret, sırrı bu makineye bağlı şifreli biçime çevirir (agent -encrypt-token; install.sh kullanır).
func EncryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("sır boş")
	}
	if strings.HasPrefix(plain, encPrefix) {
		return "", errors.New("sır zaten şifreli")
	}
	aead, err := machineAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return encPrefix + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// IsEncrypted, değerin şifreli biçimde olup olmadığını söyler.
func IsEncrypted(value string) bool { return strings.HasPrefix(value, encPrefix) }

// decryptSecret, şifreli değeri çözer. Hata metni hangi alanın okunamadığını ve ne yapılacağını söyler.
func decryptSecret(field, value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, encPrefix))
	if err != nil {
		return "", fmt.Errorf("%s: şifreli değer bozuk; yeniden vermek için: %s", field, RepairHint)
	}
	aead, err := machineAEAD()
	if err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", fmt.Errorf("%s: şifreli değer bozuk; yeniden vermek için: %s", field, RepairHint)
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil || len(plain) == 0 {
		return "", fmt.Errorf("%s bu makinede çözülemedi (config kopyalanmış ya da makine klonlanmış olabilir); yeniden vermek için: %s", field, RepairHint)
	}
	return string(plain), nil
}

// openSecret, alanı şifreliyse yerinde çözer; düzse adını PlaintextSecret'a yazar.
func (c *Config) openSecret(field string, value *string) error {
	switch {
	case *value == "":
		return nil
	case IsEncrypted(*value):
		plain, err := decryptSecret(field, *value)
		if err != nil {
			return err
		}
		*value = plain
	default:
		c.PlaintextSecret = field
	}
	return nil
}
