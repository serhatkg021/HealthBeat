package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// `healthbeat-server healthcheck` container sağlık kontrolüdür (server/Dockerfile HEALTHCHECK): server'ın /healthz
// ucunu container'ın içinden sorar; 200 dönerse 0, aksi hâlde 1 ile çıkar. Kabuk ya da wget çalıştırmaz: busybox
// wget'in TLS için açtığı ssl_client, wget bitince PID 1'e (server) kalıyor ve toplanmadığı için her kontrolde bir
// zombi birikiyordu. Bu komut alt süreç açmaz; Docker onu kendisi başlatıp toplar.

const healthcheckTimeout = 3 * time.Second

func runHealthcheckCommand(stderr io.Writer) int {
	listen := os.Getenv("HTTP_ADDR")
	if listen == "" {
		listen = ":8443"
	}
	url, err := healthcheckURL(listen)
	if err == nil {
		err = checkHealth(url, healthcheckTimeout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	return 0
}

// healthcheckURL, dinleme adresinden container'ın içinden ulaşılacak /healthz adresini çıkarır: adres boş ya da
// tüm arayüzler (0.0.0.0, ::) ise loopback'e, belirli bir adresse o adrese bağlanılır.
func healthcheckURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("HTTP_ADDR %q: %w", listen, err)
	}
	if port == "" {
		return "", fmt.Errorf("HTTP_ADDR %q: missing port", listen)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "https://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// checkHealth, url'ye GET atar ve 200 bekler. Sertifika doğrulanmaz: istek container'ın kendi içine gider ve
// sertifika çoğu kurulumda loopback için değil server'ın dış adı için verilmiştir (eski wget kontrolü de
// --no-check-certificate kullanıyordu).
func checkHealth(url string, timeout time.Duration) error {
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}
