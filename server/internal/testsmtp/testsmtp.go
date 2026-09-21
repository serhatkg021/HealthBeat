// Package testsmtp, testler için asgari bir süreç içi SMTP server'ıdır: her iletiyi kabul eder
// ve kaydeder; böylece testler bildiricinin tel üzerine gerçekte ne koyduğunu denetleyebilir.
// Düz SMTP, STARTTLS ya da örtük TLS (465 portu tarzı) konuşabilir ve AUTH PLAIN denemelerini
// kaydeder.
package testsmtp

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

type Message struct {
	From string
	To   []string
	Data string // başlıklar + gövde, dot-unstuffed
}

// Text, iletiyi bir okuyucunun göreceği biçimde döndürür: "Subject: ..." satırı (RFC 2047 çözülmüş),
// boş satır ve (quoted-printable ise çözülmüş) gövde. Kodlamayı değil içeriği denetleyen testler
// için; başlık/kodlama denetimleri Data üzerinde yapılır.
func (m Message) Text() string {
	parsed, err := mail.ReadMessage(strings.NewReader(m.Data))
	if err != nil {
		return m.Data
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		subject = parsed.Header.Get("Subject")
	}
	var body io.Reader = parsed.Body
	if strings.EqualFold(parsed.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		body = quotedprintable.NewReader(body)
	}
	raw, _ := io.ReadAll(body)
	return "Subject: " + subject + "\n\n" + string(raw)
}

// Mode, server'ın bağlantıyı nasıl güvenceye aldığını seçer.
type Mode int

const (
	Plain    Mode = iota // TLS yok
	StartTLS             // STARTTLS sunar ve istek üzerine yükseltir
	Implicit             // ilk bayttan TLS (465 portu tarzı)
)

type Server struct {
	Host string
	Port string

	ln   net.Listener
	mode Mode
	tls  *tls.Config

	mu    sync.Mutex
	msgs  []Message
	auths []string // alınan ham AUTH komut satırları
	// usedTLS[i], i numaralı iletinin şifreli bir bağlantıyla gelip gelmediğini bildirir.
	usedTLS []bool
}

// Start, rastgele bir localhost portunda düz SMTP konuşarak dinler.
func Start(t *testing.T) *Server { return StartMode(t, Plain) }

// StartMode, seçilmiş bir taşıma güvenliği moduyla Start'tır. TLS modları tek kullanımlık
// kendinden imzalı bir sertifika kullanır; bu yüzden test edilen host'lar doğrulamayı atlamalı.
func StartMode(t *testing.T, mode Mode) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testsmtp: listen: %v", err)
	}
	s := &Server{ln: ln, mode: mode}
	s.Host, s.Port, _ = net.SplitHostPort(ln.Addr().String())
	if mode != Plain {
		s.tls = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	}
	if mode == Implicit {
		s.ln = tls.NewListener(ln, s.tls)
	}
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

// Messages, şimdiye kadar alınan her şeyin bir anlık görüntüsünü döndürür.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.msgs...)
}

// Auths, alınan AUTH komut satırlarını döndürür.
func (s *Server) Auths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// Encrypted, alınan her ileti için TLS ile gelip gelmediğini bildirir.
func (s *Server) Encrypted() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.usedTLS...)
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "testsmtp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer func() { conn.Close() }()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	reply := func(line string) { conn.Write([]byte(line + "\r\n")) }

	_, encrypted := conn.(*tls.Conn)
	var cur Message
	reply("220 testsmtp ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			ext := []string{"250-testsmtp", "250-AUTH PLAIN"}
			if s.mode == StartTLS && !encrypted {
				ext = append(ext, "250-STARTTLS")
			}
			ext = append(ext, "250 8BITMIME")
			for _, l := range ext {
				reply(l)
			}
		case strings.HasPrefix(upper, "HELO"):
			reply("250 testsmtp")
		case upper == "STARTTLS" && s.mode == StartTLS && !encrypted:
			reply("220 go ahead")
			tc := tls.Server(conn, s.tls)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, encrypted = tc, true
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			r = bufio.NewReader(conn)
			reply = func(line string) { conn.Write([]byte(line + "\r\n")) }
		case strings.HasPrefix(upper, "AUTH"):
			s.mu.Lock()
			s.auths = append(s.auths, line)
			s.mu.Unlock()
			reply("235 authenticated")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			cur = Message{From: addr(line)}
			reply("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			cur.To = append(cur.To, addr(line))
			reply("250 ok")
		case upper == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				l = strings.TrimRight(l, "\r\n")
				if l == "." {
					break
				}
				sb.WriteString(strings.TrimPrefix(l, ".") + "\n") // dot-unstuff
			}
			cur.Data = sb.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.usedTLS = append(s.usedTLS, encrypted)
			s.mu.Unlock()
			reply("250 queued")
		case upper == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// addr, <...> arasındaki adresi çıkarır; ardından gelebilecek "BODY=8BITMIME" gibi ESMTP
// parametrelerini yok sayar.
func addr(line string) string {
	_, after, _ := strings.Cut(line, ":")
	after = strings.TrimSpace(after)
	if start := strings.Index(after, "<"); start >= 0 {
		if end := strings.Index(after[start:], ">"); end > 0 {
			return after[start+1 : start+end]
		}
	}
	return strings.Fields(after + " ")[0]
}
