package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestNotificationChannelsSaveAndSecrets(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	s := store.NewNotificationChannels(pool, testdb.SecretBox(t))
	admin := testdb.User(t, pool, "admin@x.test", "super_admin", "pw")
	str := func(v string) *string { return &v }

	email, err := s.Get(ctx, "email")
	if err != nil {
		t.Fatal(err)
	}
	if email.Provider != "smtp" || email.Enabled || email.SecretSet || email.OwnerMinLevel != "warning" || email.VerifiedAt != nil {
		t.Fatalf("seeded email channel = %+v", email)
	}
	if _, err := s.Get(ctx, "pigeon"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown channel: err=%v", err)
	}

	// Şifre yazılır, veritabanında açık metin olarak durmaz ve okununca çözülür.
	email.Config = json.RawMessage(`{"host": "smtp.x.test", "port": 587, "from": "hb@x.test", "username": "hb"}`)
	saved, err := s.Save(ctx, &admin, email, str("gizli-şifre"))
	if err != nil {
		t.Fatal(err)
	}
	if !saved.SecretSet || saved.Secret != "gizli-şifre" || saved.UpdatedBy == nil || *saved.UpdatedBy != admin {
		t.Fatalf("after setting the secret: %+v", saved)
	}
	var raw string
	if err := pool.QueryRow(ctx, `SELECT secret_enc FROM notification_channels WHERE channel = 'email'`).Scan(&raw); err != nil || raw == "gizli-şifre" {
		t.Fatalf("stored secret = %q err=%v, want it sealed", raw, err)
	}

	// Doğrulama: yalnızca açma/kapama ya da sahip seviyesi değişirse korunur; ayar ya da şifre değişince sıfırlanır.
	if _, err := s.MarkVerified(ctx, "email"); err != nil {
		t.Fatal(err)
	}
	saved.Enabled, saved.OwnerMinLevel = true, "critical"
	if saved, err = s.Save(ctx, &admin, saved, nil); err != nil || saved.VerifiedAt == nil || saved.Secret != "gizli-şifre" {
		t.Fatalf("enable only: verified=%v secret kept=%v err=%v", saved.VerifiedAt, saved.Secret == "gizli-şifre", err)
	}
	saved.Config = json.RawMessage(`{"host": "smtp2.x.test", "port": 587, "from": "hb@x.test", "username": "hb"}`)
	if saved, err = s.Save(ctx, &admin, saved, nil); err != nil || saved.VerifiedAt != nil {
		t.Fatalf("config change: verified=%v err=%v, want it cleared", saved.VerifiedAt, err)
	}
	if _, err := s.MarkVerified(ctx, "email"); err != nil {
		t.Fatal(err)
	}
	if saved, err = s.Save(ctx, &admin, saved, str("yeni-şifre")); err != nil || saved.VerifiedAt != nil || saved.Secret != "yeni-şifre" {
		t.Fatalf("secret change: %+v err=%v, want the new secret and verification cleared", saved, err)
	}

	// Boş şifre siler.
	if saved, err = s.Save(ctx, &admin, saved, str("")); err != nil || saved.SecretSet || saved.Secret != "" {
		t.Fatalf("clearing the secret: %+v err=%v", saved, err)
	}

	// CHECK ihlali ErrSettingsInvalid olur.
	saved.OwnerMinLevel = "loud"
	if _, err := s.Save(ctx, nil, saved, nil); !errors.Is(err, store.ErrSettingsInvalid) {
		t.Fatalf("invalid owner level: err=%v", err)
	}
}

// Şifreli değer kanal adına bağlıdır: başka bir kanalın satırına kopyalanırsa çözülemez.
func TestNotificationChannelSecretIsBoundToItsChannel(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	s := store.NewNotificationChannels(pool, testdb.SecretBox(t))
	email, _ := s.Get(ctx, "email")
	secret := "gizli"
	if _, err := s.Save(ctx, nil, email, &secret); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notification_channels (channel, provider, secret_enc)
		SELECT 'sms', 'test', secret_enc FROM notification_channels WHERE channel = 'email'`); err != nil {
		t.Fatal(err)
	}
	if sms, err := s.Get(ctx, "sms"); err != nil || !sms.SecretUnreadable || sms.SecretSet || sms.Secret != "" {
		t.Fatalf("a secret copied from another channel: %+v err=%v, want it unreadable", sms, err)
	}
	if got, err := s.Get(ctx, "email"); err != nil || got.Secret != "gizli" {
		t.Fatalf("email after the copy: %+v err=%v", got, err)
	}
}

// Anahtar değişince kayıtlı şifre çözülemez: kanal yine okunur (server açılabilsin), işaretlenir ve yeni şifre
// kaydedilince ya da şifre silinince işaret kalkar.
func TestNotificationChannelWithAnUnreadableSecretIsStillReadable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	str := func(v string) *string { return &v }

	old := store.NewNotificationChannels(pool, testdb.SecretBox(t))
	email, _ := old.Get(ctx, "email")
	email.Config = json.RawMessage(`{"host": "smtp.x.test", "port": 587, "from": "hb@x.test", "username": "hb"}`)
	if _, err := old.Save(ctx, nil, email, str("eski-anahtarla")); err != nil {
		t.Fatal(err)
	}

	box, err := secretbox.New([]byte("ffffffffffffffffffffffffffffffff"))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewNotificationChannels(pool, box)
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list with a changed key: %v", err)
	}
	var got *model.NotificationChannel
	for i := range list {
		if list[i].Channel == "email" {
			got = &list[i]
		}
	}
	if got == nil || !got.SecretUnreadable || got.SecretSet || got.Secret != "" {
		t.Fatalf("email with a changed key = %+v, want it flagged and without a secret", got)
	}

	// Şifreye dokunmayan kayıt çalışır ve işaret kalır.
	got.OwnerMinLevel = "critical"
	saved, err := s.Save(ctx, nil, *got, nil)
	if err != nil || !saved.SecretUnreadable || saved.OwnerMinLevel != "critical" {
		t.Fatalf("saving without touching the secret: %+v err=%v", saved, err)
	}
	if saved, err = s.Save(ctx, nil, saved, str("yeni-anahtarla")); err != nil || saved.SecretUnreadable || saved.Secret != "yeni-anahtarla" {
		t.Fatalf("entering the secret again: %+v err=%v", saved, err)
	}

	// Silmek de işareti kaldırır.
	if _, err := old.Save(ctx, nil, saved, str("yine-eski")); err != nil {
		t.Fatal(err)
	}
	if saved, err = s.Save(ctx, nil, saved, str("")); err != nil || saved.SecretUnreadable || saved.SecretSet {
		t.Fatalf("clearing an unreadable secret: %+v err=%v", saved, err)
	}
}
