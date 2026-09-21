package notify

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"healthbeat-server/internal/testsmtp"
)

func mailer(srv *testsmtp.Server, username string) *Mailer {
	m := New(Config{Host: srv.Host, Port: srv.Port, From: "alerts@healthbeat.test", Username: username, Password: "pw"})
	m.tlsConfig = &tls.Config{InsecureSkipVerify: true} // sahte sunucunun sertifikası kendinden imzalı
	return m
}

func TestSendDeliversOverPlainSMTP(t *testing.T) {
	srv := testsmtp.Start(t)
	err := mailer(srv, "").Send(context.Background(), []string{"a@example.com", "b@example.com"}, "[HealthBeat] CRITICAL cpu alert: web-1", "Host: web-1\n")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	got := msgs[0]
	if got.From != "alerts@healthbeat.test" || strings.Join(got.To, ",") != "a@example.com,b@example.com" {
		t.Errorf("envelope = %s -> %v", got.From, got.To)
	}
	for _, want := range []string{
		"Subject: [HealthBeat] CRITICAL cpu alert: web-1", "To: a@example.com, b@example.com", "Host: web-1",
		"Date: ", "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8",
	} {
		if !strings.Contains(got.Data, want) {
			t.Errorf("message missing %q:\n%s", want, got.Data)
		}
	}
	if len(srv.Auths()) != 0 {
		t.Error("AUTH attempted without configured credentials")
	}
}

func TestSendUpgradesWithSTARTTLSWhenOffered(t *testing.T) {
	srv := testsmtp.StartMode(t, testsmtp.StartTLS)
	if err := mailer(srv, "").Send(context.Background(), []string{"a@example.com"}, "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if enc := srv.Encrypted(); len(enc) != 1 || !enc[0] {
		t.Fatalf("message was delivered without TLS although the server offered STARTTLS: %v", enc)
	}
}

func TestSendUsesImplicitTLSOnPort465(t *testing.T) {
	srv := testsmtp.StartMode(t, testsmtp.Implicit)
	m := mailer(srv, "")
	m.cfg.Port = "465"
	// Sahte sunucu rastgele bir portta dinler; "465" bağlantısını ona yönlendir.
	m.dialOverride = func(ctx context.Context) (net.Conn, error) {
		return (&tls.Dialer{Config: m.tlsCfg()}).DialContext(ctx, "tcp", net.JoinHostPort(srv.Host, srv.Port))
	}
	if err := m.Send(context.Background(), []string{"a@example.com"}, "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if enc := srv.Encrypted(); len(enc) != 1 || !enc[0] {
		t.Fatalf("implicit-TLS message not encrypted: %v", enc)
	}
}

func TestSendAuthenticatesWhenCredentialsConfigured(t *testing.T) {
	srv := testsmtp.StartMode(t, testsmtp.StartTLS)
	if err := mailer(srv, "mailer-user").Send(context.Background(), []string{"a@example.com"}, "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	auths := srv.Auths()
	if len(auths) != 1 || !strings.HasPrefix(strings.ToUpper(auths[0]), "AUTH PLAIN") {
		t.Fatalf("AUTH lines = %v, want one AUTH PLAIN", auths)
	}
}

func TestSendWithoutSMTPHostIsLogOnly(t *testing.T) {
	if err := New(Config{}).Send(context.Background(), []string{"a@example.com"}, "subject", "body"); err != nil {
		t.Fatalf("log-only Send returned an error: %v", err)
	}
}

func TestSendToNobodyDoesNotConnect(t *testing.T) {
	srv := testsmtp.Start(t)
	if err := mailer(srv, "").Send(context.Background(), nil, "subject", "body"); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Messages()); n != 0 {
		t.Fatalf("%d message(s) sent to an empty recipient list", n)
	}
}

func TestSendReportsUnreachableServer(t *testing.T) {
	m := New(Config{Host: "127.0.0.1", Port: "1", From: "alerts@healthbeat.test"}) // 1. portta hiçbir şey dinlemiyor
	if err := m.Send(context.Background(), []string{"a@example.com"}, "s", "b"); err == nil {
		t.Fatal("expected an error when the SMTP server is unreachable")
	}
}

// Regresyon: net/smtp.SendMail context'leri yok sayıyor ve zaman aşımı yoktu; bu yüzden TCP
// bağlantısını kabul edip sonra hiçbir şey söylemeyen bir relay çağıranı süresiz bloke ediyordu
// (ölçüldü: 1 sn süre sınırıyla 8 sn sonra hâlâ bloklu).
func TestSendHonoursContextAgainstAHungServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // açık tut, karşılama mesajını asla gönderme
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- New(Config{Host: host, Port: port, From: "a@x"}).Send(ctx, []string{"b@x"}, "s", "b") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send succeeded against a server that never answered")
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("Send took %v to give up on a 500ms deadline", elapsed)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Send is still blocked long after its context expired")
	}
}

func TestSendCancellationInterruptsAnInFlightSession(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.Write([]byte("220 hi\r\n")) // karşıla, sonra konuşma ortasında sessizleş
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(Config{Host: host, Port: port, From: "a@x"}).Send(ctx, []string{"b@x"}, "s", "b") }()
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send reported success after cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the context did not interrupt the SMTP session")
	}
}

func TestBuildMessageNeutralisesHeaderInjection(t *testing.T) {
	msg := string(buildMessage("a@x", []string{"b@x"},
		"[HealthBeat] CRITICAL cpu alert: evil\r\nBcc: attacker@evil.test\r\n\r\nphishing body", "real body", time.Unix(0, 0)))

	header, body, _ := strings.Cut(msg, "\r\n\r\n")
	if strings.Contains(header, "\r\nBcc:") {
		t.Fatalf("a hostname containing CRLF injected a header:\n%s", msg)
	}
	if strings.Contains(strings.SplitN(msg, "\r\n\r\n", 2)[0], "phishing") && !strings.Contains(header, "Subject:") {
		t.Fatal("subject was split")
	}
	if strings.TrimSpace(body) != "real body" {
		t.Fatalf("body = %q; injected text must not become the body", body)
	}
}

func TestBuildMessageEncodesNonASCIISubject(t *testing.T) {
	msg := string(buildMessage("a@x", []string{"b@x"}, "Sunucu çevrimdışı: web-ş", "b", time.Unix(0, 0)))
	subjectLine := ""
	for _, l := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(l, "Subject:") {
			subjectLine = l
		}
	}
	if !strings.Contains(subjectLine, "=?utf-8?q?") {
		t.Fatalf("non-ASCII subject not RFC 2047 encoded: %q", subjectLine)
	}
}

// From ve To başlıklara olduğu gibi yazılır; bu yüzden içlerindeki bir CR/LF (bir kullanıcı
// e-postası ya da yapılandırılmış gönderici) yeni bir başlık açmamalı ya da başlık bloğunu bitirmemeli.
func TestBuildMessageNeutralisesHeaderInjectionInAddresses(t *testing.T) {
	for name, msg := range map[string]string{
		"to":   string(buildMessage("a@x", []string{"b@x\r\nBcc: attacker@evil.test"}, "s", "real body", time.Unix(0, 0))),
		"from": string(buildMessage("a@x\r\nBcc: attacker@evil.test", []string{"b@x"}, "s", "real body", time.Unix(0, 0))),
	} {
		header, body, _ := strings.Cut(msg, "\r\n\r\n")
		if strings.Contains(header, "\r\nBcc:") {
			t.Errorf("%s: CRLF injected a header:\n%s", name, header)
		}
		if strings.TrimSpace(body) != "real body" {
			t.Errorf("%s: body = %q", name, body)
		}
	}
}

// Türkçe konu ve gövde: konu RFC 2047, gövde quoted-printable olarak yalnızca ASCII gider ve
// okuyucunun tarafında aynen geri çözülür.
func TestTurkishSubjectAndBodyTravelAsASCIIAndDecodeBack(t *testing.T) {
	srv := testsmtp.Start(t)
	subject := "[HealthBeat] KRİTİK disk kayboldu alert'i: sunucu-1 (mount /veri)"
	body := "Sunucu: sunucu-1\nAyrıntı: /veri mount'u son 3 raporda görünmedi (yanıt vermiyor).\nOluşturulma: şimdi ğüşiöç\n"
	if err := mailer(srv, "").Send(context.Background(), []string{"a@example.com"}, subject, body); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg := srv.Messages()[0]
	for i := 0; i < len(msg.Data); i++ {
		if msg.Data[i] > 127 {
			t.Fatalf("byte %d of the message is not ASCII: the server may not accept 8-bit data:\n%s", i, msg.Data)
		}
	}
	if !strings.Contains(msg.Data, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("the body encoding is not declared:\n%s", msg.Data)
	}
	if got, want := msg.Text(), "Subject: "+subject+"\n\n"+body; got != want {
		t.Fatalf("decoded message differs:\n got: %q\nwant: %q", got, want)
	}
}
