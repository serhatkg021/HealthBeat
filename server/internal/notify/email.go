// Package notify, alert e-postalarını SMTP üzerinden gönderir (docs/MIMARI.md bölüm 8:
// "Bildirim kanalı (v1): e-posta (SMTP)").
//
// SMTP konuşması smtp.SendMail yerine net/smtp'nin Client'ı üzerinde uygulanır; çünkü SendMail'in
// zaman aşımı yoktur ve context'leri yok sayar: takılan bir relay çağıranı süresiz bloke ederdi.
// Burada her adım çağıranın context'i ve katı bir oturum üst sınırıyla sınırlıdır.
package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"
)

const (
	dialTimeout = 10 * time.Second
	// sessionTimeout tüm konuşmayı sınırlar (bağlanma, TLS, kimlik doğrulama, DATA).
	sessionTimeout = 30 * time.Second
)

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Mailer, Host boşsa etkisizdir (yalnızca log): SMTP yapılandırılmamış bir geliştirme/test
// ortamı bir alert'te asla başarısız olmaz — yalnızca ne gönderileceğini loglar.
//
// Taşıma güvenliği: 465 portu örtük TLS kullanır (bağlantı ilk bayttan TLS'tir); diğer her
// port açık metinle başlar ve sunucu sunduğunda STARTTLS ile yükseltir. Kimlik bilgileri
// yalnızca TLS üzerinden (ya da localhost'a) gönderilir — net/smtp'nin PlainAuth'u aksini reddeder.
type Mailer struct {
	cfg     Config
	enabled bool

	tlsConfig    *tls.Config                                 // test kancası; nil = sistem köklerine karşı doğrula
	dialOverride func(ctx context.Context) (net.Conn, error) // test kancası: ağ bağlantısını değiştirir
}

func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg, enabled: cfg.Host != ""}
}

// Enabled, gerçek bir SMTP sunucusu yapılandırılıp yapılandırılmadığını söyler (false = yalnızca log).
func (m *Mailer) Enabled() bool { return m.enabled }

func (m *Mailer) tlsCfg() *tls.Config {
	if m.tlsConfig != nil {
		c := m.tlsConfig.Clone()
		if c.ServerName == "" {
			c.ServerName = m.cfg.Host
		}
		return c
	}
	return &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
}

func (m *Mailer) Send(ctx context.Context, to []string, subject, body string) error {
	if !m.enabled {
		log.Printf("notify: SMTP not configured, would have sent %q to %v", subject, to)
		return nil
	}
	if len(to) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	conn, err := m.dial(ctx)
	if err != nil {
		return fmt.Errorf("connect to smtp server: %w", err)
	}
	defer conn.Close()

	// Context bittiği anda bekleyen her okuma/yazmayı serbest bırak ve tüm oturumu ayrıca bir
	// soket süre sınırıyla sınırla.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}

	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()

	if _, implicitTLS := conn.(*tls.Conn); !implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(m.tlsCfg()); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}
	if m.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := c.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(m.cfg.From, to, subject, body, time.Now())); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end of data: %w", err)
	}
	return c.Quit()
}

func (m *Mailer) dial(ctx context.Context) (net.Conn, error) {
	if m.dialOverride != nil {
		return m.dialOverride(ctx)
	}
	addr := net.JoinHostPort(m.cfg.Host, m.cfg.Port)
	d := &net.Dialer{Timeout: dialTimeout}
	if m.cfg.Port == "465" {
		return (&tls.Dialer{NetDialer: d, Config: m.tlsCfg()}).DialContext(ctx, "tcp", addr)
	}
	return d.DialContext(ctx, "tcp", addr)
}

// oneLine, s'yi bir başlığa koymak için güvenli kılar: CR/LF aksi halde bir değerin (ör. bir
// yöneticinin alert konusuna yazdığı sunucu adı) fazladan başlık enjekte etmesine ya da
// gövdeyi erken başlatmasına izin verirdi.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func buildMessage(from string, to []string, subject, body string, now time.Time) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: %s\r\n", oneLine(from))
	fmt.Fprintf(&sb, "To: %s\r\n", oneLine(strings.Join(to, ", ")))
	fmt.Fprintf(&sb, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", oneLine(subject)))
	fmt.Fprintf(&sb, "Date: %s\r\n", now.Format(time.RFC1123Z))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	// Gövde Türkçe karakter içerir; 8BITMIME sunmayan sunucularda bozulmasın diye yalnızca ASCII
	// gönderilir (quoted-printable).
	sb.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	sb.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&sb)
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
	return []byte(sb.String())
}
