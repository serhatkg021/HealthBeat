package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"healthbeat-server/internal/httpapi"
	"healthbeat-server/internal/testdb"
)

// E-posta ile şifre sıfırlama: gerçek router, store'lar ve geçici şema üzerinde uçtan uca.

const panelBase = "https://panel.test"

type sentMail struct {
	to      []string
	subject string
	body    string
}

// fakeMailer, gönderilen e-postaları toplar; enabled=false gerçek bir SMTP olmayan ortamı temsil eder.
type fakeMailer struct {
	mu      sync.Mutex
	enabled bool
	sent    []sentMail
}

func (f *fakeMailer) Enabled() bool { return f.enabled }

func (f *fakeMailer) Send(_ context.Context, to []string, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{to: to, subject: subject, body: body})
	return nil
}

func (f *fakeMailer) messages() []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMail(nil), f.sent...)
}

// newResetAPI, e-posta ile sıfırlaması açık bir API ve bir kullanıcı (u@x.test) kurar.
func newResetAPI(t *testing.T) (*api, *fakeMailer) {
	t.Helper()
	a := newAPI(t)
	mail := &fakeMailer{enabled: true}
	a.deps.SetPasswordReset(mail, panelBase)
	testdb.User(t, a.pool, "u@x.test", "operator", password)
	return a, mail
}

// mails, arka planda giden e-postaların bitmesini bekler ve gönderilenleri döndürür.
func (a *api) mails(m *fakeMailer) []sentMail {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.deps.WaitForMail(ctx); err != nil {
		a.t.Fatalf("waiting for mail: %v", err)
	}
	return m.messages()
}

var tokenInMail = regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`)

// tokenFrom, e-postadaki bağlantıdan token'ı çıkarır.
func tokenFrom(t *testing.T, m sentMail) string {
	t.Helper()
	match := tokenInMail.FindStringSubmatch(m.body)
	if match == nil {
		t.Fatalf("no reset link in mail body:\n%s", m.body)
	}
	return match[1]
}

func (a *api) forgot(email string, wantStatus int) {
	a.t.Helper()
	a.expect(wantStatus, "POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": email}, nil)
}

func (a *api) reset(token, newPassword string, wantStatus int) {
	a.t.Helper()
	a.expect(wantStatus, "POST", "/api/v1/auth/reset-password", "", map[string]string{"token": token, "new_password": newPassword}, nil)
}

func (a *api) count(query string, args ...any) int {
	a.t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		a.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestPasswordResetFullFlow(t *testing.T) {
	a, mail := newResetAPI(t)

	// Şifreyi geçici olarak "değiştirmek zorunda" işaretle ve açık bir oturum aç: sıfırlama ikisini de çözmeli.
	if _, err := a.pool.Exec(context.Background(), `UPDATE users SET must_change_password = true WHERE email = 'u@x.test'`); err != nil {
		t.Fatal(err)
	}
	var old struct {
		RefreshToken string `json:"refresh_token"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": password}, &old)

	a.forgot("u@x.test", 204)
	msgs := a.mails(mail)
	if len(msgs) != 1 {
		t.Fatalf("want 1 mail, got %d", len(msgs))
	}
	if got := msgs[0].to; len(got) != 1 || got[0] != "u@x.test" {
		t.Fatalf("mail recipients = %v", got)
	}
	if !strings.Contains(msgs[0].body, panelBase+"/reset-password#token=") {
		t.Fatalf("link must point at the configured panel address in the URL fragment:\n%s", msgs[0].body)
	}
	if !strings.Contains(msgs[0].body, "30 dakika") {
		t.Fatalf("mail must state the validity period:\n%s", msgs[0].body)
	}
	token := tokenFrom(t, msgs[0])

	// Ham token saklanmaz; yalnızca özeti.
	sum := sha256.Sum256([]byte(token))
	if n := a.count(`SELECT count(*) FROM password_reset_tokens WHERE token_hash = $1`, token); n != 0 {
		t.Fatal("the raw token must never be stored")
	}
	if n := a.count(`SELECT count(*) FROM password_reset_tokens WHERE token_hash = $1`, hex.EncodeToString(sum[:])); n != 1 {
		t.Fatal("the SHA-256 of the token must be stored")
	}

	const newPassword = "a-brand-new-passphrase"
	a.reset(token, newPassword, 204)

	// Yeni şifre çalışır, eskisi çalışmaz; bayrak temizlenir; eski oturum kapanır.
	a.expect(401, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": password}, nil)
	var fresh struct {
		User struct {
			MustChangePassword bool `json:"must_change_password"`
		} `json:"user"`
	}
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": newPassword}, &fresh)
	if fresh.User.MustChangePassword {
		t.Fatal("a password the user chose themselves must clear must_change_password")
	}
	a.expect(401, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": old.RefreshToken}, nil)

	// Denetim kaydı ve bilgilendirme e-postası.
	if n := a.count(`SELECT count(*) FROM audit_logs WHERE action = 'auth.password_reset_requested' AND actor_email = 'u@x.test'`); n != 1 {
		t.Fatalf("audit auth.password_reset_requested = %d, want 1", n)
	}
	if n := a.count(`SELECT count(*) FROM audit_logs WHERE action = 'auth.password_reset' AND actor_email = 'u@x.test'`); n != 1 {
		t.Fatalf("audit auth.password_reset = %d, want 1", n)
	}
	msgs = a.mails(mail)
	if len(msgs) != 2 || !strings.Contains(msgs[1].subject, "değiştirildi") {
		t.Fatalf("want a confirmation mail after the reset, got %+v", msgs)
	}
	if strings.Contains(msgs[1].body, token) || strings.Contains(msgs[1].body, newPassword) {
		t.Fatal("the confirmation mail must not contain the token or the password")
	}

	// Bağlantı tek kullanımlıktır.
	a.reset(token, "yet-another-passphrase", 400)
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": newPassword}, nil)
}

func TestForgotPasswordDoesNotRevealWhetherTheAccountExists(t *testing.T) {
	a, mail := newResetAPI(t)

	var known, unknown map[string]any
	if got := a.call("POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "u@x.test"}, &known); got != 204 {
		t.Fatalf("known e-mail: %d", got)
	}
	if got := a.call("POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "nobody@x.test"}, &unknown); got != 204 {
		t.Fatalf("unknown e-mail: %d", got)
	}
	if len(known) != 0 || len(unknown) != 0 {
		t.Fatalf("responses must carry no body: %v / %v", known, unknown)
	}
	msgs := a.mails(mail)
	if len(msgs) != 1 || msgs[0].to[0] != "u@x.test" {
		t.Fatalf("only the existing account gets mail, got %+v", msgs)
	}
	if n := a.count(`SELECT count(*) FROM audit_logs WHERE action = 'auth.password_reset_requested'`); n != 1 {
		t.Fatalf("an unknown e-mail must leave no audit trail, got %d entries", n)
	}
}

func TestForgotPasswordIsCaseAndWhitespaceInsensitive(t *testing.T) {
	a, mail := newResetAPI(t)
	a.forgot("  U@X.Test ", 204)
	if msgs := a.mails(mail); len(msgs) != 1 {
		t.Fatalf("want 1 mail, got %d", len(msgs))
	}
}

func TestForgotPasswordRejectsMalformedRequests(t *testing.T) {
	// Sınırlayıcı biçimsiz istekleri de sayar (bilerek); bu yüzden istekler kaynak IP bütçesinin altında kalacak
	// gruplara bölünür ve her grup taze bir API kullanır.
	bad := []string{"", "   ", "no-at-sign", "@x.test", "u@", "a b@x.test", "u@x.test\r\nBcc: evil@x.test", "<u@x.test>"}
	for start := 0; start < len(bad); start += 4 {
		a, mail := newResetAPI(t)
		for _, email := range bad[start : start+4] {
			a.forgot(email, 400)
		}
		if start == 0 {
			a.expect(400, "POST", "/api/v1/auth/forgot-password", "", rawBody(`{not json`), nil)
		}
		if msgs := a.mails(mail); len(msgs) != 0 {
			t.Fatalf("malformed requests must not send mail, got %+v", msgs)
		}
	}
}

func TestResetLinkIgnoresRequestHostHeaders(t *testing.T) {
	a, mail := newResetAPI(t)
	a.expect(204, "POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "u@x.test"}, nil,
		"X-Forwarded-Host", "evil.test", "Origin", "https://evil.test", "Referer", "https://evil.test/")
	msgs := a.mails(mail)
	if len(msgs) != 1 || !strings.Contains(msgs[0].body, panelBase+"/reset-password#token=") || strings.Contains(msgs[0].body, "evil") {
		t.Fatalf("the link root must come from configuration, not request headers:\n%+v", msgs)
	}
}

func TestResetRejectsWeakPasswordWithoutBurningTheLink(t *testing.T) {
	a, mail := newResetAPI(t)
	a.forgot("u@x.test", 204)
	token := tokenFrom(t, a.mails(mail)[0])

	a.reset(token, "short", 400)
	a.reset(token, "", 400)
	a.reset("", "a-brand-new-passphrase", 400)
	a.reset(token, "a-brand-new-passphrase", 204) // hâlâ geçerli
}

func TestResetErrorCodeSeparatesDeadLinksFromFixableMistakes(t *testing.T) {
	a, mail := newResetAPI(t)
	a.forgot("u@x.test", 204)
	token := tokenFrom(t, a.mails(mail)[0])

	var dead, weak map[string]string
	a.expect(400, "POST", "/api/v1/auth/reset-password", "", map[string]string{"token": "not-a-real-token", "new_password": "a-brand-new-passphrase"}, &dead)
	a.expect(400, "POST", "/api/v1/auth/reset-password", "", map[string]string{"token": token, "new_password": "short"}, &weak)
	if dead["code"] != "reset_link_invalid" {
		t.Fatalf("a dead link needs code reset_link_invalid, got %v", dead)
	}
	if weak["code"] != "validation_failed" {
		t.Fatalf("a weak password is fixable on the same page and must not signal a dead link (want validation_failed), got %v", weak)
	}
}

func TestResetRejectsUnknownAndExpiredTokens(t *testing.T) {
	a, mail := newResetAPI(t)
	a.reset("not-a-real-token", "a-brand-new-passphrase", 400)
	a.reset(strings.Repeat("x", 500), "a-brand-new-passphrase", 400)

	a.forgot("u@x.test", 204)
	token := tokenFrom(t, a.mails(mail)[0])
	if _, err := a.pool.Exec(context.Background(), `UPDATE password_reset_tokens SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	a.reset(token, "a-brand-new-passphrase", 400)
	// Şifre değişmedi.
	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "u@x.test", "password": password}, nil)
}

func TestNewRequestInvalidatesThePreviousLink(t *testing.T) {
	a, mail := newResetAPI(t)
	a.forgot("u@x.test", 204)
	a.forgot("u@x.test", 204)
	msgs := a.mails(mail)
	if len(msgs) != 2 {
		t.Fatalf("want 2 mails, got %d", len(msgs))
	}
	first, second := tokenFrom(t, msgs[0]), tokenFrom(t, msgs[1])
	if first == second {
		t.Fatal("every request must produce a fresh token")
	}
	a.reset(first, "a-brand-new-passphrase", 400)
	a.reset(second, "a-brand-new-passphrase", 204)
}

func TestForgotPasswordIsThrottled(t *testing.T) {
	a, mail := newResetAPI(t)

	// Aynı e-posta: fazlası sessizce yok sayılır (yanıt aynı, posta gitmez): posta bombası olmaz. (5 istek: IP
	// bütçesinin içinde kalır, yalnızca e-posta sınırı devreye girer.)
	// Büyük/küçük harf ya da boşlukla e-posta sınırı atlatılamaz.
	for _, email := range []string{"u@x.test", "U@X.TEST", " u@x.test", "u@x.test ", "U@x.Test"} {
		a.forgot(email, 204)
	}
	if msgs := a.mails(mail); len(msgs) != 3 {
		t.Fatalf("one address may receive only a few mails in a row, got %d", len(msgs))
	}

	// Aynı IP: bütçe bitince 429 ve Retry-After.
	got := 0
	for i := 0; i < 10; i++ {
		code, hdr := a.callHeaders("POST", "/api/v1/auth/forgot-password", "", nil)
		if code == 429 {
			if hdr.Get("Retry-After") == "" {
				t.Fatal("a 429 needs Retry-After")
			}
			got++
		}
	}
	if got == 0 {
		t.Fatal("the request source must eventually be throttled")
	}
}

func TestPasswordResetIsOffWithoutSMTPOrBaseURL(t *testing.T) {
	type opts struct {
		PasswordResetEnabled bool `json:"password_reset_enabled"`
	}
	cases := []struct {
		name    string
		mailer  *fakeMailer
		baseURL string
		enabled bool
	}{
		{"nothing configured", nil, "", false},
		{"SMTP without base URL", &fakeMailer{enabled: true}, "", false},
		{"base URL without SMTP", &fakeMailer{enabled: false}, panelBase, false},
		{"both", &fakeMailer{enabled: true}, panelBase, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAPI(t)
			testdb.User(t, a.pool, "u@x.test", "operator", password)
			if tc.mailer != nil {
				a.deps.SetPasswordReset(tc.mailer, tc.baseURL)
			}
			var o opts
			a.expect(200, "GET", "/api/v1/auth/options", "", nil, &o)
			if o.PasswordResetEnabled != tc.enabled {
				t.Fatalf("password_reset_enabled = %v, want %v", o.PasswordResetEnabled, tc.enabled)
			}
			// Kapalıyken de yanıt aynıdır (204) ve token üretilmez.
			a.forgot("u@x.test", 204)
			if !tc.enabled {
				if n := a.count(`SELECT count(*) FROM password_reset_tokens`); n != 0 {
					t.Fatalf("no token may be issued while the feature is off, got %d", n)
				}
				if tc.mailer != nil {
					if msgs := a.mails(tc.mailer); len(msgs) != 0 {
						t.Fatalf("no mail may be sent while the feature is off, got %+v", msgs)
					}
				}
			}
		})
	}
}

func TestDeletingAUserDeletesTheirResetLinks(t *testing.T) {
	a, _ := newResetAPI(t)
	a.forgot("u@x.test", 204)
	if n := a.count(`SELECT count(*) FROM password_reset_tokens`); n != 1 {
		t.Fatalf("tokens = %d", n)
	}
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM users WHERE email = 'u@x.test'`); err != nil {
		t.Fatal(err)
	}
	if n := a.count(`SELECT count(*) FROM password_reset_tokens`); n != 0 {
		t.Fatalf("a deleted user's links must go with them, got %d", n)
	}
}

// Başarısız sıfırlama denemeleri giriş denemeleriyle aynı bütçeden düşer: token tahmini kısılır.
func TestFailedResetAttemptsAreThrottled(t *testing.T) {
	a := newAPIWithLimits(t, httpapi.RateLimits{AuthFailuresPerMinute: 1, IngestPerMinute: 6000})
	a.deps.SetPasswordReset(&fakeMailer{enabled: true}, panelBase)
	throttled := 0
	for i := 0; i < 15; i++ {
		code := a.call("POST", "/api/v1/auth/reset-password", "", map[string]string{"token": "guess", "new_password": "a-brand-new-passphrase"}, nil)
		if code == 429 {
			throttled++
		} else if code != 400 {
			t.Fatalf("attempt %d = %d, want 400 or 429", i, code)
		}
	}
	if throttled == 0 {
		t.Fatal("repeated wrong tokens from one source must be throttled")
	}
}
