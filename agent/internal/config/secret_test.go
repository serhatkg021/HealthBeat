package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setMachineID, makine kimliğini sahte bir dosyadan okutur ("" = hiçbir kimlik dosyası yok).
func setMachineID(t *testing.T, id string) {
	t.Helper()
	old := machineIDPaths
	t.Cleanup(func() { machineIDPaths = old })
	path := filepath.Join(t.TempDir(), "machine-id")
	if id != "" {
		if err := os.WriteFile(path, []byte(id+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	machineIDPaths = []string{path}
}

func encrypt(t *testing.T, plain string) string {
	t.Helper()
	enc, err := EncryptSecret(plain)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func pushConfig(token string) string {
	return `{"server_url":"https://s","host_id":"id","api_token":"` + token + `"}`
}

func TestEncryptedTokenRoundTrip(t *testing.T) {
	setMachineID(t, "4c4c4544003910548036b4c04f4b3333")
	enc := encrypt(t, "hb_secret-token")
	if !strings.HasPrefix(enc, "enc:v1:") || strings.Contains(enc, "hb_secret-token") {
		t.Fatalf("encrypted = %q", enc)
	}
	if again := encrypt(t, "hb_secret-token"); again == enc {
		t.Fatal("two encryptions of the same secret are identical (nonce reused)")
	}

	cfg, err := load(t, pushConfig(enc))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "hb_secret-token" || cfg.PlaintextSecret != "" {
		t.Fatalf("api_token = %q, plaintext = %q", cfg.APIToken, cfg.PlaintextSecret)
	}
}

func TestEncryptedPullSecret(t *testing.T) {
	setMachineID(t, "4c4c4544003910548036b4c04f4b3333")
	enc := encrypt(t, "pull-s3cret")
	cfg, err := load(t, strings.Replace(validPull, `"pull_secret":"s"`, `"pull_secret":"`+enc+`"`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PullSecret != "pull-s3cret" || cfg.PlaintextSecret != "" {
		t.Fatalf("pull_secret = %q, plaintext = %q", cfg.PullSecret, cfg.PlaintextSecret)
	}
}

func TestPlaintextSecretStillWorks(t *testing.T) {
	setMachineID(t, "") // düz sır makine kimliği gerektirmez
	cfg, err := load(t, pushConfig("plain-token"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "plain-token" || cfg.PlaintextSecret != "api_token" {
		t.Fatalf("api_token = %q, plaintext = %q", cfg.APIToken, cfg.PlaintextSecret)
	}

	cfg, err = load(t, validPull)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlaintextSecret != "pull_secret" {
		t.Fatalf("pull plaintext = %q", cfg.PlaintextSecret)
	}
}

func TestEncryptedTokenFromAnotherMachine(t *testing.T) {
	setMachineID(t, "11111111111111111111111111111111")
	enc := encrypt(t, "hb_secret-token")

	setMachineID(t, "22222222222222222222222222222222") // kopyalanan dosya ya da klonlanan VM
	_, err := load(t, pushConfig(enc))
	if err == nil || !strings.Contains(err.Error(), "api_token bu makinede çözülemedi") || !strings.Contains(err.Error(), RepairHint) {
		t.Fatalf("err = %v", err)
	}
}

func TestMalformedEncryptedToken(t *testing.T) {
	setMachineID(t, "4c4c4544003910548036b4c04f4b3333")
	enc := encrypt(t, "hb_secret-token")
	for _, value := range []string{"enc:v1:", "enc:v1:not*base64", "enc:v1:AAAA", enc[:len(enc)-4]} {
		_, err := load(t, pushConfig(value))
		if err == nil || !strings.Contains(err.Error(), "api_token") || !strings.Contains(err.Error(), RepairHint) {
			t.Errorf("%q: err = %v", value, err)
		}
	}
}

func TestOnlyTheModesSecretIsDecrypted(t *testing.T) {
	// Push yapılandırmasında kalmış, başka makinede şifrelenmiş bir pull_secret açılışı engellemez.
	setMachineID(t, "11111111111111111111111111111111")
	stale := encrypt(t, "old-pull-secret")
	setMachineID(t, "22222222222222222222222222222222")
	cfg, err := load(t, `{"server_url":"https://s","host_id":"id","api_token":"tok","pull_secret":"`+stale+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "tok" {
		t.Fatalf("api_token = %q", cfg.APIToken)
	}
}

func TestMachineIDFallback(t *testing.T) {
	dir := t.TempDir()
	dbus := filepath.Join(dir, "dbus-machine-id")
	if err := os.WriteFile(dbus, []byte("4c4c4544003910548036b4c04f4b3333\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	old := machineIDPaths
	t.Cleanup(func() { machineIDPaths = old })
	machineIDPaths = []string{filepath.Join(dir, "missing"), dbus}

	cfg, err := load(t, pushConfig(encrypt(t, "hb_secret-token")))
	if err != nil || cfg.APIToken != "hb_secret-token" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestEncryptSecretErrors(t *testing.T) {
	setMachineID(t, "4c4c4544003910548036b4c04f4b3333")
	if _, err := EncryptSecret(""); err == nil {
		t.Error("empty secret encrypted")
	}
	if _, err := EncryptSecret(encrypt(t, "x")); err == nil {
		t.Error("already encrypted secret encrypted again")
	}

	setMachineID(t, "")
	if _, err := EncryptSecret("tok"); err == nil || !strings.Contains(err.Error(), "makine kimliği bulunamadı") {
		t.Errorf("without machine id: err = %v", err)
	}
	_, err := load(t, pushConfig("enc:v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"))
	if err == nil || !strings.Contains(err.Error(), "makine kimliği bulunamadı") {
		t.Errorf("decrypt without machine id: err = %v", err)
	}
}
