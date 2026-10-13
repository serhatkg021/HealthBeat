// Package conncheck, push modundaki agent'ın server'a ulaşıp ulaşamadığını aşama aşama dener (agent
// -check-connection; kurulum sihirbazı kullanır): adres çözülür, porta bağlanılır, TLS el sıkışması yapılır ve tek bir
// rapor gönderilir. Her aşama kullanıcıya bir ✓/✗ satırı yazar; ilk hatada durulur ve hatanın sınıfı döner. Sihirbaz
// sınıfa göre ne yapılacağını sorar (CA ver, token'ı yeniden gir...).
package conncheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"healthbeat-agent/internal/pusher"
)

// Result, denemenin sonucudur; aynı zamanda agent -check-connection'ın çıkış kodudur (install.sh bu değerlere bakar).
type Result int

const (
	OK               Result = 0
	ResultDNS        Result = 10 // adres çözülemedi
	ResultConnect    Result = 11 // porta ulaşılamadı
	ResultUntrusted  Result = 12 // sertifikaya güvenilmiyor (özel CA)
	ResultName       Result = 13 // sertifika bu adres için değil
	ResultTLS        Result = 14 // diğer TLS hataları (süresi dolmuş sertifika, protokol)
	ResultAuth       Result = 15 // server token'ı ya da host id'yi kabul etmedi (401)
	ResultRejected   Result = 16 // server raporu başka bir durumla reddetti
	ResultSendFailed Result = 17 // TLS'ten sonra istek gönderilemedi
)

// stepTimeout, ağ aşamalarının (çözme, bağlanma, el sıkışma) her birinin süre sınırıdır.
const stepTimeout = 5 * time.Second

// Sender, tek bir raporu gönderir ve server'ın bildirdiği sürümü döndürür (agent'ta gerçek rapor; testte sahte).
type Sender func(ctx context.Context) (pusher.ServerInfo, error)

// Run, serverURL'e karşı denemeyi yapar ve satırları out'a yazar.
func Run(ctx context.Context, serverURL string, tlsOpts pusher.TLSOptions, send Sender, out io.Writer) Result {
	u, err := url.Parse(serverURL)
	if err != nil || u.Hostname() == "" {
		fail(out, fmt.Sprintf("Server adresi geçersiz: %s", serverURL))
		return ResultDNS
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
	}

	addr := host
	if net.ParseIP(host) == nil {
		cctx, cancel := context.WithTimeout(ctx, stepTimeout)
		ips, err := net.DefaultResolver.LookupHost(cctx, host)
		cancel()
		if err != nil || len(ips) == 0 {
			fail(out, fmt.Sprintf("Adres çözülemedi: %s", host), "DNS kaydını ya da yazdığınız adresi kontrol edin.")
			return ResultDNS
		}
		addr = ips[0]
		ok(out, fmt.Sprintf("Adres çözüldü (%s)", addr))
	}

	target := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: stepTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		fail(out, fmt.Sprintf("Porta ulaşılamıyor: %s", net.JoinHostPort(addr, port)),
			"Güvenlik duvarı bu porta izin veriyor mu, server çalışıyor mu?")
		return ResultConnect
	}
	ok(out, fmt.Sprintf("Porta ulaşıldı (%s)", port))

	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		RootCAs:            tlsOpts.RootCAs,
		InsecureSkipVerify: tlsOpts.InsecureSkipVerify, //nolint:gosec // agent yapılandırmasının açık tercihi
	})
	hctx, cancel := context.WithTimeout(ctx, stepTimeout)
	err = tlsConn.HandshakeContext(hctx)
	cancel()
	if err != nil {
		conn.Close()
		return tlsFailure(out, err, host, tlsOpts.RootCAs != nil)
	}
	state := tlsConn.ConnectionState()
	tlsConn.Close()
	switch {
	case tlsOpts.InsecureSkipVerify:
		ok(out, "Sertifika doğrulanmadı (doğrulama kapalı)")
	case len(state.PeerCertificates) > 0:
		ok(out, fmt.Sprintf("Sertifika doğrulandı (%s)", issuerName(state.PeerCertificates[len(state.PeerCertificates)-1])))
	default:
		ok(out, "Sertifika doğrulandı")
	}

	info, err := send(ctx)
	if err != nil {
		var status *pusher.StatusError
		switch {
		case errors.As(err, &status) && status.Status == 401:
			fail(out, "Server token'ı ya da host id'yi kabul etmedi (401).",
				"Host id'yi panelde sunucunun Ayarlar → Bağlantı ve kimlik",
				"kartındakiyle karşılaştırın. Token kaybolduysa aynı",
				"kartta Kimlik bilgisini yenile ile yenisini alın.")
			return ResultAuth
		case errors.As(err, &status):
			fail(out, fmt.Sprintf("Server raporu reddetti (HTTP %d).", status.Status), shorten(status.Body, 60))
			return ResultRejected
		default:
			fail(out, "Rapor gönderilemedi.", shorten(err.Error(), 60))
			return ResultSendFailed
		}
	}
	if info.Version != "" {
		ok(out, fmt.Sprintf("Rapor kabul edildi (server %s)", info.Version))
	} else {
		ok(out, "Rapor kabul edildi")
	}
	return OK
}

func tlsFailure(out io.Writer, err error, host string, customCA bool) Result {
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &unknown):
		if customCA {
			fail(out, "Sertifikaya güvenilmiyor: verilen CA bu sertifikayı",
				"imzalamamış. Doğru CA dosyasını verin.")
		} else {
			fail(out, "Sertifikaya güvenilmiyor: server'ın sertifikası",
				"özel bir CA ile imzalanmış.")
		}
		return ResultUntrusted
	case errors.As(err, &hostname):
		names := hostname.Certificate.DNSNames
		for _, ip := range hostname.Certificate.IPAddresses {
			names = append(names, ip.String())
		}
		fail(out, fmt.Sprintf("Sertifika bu adres (%s) için düzenlenmemiş.", host),
			shorten("Sertifikadaki adlar: "+strings.Join(names, ", "), 64))
		return ResultName
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		fail(out, "Sertifikanın süresi dolmuş ya da henüz geçerli değil.",
			"Server'ın sertifikasını yenileyin ya da saati kontrol edin.")
		return ResultTLS
	default:
		fail(out, "TLS bağlantısı kurulamadı.", shorten(err.Error(), 60))
		return ResultTLS
	}
}

// issuerName, zincirin en üstündeki sertifikanın imzalayanının okunur adıdır.
func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.String()
}

func ok(out io.Writer, line string) { fmt.Fprintf(out, "  ✓ %s\n", line) }

func fail(out io.Writer, line string, details ...string) {
	fmt.Fprintf(out, "  ✗ %s\n", line)
	for _, d := range details {
		if d != "" {
			fmt.Fprintf(out, "    %s\n", d)
		}
	}
}

// shorten, satırın sihirbazın 72 karakterlik genişliğini aşmaması için metni kısaltır.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
