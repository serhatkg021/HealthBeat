package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestPushDefaults(t *testing.T) {
	// Bir Faz 3 yapılandırması: "mode" yok, aralık yok, disk mount'u yok.
	cfg, err := load(t, `{"server_url":"https://s","host_id":"id","api_token":"tok"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModePush || cfg.IntervalSeconds != 30 || !reflect.DeepEqual(cfg.DiskMounts, []string{"/"}) {
		t.Fatalf("defaults = mode %q interval %d mounts %v", cfg.Mode, cfg.IntervalSeconds, cfg.DiskMounts)
	}
}

func TestPushKeepsExplicitValues(t *testing.T) {
	cfg, err := load(t, `{"mode":"push","server_url":"https://s","host_id":"id","api_token":"tok",
		"interval_seconds":5,"disk_mounts":["/","/data"],"insecure_skip_verify":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntervalSeconds != 5 || len(cfg.DiskMounts) != 2 || !cfg.InsecureSkipVerify {
		t.Fatalf("cfg = %+v", cfg)
	}
}

const validPull = `{"mode":"pull","listen_addr":":9443","pull_endpoint":"/api/v1/status","pull_secret":"s",
	"tls_cert_file":"c.pem","tls_key_file":"k.pem","allowed_server_ips":["10.0.0.1"]}`

func TestPullValid(t *testing.T) {
	cfg, err := load(t, validPull)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModePull || cfg.PullEndpoint != "/api/v1/status" || !reflect.DeepEqual(cfg.DiskMounts, []string{"/"}) {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestValidationErrors(t *testing.T) {
	without := func(field string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(validPull), &m); err != nil {
			t.Fatal(err)
		}
		delete(m, field)
		out, _ := json.Marshal(m)
		return string(out)
	}

	for name, tc := range map[string]struct{ json, want string }{
		"push without token":      {`{"server_url":"https://s","host_id":"id"}`, "api_token"},
		"push without url":        {`{"host_id":"id","api_token":"t"}`, "server_url"},
		"push without id":         {`{"server_url":"https://s","api_token":"t"}`, "host_id"},
		"pull without secret":     {without("pull_secret"), "pull_secret"},
		"pull without listen":     {without("listen_addr"), "listen_addr"},
		"pull without TLS cert":   {without("tls_cert_file"), "tls_cert_file"},
		"pull without TLS key":    {without("tls_key_file"), "tls_key_file"},
		"pull without whitelist":  {without("allowed_server_ips"), "allowed_server_ips"},
		"pull endpoint w/o slash": {strings.Replace(validPull, `"/api/v1/status"`, `"api/v1/status"`, 1), "must start with /"},
		"unknown mode":            {`{"mode":"carrier-pigeon"}`, "mode must be"},
		"not json":                {`{`, "parse config"},
		"empty":                   {``, "parse config"},
	} {
		_, err := load(t, tc.json)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}

	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Errorf("missing file: err = %v", err)
	}
}

// Pull modunda TLS zorunludur (PROMPT bölüm 5); onu atlayan bir yapılandırma sessizce düz
// HTTP'ye dönmek yerine reddedilmeli.
func TestPullNeverAllowsPlainHTTP(t *testing.T) {
	_, err := load(t, `{"mode":"pull","listen_addr":":9443","pull_endpoint":"/s","pull_secret":"x","allowed_server_ips":["10.0.0.1"]}`)
	if err == nil {
		t.Fatal("pull config without TLS files was accepted")
	}
}

func writeCA(t *testing.T) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	path := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	return path
}

func TestCACertFile(t *testing.T) {
	ca := writeCA(t)
	base := `{"server_url":"https://s","host_id":"id","api_token":"tok","ca_cert_file":%q%s}`

	cfg, err := load(t, fmt.Sprintf(base, ca, ""))
	if err != nil {
		t.Fatalf("valid CA file rejected: %v", err)
	}
	pool, err := cfg.RootCAs()
	if err != nil || pool == nil {
		t.Fatalf("RootCAs = %v, %v", pool, err)
	}
	if plain, _ := load(t, `{"server_url":"https://s","host_id":"id","api_token":"tok"}`); plain != nil {
		if p, err := plain.RootCAs(); p != nil || err != nil {
			t.Fatalf("no ca_cert_file: RootCAs = %v, %v; want nil (system roots)", p, err)
		}
	}

	notPEM := filepath.Join(t.TempDir(), "junk.pem")
	os.WriteFile(notPEM, []byte("this is not a certificate"), 0o600)
	for name, tc := range map[string]struct{ json, want string }{
		"missing file":              {fmt.Sprintf(base, filepath.Join(t.TempDir(), "nope.pem"), ""), "read ca_cert_file"},
		"not a certificate":         {fmt.Sprintf(base, notPEM, ""), "no PEM-encoded certificate"},
		"contradicts insecure mode": {fmt.Sprintf(base, ca, `,"insecure_skip_verify":true`), "contradict"},
		"pull mode":                 {strings.Replace(validPull, `{"mode":"pull"`, fmt.Sprintf(`{"ca_cert_file":%q,"mode":"pull"`, ca), 1), "only applies to push mode"},
	} {
		if _, err := load(t, tc.json); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestDiskMountsValidation(t *testing.T) {
	base := `{"server_url":"https://s","host_id":"id","api_token":"tok","disk_mounts":%s}`
	for _, ok := range []string{`["/"]`, `["auto"]`, `["/","auto","/data"]`, `["/mnt/My Disk"]`} {
		if _, err := load(t, fmt.Sprintf(base, ok)); err != nil {
			t.Errorf("disk_mounts %s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{`["data"]`, `["/", "var"]`, `["Auto"]`, `[""]`, `["C:\\data"]`} {
		if _, err := load(t, fmt.Sprintf(base, bad)); err == nil || !strings.Contains(err.Error(), "disk_mounts") {
			t.Errorf("disk_mounts %s: err=%v, want it to be refused (it would be silently skipped forever)", bad, err)
		}
	}
	// Varsayılan hâlâ yalnızca kök dosya sistemidir: auto'ya geçmek açık bir tercihtir.
	cfg, err := load(t, `{"server_url":"https://s","host_id":"id","api_token":"tok"}`)
	if err != nil || len(cfg.DiskMounts) != 1 || cfg.DiskMounts[0] != "/" {
		t.Fatalf("default disk_mounts = %v err=%v", cfg.DiskMounts, err)
	}
}
