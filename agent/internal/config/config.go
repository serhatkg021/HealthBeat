// Package config, agent'ın JSON yapılandırma dosyasını yükler (server adresi,
// kimlik bilgileri, push aralığı — bkz. docs/MIMARI.md bölüm 6, "Agent kurulumu"). Aynı
// dosya biçimi hem push hem pull modunu kapsar; Mode hangi alan kümesinin zorunlu olarak
// doğrulanacağını seçer.
package config

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	ModePush = "push"
	ModePull = "pull"
)

type Config struct {
	Mode string `json:"mode"`

	// Push modu.
	ServerURL          string `json:"server_url,omitempty"`
	HostID             string `json:"host_id,omitempty"`
	APIToken           string `json:"api_token,omitempty"`
	IntervalSeconds    int    `json:"interval_seconds,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	// CACertFile, özel CA kullanan server'lar için server sertifikasını imzalayan sertifika
	// otoritelerini (ya da otoritesini) içeren bir PEM dosyasıdır. Ayarlıysa yalnızca bu otoriteler
	// güvenilir sayılır (curl --cacert gibi), sistem kökleri değil.
	CACertFile string `json:"ca_cert_file,omitempty"`

	// Pull modu: agent kendi yerel HTTPS API'sini çalıştırır ve server oraya çağrı yapar
	// (bkz. docs/MIMARI.md bölüm 5 — TLS + IP whitelist + paylaşılan secret burada
	// isteğe bağlı sertleştirme değil, zorunludur).
	ListenAddr       string   `json:"listen_addr,omitempty"`
	PullEndpoint     string   `json:"pull_endpoint,omitempty"`
	PullSecret       string   `json:"pull_secret,omitempty"`
	AllowedServerIPs []string `json:"allowed_server_ips,omitempty"`
	TLSCertFile      string   `json:"tls_cert_file,omitempty"`
	TLSKeyFile       string   `json:"tls_key_file,omitempty"`

	// Her iki mod için ortak. Her girdi mutlak bir mount yoludur ya da sunucudaki her gerçek
	// dosya sistemi için "auto"dur (sanal, bellek tabanlı ve snap paketleri gibi salt okunur imaj
	// mount'ları dışarıda bırakılır). Bir mount'u raporlamak tek başına alert üretmez: hangi
	// mount'ların alert üreteceği agent başına panelden seçilir.
	DiskMounts []string `json:"disk_mounts,omitempty"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	// Faz 3 yapılandırmaları "mode" alanından öncedir ve hepsi push modudur.
	if cfg.Mode == "" {
		cfg.Mode = ModePush
	}

	switch cfg.Mode {
	case ModePush:
		if cfg.ServerURL == "" || cfg.HostID == "" || cfg.APIToken == "" {
			return nil, fmt.Errorf("push mode requires server_url, host_id, and api_token")
		}
		if cfg.IntervalSeconds <= 0 {
			cfg.IntervalSeconds = 30
		}
		if cfg.CACertFile != "" && cfg.InsecureSkipVerify {
			return nil, fmt.Errorf("ca_cert_file and insecure_skip_verify contradict each other: use the CA file to verify the server, or skip verification, not both")
		}
		// Kullanılamaz bir CA dosyasında ilk push'ta değil, açılışta hata ver.
		if _, err := cfg.RootCAs(); err != nil {
			return nil, err
		}
	case ModePull:
		if cfg.CACertFile != "" {
			return nil, fmt.Errorf("ca_cert_file only applies to push mode (it verifies the server the agent connects to)")
		}
		if cfg.ListenAddr == "" || cfg.PullEndpoint == "" || cfg.PullSecret == "" {
			return nil, fmt.Errorf("pull mode requires listen_addr, pull_endpoint, and pull_secret")
		}
		if !strings.HasPrefix(cfg.PullEndpoint, "/") {
			return nil, fmt.Errorf("pull_endpoint must start with /")
		}
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			return nil, fmt.Errorf("pull mode requires tls_cert_file and tls_key_file (HTTPS is mandatory — see docs/MIMARI.md section 5)")
		}
		if len(cfg.AllowedServerIPs) == 0 {
			return nil, fmt.Errorf("pull mode requires at least one entry in allowed_server_ips")
		}
	default:
		return nil, fmt.Errorf("mode must be %q or %q", ModePush, ModePull)
	}

	if len(cfg.DiskMounts) == 0 {
		cfg.DiskMounts = []string{"/"}
	}
	for _, m := range cfg.DiskMounts {
		// "data" gibi bir yazım hatası aksi halde sonsuza dek sessizce atlanırdı.
		if m != "auto" && !strings.HasPrefix(m, "/") {
			return nil, fmt.Errorf(`disk_mounts entry %q must be an absolute path or "auto" (every real filesystem)`, m)
		}
	}

	return &cfg, nil
}

// RootCAs, ca_cert_file'dan kurulan havuzu döndürür; hiçbiri yapılandırılmamışsa nil
// (anlamı: sistemin güvendiği köklere karşı doğrula).
func (c *Config) RootCAs() (*x509.CertPool, error) {
	if c.CACertFile == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(c.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read ca_cert_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ca_cert_file %s contains no PEM-encoded certificate", c.CACertFile)
	}
	return pool, nil
}
