package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/testsmtp"
)

func newChannels(t *testing.T) (*Channels, *store.NotificationChannels, *notify.Mailer, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)
	st := store.NewNotificationChannels(pool, testdb.SecretBox(t))
	mailer := notify.New(notify.Config{})
	c, err := NewChannels(context.Background(), st, mailer)
	if err != nil {
		t.Fatal(err)
	}
	return c, st, mailer, testdb.User(t, pool, "admin@x.test", "super_admin", "pw")
}

func smtpOf(t *testing.T, ch model.NotificationChannel) SMTPConfig {
	t.Helper()
	var cfg SMTPConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestChannelUpdateValidatesAndNotifies(t *testing.T) {
	ctx := context.Background()
	c, st, _, admin := newChannels(t)
	var seen []string
	c.OnChange(func(old, upd model.NotificationChannel) {
		seen = append(seen, fmt.Sprintf("%v->%v %s", old.Enabled, upd.Enabled, smtpOf(t, upd).Host))
	})

	// Ayarı eksik kanal açılamaz ("ayar gerekli").
	var fe *FieldError
	if _, err := c.Update(ctx, &admin, "email", ChannelPatch{Enabled: ptr(true)}); !errors.As(err, &fe) || fe.Field != "enabled" {
		t.Fatalf("enabling without settings: err=%v", err)
	}
	for name, tc := range map[string]struct {
		p     ChannelPatch
		field Field
	}{
		"port range":     {ChannelPatch{Config: json.RawMessage(`{"port": 70000}`)}, "config.port"},
		"port type":      {ChannelPatch{Config: json.RawMessage(`{"port": "587"}`)}, "config"},
		"unknown key":    {ChannelPatch{Config: json.RawMessage(`{"hots": "x"}`)}, "config"},
		"not an object":  {ChannelPatch{Config: json.RawMessage(`[1]`)}, "config"},
		"host with url":  {ChannelPatch{Config: json.RawMessage(`{"host": "smtp://x.test"}`)}, "config.host"},
		"from":           {ChannelPatch{Config: json.RawMessage(`{"from": "HealthBeat <hb@x.test>"}`)}, "config.from"},
		"owner level":    {ChannelPatch{OwnerMinLevel: ptr("loud")}, "owner_min_level"},
		"secret newline": {ChannelPatch{Secret: ptr("a\r\nRCPT TO:<x>")}, "secret"},
	} {
		if _, err := c.Update(ctx, &admin, "email", tc.p); !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err=%v, want a FieldError on %s", name, err, tc.field)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("a rejected change notified subscribers: %v", seen)
	}
	if _, err := c.Update(ctx, &admin, "pigeon", ChannelPatch{Enabled: ptr(true)}); !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("unknown channel: err=%v", err)
	}
	if _, err := c.Update(ctx, &admin, "email", ChannelPatch{}); !errors.Is(err, ErrNothingToChange) {
		t.Fatalf("empty patch: err=%v", err)
	}

	// Ayar ve şifre birlikte verilip kanal açılır; ayar olağan biçime getirilir, şifre değişiklik listesinde görünmez.
	changes, err := c.Update(ctx, &admin, "email", ChannelPatch{
		Enabled: ptr(true),
		Config:  json.RawMessage(`{"host": " smtp.x.test ", "username": "hb", "from": "HB@X.test"}`),
		Secret:  ptr("gizli"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(changes), "gizli") {
		t.Fatalf("changes leak the secret: %v", changes)
	}
	want := Changes{
		"enabled":         {Old: false, New: true},
		"config.host":     {Old: "", New: "smtp.x.test"},
		"config.username": {Old: "", New: "hb"},
		"config.from":     {Old: "", New: "hb@x.test"},
		"secret":          {Old: false, New: true},
	}
	if fmt.Sprint(changes) != fmt.Sprint(want) {
		t.Fatalf("changes = %v\nwant      %v", changes, want)
	}
	email, _ := c.Get("email")
	if cfg := smtpOf(t, email); cfg != (SMTPConfig{Host: "smtp.x.test", Port: 587, Username: "hb", From: "hb@x.test"}) || email.Secret != "gizli" {
		t.Fatalf("email channel = %+v %+v", cfg, email)
	}
	if db, _ := st.Get(ctx, "email"); !db.Enabled || db.Secret != "gizli" {
		t.Fatalf("database = %+v", db)
	}
	if strings.Join(seen, ",") != "false->true smtp.x.test" {
		t.Fatalf("subscriber calls = %v", seen)
	}
	if got := MailerConfig(email); got != (notify.Config{Host: "smtp.x.test", Port: "587", Username: "hb", Password: "gizli", From: "hb@x.test"}) {
		t.Fatalf("mailer config = %+v", got)
	}

	// Verilmeyen config anahtarları korunur; aynı değerler değişiklik sayılmaz.
	if changes, err := c.Update(ctx, &admin, "email", ChannelPatch{Config: json.RawMessage(`{"port": 465}`)}); err != nil ||
		fmt.Sprint(changes) != fmt.Sprint(Changes{"config.port": {Old: float64(587), New: float64(465)}}) {
		t.Fatalf("port only: changes=%v err=%v", changes, err)
	}
	if changes, err := c.Update(ctx, &admin, "email", ChannelPatch{Enabled: ptr(true)}); err != nil || len(changes) != 0 {
		t.Fatalf("no-op: changes=%v err=%v", changes, err)
	}

	// Açık kanalın gerekli ayarı boşaltılamaz; kapatılınca göndericinin ayarı boşalır (yalnızca log).
	if _, err := c.Update(ctx, &admin, "email", ChannelPatch{Config: json.RawMessage(`{"host": ""}`)}); !errors.As(err, &fe) || fe.Field != "enabled" {
		t.Fatalf("clearing the host of an enabled channel: err=%v", err)
	}
	if _, err := c.Update(ctx, &admin, "email", ChannelPatch{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	email, _ = c.Get("email")
	if got := MailerConfig(email); got != (notify.Config{}) {
		t.Fatalf("mailer config of a disabled channel = %+v, want empty", got)
	}
}

// Deneme gönderimi kayıtlı ayarla, kanal kapalıyken de yapılır; başarılıysa kanal doğrulanmış sayılır.
func TestChannelTestSendsAndMarksVerified(t *testing.T) {
	ctx := context.Background()
	c, _, mailer, admin := newChannels(t)
	srv := testsmtp.Start(t)
	port := 0
	fmt.Sscan(srv.Port, &port)

	var fe *FieldError
	if err := c.Test(ctx, "email", "a@x.test"); !errors.As(err, &fe) || fe.Field != "enabled" {
		t.Fatalf("test without settings: err=%v, want 'ayar gerekli'", err)
	}
	if _, err := c.Update(ctx, &admin, "email", ChannelPatch{
		Config: json.RawMessage(fmt.Sprintf(`{"host": %q, "port": %d, "from": "hb@x.test"}`, srv.Host, port)),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Test(ctx, "email", "Ali <ali@x.test>"); !errors.As(err, &fe) || fe.Field != "to" {
		t.Fatalf("bad recipient: err=%v", err)
	}
	if err := c.Test(ctx, "email", "Ali@X.test"); err != nil {
		t.Fatalf("test send: %v", err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 || strings.Join(msgs[0].To, ",") != "ali@x.test" || !strings.Contains(msgs[0].Text(), "Deneme") {
		t.Fatalf("test messages = %+v", msgs)
	}
	if email, _ := c.Get("email"); email.VerifiedAt == nil || email.Enabled {
		t.Fatalf("after the test: verified=%v enabled=%v, want verified and still disabled", email.VerifiedAt, email.Enabled)
	}
	if mailer.Enabled() {
		t.Fatal("a test send enabled the mailer")
	}

	// Ulaşılamayan sunucu: hata döner, doğrulama sıfırlanmış kalır.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := c.Update(ctx, &admin, "email", ChannelPatch{Config: json.RawMessage(fmt.Sprintf(`{"port": %d}`, deadPort))}); err != nil {
		t.Fatal(err)
	}
	if err := c.Test(ctx, "email", "ali@x.test"); err == nil || errors.As(err, &fe) {
		t.Fatalf("test against a closed port: err=%v, want the connection error", err)
	}
	if email, _ := c.Get("email"); email.VerifiedAt != nil {
		t.Fatal("a failed test left the channel verified")
	}
}
