package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
)

// ErrUnknownChannel, tanımlı olmayan bir kanal adı verildiğinde döner.
var ErrUnknownChannel = errors.New("unknown notification channel")

// SMTPConfig, e-posta kanalının (provider "smtp") sır olmayan ayarıdır; şifre kanalın secret'ıdır.
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	From     string `json:"from"`
}

// ChannelPatch, bir kanal değişikliğidir: nil alan "dokunma" demektir. Config'te verilen anahtarlar mevcut ayarın üstüne
// yazılır (verilmeyenler korunur). Secret "" ise şifre silinir.
type ChannelPatch struct {
	Enabled       *bool           `json:"enabled"`
	Config        json.RawMessage `json:"config"`
	Secret        *string         `json:"secret"`
	OwnerMinLevel *string         `json:"owner_min_level"`
}

// Channels, bildirim kanallarının bellekteki kopyasını tutar ve değişiklikleri doğrulayıp yazar (bkz. Service). Kanal
// değişince abonelere (ör. e-posta göndericisi) haber verilir; yeniden başlatma gerekmez.
type Channels struct {
	store  *store.NotificationChannels
	mailer *notify.Mailer // deneme gönderimleri için

	cur atomic.Pointer[map[string]model.NotificationChannel]

	mu   sync.Mutex // yazmaları sıraya koyar (bkz. Service.mu)
	subs []func(old, updated model.NotificationChannel)
}

// NewChannels, kanalları veritabanından okur. mailer, e-posta kanalının deneme gönderimlerinde kullanılır.
func NewChannels(ctx context.Context, st *store.NotificationChannels, mailer *notify.Mailer) (*Channels, error) {
	list, err := st.List(ctx)
	if err != nil {
		return nil, err
	}
	c := &Channels{store: st, mailer: mailer}
	all := make(map[string]model.NotificationChannel, len(list))
	for _, ch := range list {
		all[ch.Channel] = ch
	}
	c.cur.Store(&all)
	return c, nil
}

// Get, bir kanalın güncel hâlidir (kilitsiz).
func (c *Channels) Get(channel string) (model.NotificationChannel, bool) {
	ch, ok := (*c.cur.Load())[channel]
	return ch, ok
}

// List, bütün kanallardır (ada göre sıralı).
func (c *Channels) List() []model.NotificationChannel {
	all := *c.cur.Load()
	out := make([]model.NotificationChannel, 0, len(all))
	for _, name := range slices.Sorted(maps.Keys(all)) {
		out = append(out, all[name])
	}
	return out
}

// RuleCounts, her kanala bağlı bildirim kuralı sayısıdır.
func (c *Channels) RuleCounts(ctx context.Context) (map[string]int, error) {
	return c.store.RuleCounts(ctx)
}

// OnChange, her başarılı kanal değişikliğinden sonra çağrılacak bir abone ekler (bkz. Service.OnChange).
func (c *Channels) OnChange(fn func(old, updated model.NotificationChannel)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs = append(c.subs, fn)
}

// Update, kanalı doğrular ve yazar. Geçersiz bir değer *FieldError döndürür ve hiçbir şey yazılmaz. Dönen değişiklik
// listesinde şifrenin kendisi yer almaz (yalnızca ayarlı olup olmadığı).
func (c *Channels) Update(ctx context.Context, actor *uuid.UUID, channel string, p ChannelPatch) (Changes, error) {
	if p.Enabled == nil && p.Config == nil && p.Secret == nil && p.OwnerMinLevel == nil {
		return nil, ErrNothingToChange
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	old, ok := c.Get(channel)
	if !ok {
		return nil, ErrUnknownChannel
	}
	next := old
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	if p.OwnerMinLevel != nil {
		level := strings.TrimSpace(*p.OwnerMinLevel)
		if !model.ValidAlertLevel(level) {
			return nil, &FieldError{"owner_min_level", "info, warning ya da critical olmalı"}
		}
		next.OwnerMinLevel = level
	}
	if p.Secret != nil {
		if strings.ContainsAny(*p.Secret, "\r\n") {
			return nil, &FieldError{"secret", "satır sonu içeremez"}
		}
		next.Secret, next.SecretSet = *p.Secret, *p.Secret != ""
	}
	config, err := mergeConfig(old.Config, p.Config)
	if err != nil {
		return nil, err
	}
	if next.Config, err = normalizeConfig(next.Provider, config); err != nil {
		return nil, err
	}
	if next.Enabled {
		if err := readyToSend(next); err != nil {
			return nil, err
		}
	}

	// Karşılaştırma olağan biçimler arasında yapılır: migration'ın yazdığı eksik ayar ({"port": 587}) ilk kayıtta
	// tamamlanır, bu bir değişiklik sayılmaz.
	before := old
	if m, err := mergeConfig(old.Config, nil); err == nil {
		if n, err := normalizeConfig(old.Provider, m); err == nil {
			before.Config = n
		}
	}
	changes := channelChanges(before, next, p.Secret != nil)
	if len(changes) == 0 {
		return changes, nil
	}
	updated, err := c.store.Save(ctx, actor, next, p.Secret)
	if err != nil {
		return nil, err
	}
	c.remember(updated)
	for _, fn := range c.subs {
		fn(old, updated)
	}
	for _, f := range slices.Sorted(maps.Keys(changes)) {
		slog.InfoContext(ctx, "notification channel: changed", "channel", channel, "setting", string(f),
			"old", changes[f].Old, "new", changes[f].New)
	}
	return changes, nil
}

// Test, kanalın kayıtlı ayarıyla to adresine hemen (kuyruğa girmeden) bir deneme iletisi gönderir; kanal kapalıyken de
// çalışır, böylece açmadan önce denenebilir. Başarılıysa kanal doğrulanmış (verified_at) sayılır. Gönderim hatası
// olduğu gibi döner (SMTP'nin yanıtı yöneticiye sorunu söyler).
func (c *Channels) Test(ctx context.Context, channel, to string) error {
	ch, ok := c.Get(channel)
	if !ok {
		return ErrUnknownChannel
	}
	if ch.Provider != "smtp" {
		return fmt.Errorf("%w: %s cannot be tested", ErrUnknownChannel, channel)
	}
	addr, err := model.NormalizeEmail(to)
	if err != nil {
		return &FieldError{"to", err.Error()}
	}
	if err := readyToSend(ch); err != nil {
		return err
	}
	if err := c.mailer.SendWith(ctx, smtpNotifyConfig(ch), []string{addr}, "[HealthBeat] Deneme e-postası",
		"Bu ileti HealthBeat panelindeki e-posta kanalı ayarını denemek için gönderildi. Aldıysanız ayar çalışıyor.\n"); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	updated, err := c.store.MarkVerified(ctx, channel)
	if err != nil {
		return err
	}
	c.remember(updated)
	return nil
}

// MailerConfig, e-posta kanalının göndericiye verilecek ayarıdır: kanal kapalıysa boş (gönderim yalnızca loglanır).
func MailerConfig(ch model.NotificationChannel) notify.Config {
	if !ch.Enabled {
		return notify.Config{}
	}
	return smtpNotifyConfig(ch)
}

func smtpNotifyConfig(ch model.NotificationChannel) notify.Config {
	var cfg SMTPConfig
	_ = json.Unmarshal(ch.Config, &cfg) // normalizeConfig'ten geçmiş ayar
	return notify.Config{Host: cfg.Host, Port: strconv.Itoa(cfg.Port), Username: cfg.Username, Password: ch.Secret, From: cfg.From}
}

// remember, yazılmış bir kanalı bellekteki kopyaya koyar (c.mu altında).
func (c *Channels) remember(ch model.NotificationChannel) {
	all := maps.Clone(*c.cur.Load())
	all[ch.Channel] = ch
	c.cur.Store(&all)
}

// mergeConfig, patch'teki anahtarları mevcut ayarın üstüne yazar.
func mergeConfig(current, patch json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if err := json.Unmarshal(current, &out); err != nil {
		return nil, err
	}
	if patch == nil {
		return out, nil
	}
	var p map[string]any
	if err := json.Unmarshal(patch, &p); err != nil || p == nil {
		return nil, &FieldError{"config", "bir nesne olmalı"}
	}
	maps.Copy(out, p)
	return out, nil
}

// normalizeConfig, bir kanalın ayarını provider'ının kurallarıyla doğrular ve olağan biçimde döndürür.
func normalizeConfig(provider string, config map[string]any) (json.RawMessage, error) {
	switch provider {
	case "smtp":
		raw, _ := json.Marshal(config)
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var cfg SMTPConfig
		if err := dec.Decode(&cfg); err != nil {
			return nil, &FieldError{"config", "tanınmayan ya da yanlış türde bir alan var (host, port, username, from)"}
		}
		cfg.Host = strings.TrimSpace(cfg.Host)
		cfg.Username = strings.TrimSpace(cfg.Username)
		cfg.From = strings.TrimSpace(cfg.From)
		if cfg.Host != "" && strings.ContainsAny(cfg.Host, " /:\t\r\n@") {
			return nil, &FieldError{"config.host", "yalnızca sunucu adı ya da IP olmalı (ör. smtp.example.com)"}
		}
		if cfg.Port < 1 || cfg.Port > 65535 {
			return nil, &FieldError{"config.port", "1 ile 65535 arasında olmalı"}
		}
		if strings.ContainsAny(cfg.Username, "\r\n") {
			return nil, &FieldError{"config.username", "satır sonu içeremez"}
		}
		if cfg.From != "" {
			from, err := model.NormalizeEmail(cfg.From)
			if err != nil {
				return nil, &FieldError{"config.from", err.Error()}
			}
			cfg.From = from
		}
		out, err := json.Marshal(cfg)
		return out, err
	}
	return nil, fmt.Errorf("unknown notification provider %q", provider)
}

// Ready, kanalın ayarının gönderim için yeterli olup olmadığıdır (değilse panel "Ayar gerekli" gösterir).
func Ready(ch model.NotificationChannel) bool { return readyToSend(ch) == nil }

// DisplayConfig, kanalın ayarını bütün alanlarıyla (olağan biçimde) döndürür; migration'ın yazdığı eksik ayar
// ({"port": 587}) boş alanlarla tamamlanır.
func DisplayConfig(ch model.NotificationChannel) json.RawMessage {
	if m, err := mergeConfig(ch.Config, nil); err == nil {
		if n, err := normalizeConfig(ch.Provider, m); err == nil {
			return n
		}
	}
	return ch.Config
}

// readyToSend, açık bir kanalın gönderim için gereken ayarlarının dolu olup olmadığını denetler.
func readyToSend(ch model.NotificationChannel) error {
	if ch.Provider == "smtp" {
		var cfg SMTPConfig
		_ = json.Unmarshal(ch.Config, &cfg)
		if cfg.Host == "" || cfg.From == "" {
			return &FieldError{"enabled", "ayar gerekli: SMTP sunucusu ve gönderen adresi girilmeli"}
		}
	}
	return nil
}

// channelChanges, bir kanal değişikliğinde değişen alanlardır ("config.<anahtar>" biçiminde). Şifrenin kendisi yazılmaz:
// verildiyse eski ve yeni hâli yalnızca "ayarlı mı" olarak görünür.
func channelChanges(old, next model.NotificationChannel, secretGiven bool) Changes {
	changes := Changes{}
	if old.Enabled != next.Enabled {
		changes["enabled"] = Change{Old: old.Enabled, New: next.Enabled}
	}
	if old.OwnerMinLevel != next.OwnerMinLevel {
		changes["owner_min_level"] = Change{Old: old.OwnerMinLevel, New: next.OwnerMinLevel}
	}
	var before, after map[string]any
	_ = json.Unmarshal(old.Config, &before)
	_ = json.Unmarshal(next.Config, &after)
	for k, v := range after {
		if fmt.Sprint(before[k]) != fmt.Sprint(v) {
			changes[Field("config."+k)] = Change{Old: before[k], New: v}
		}
	}
	if secretGiven && (old.Secret != next.Secret || old.SecretSet != next.SecretSet) {
		changes["secret"] = Change{Old: old.SecretSet, New: next.SecretSet}
	}
	return changes
}
