package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/settings"
)

// Sistem ayarları (Ayarlar sayfası): çalışma zamanı ayarları ve bildirim kanalları. Okumak
// settings.view, değiştirmek settings.manage ister (varsayılan olarak yalnızca super_admin). Her değişiklik denetim
// kaydına yazılır; kanal şifresi hiçbir yanıtta ya da kayıtta yer almaz.

// errorCodeChannelTestFailed, deneme gönderiminin kanalın sunucusunda başarısız olduğunu söyler (ayar yanlış olabilir).
const errorCodeChannelTestFailed = "channel_test_failed"

// SetSettings, ayar ve kanal servislerini bağlar (main.go); kural API'si kanalların durumunu buradan okur.
func (d *Deps) SetSettings(appSettings *settings.Service, channels *settings.Channels) {
	d.appSettings, d.channels = appSettings, channels
}

// fieldError, ayar doğrulama hatasını alanı adlandıran bir 400'e çevirir; başka bir hata beklenmeyendir: fail(op) ile
// loglanıp 500 olur.
func fieldError(err error, fail failFunc, op string) error {
	var fe *settings.FieldError
	if errors.As(err, &fe) {
		e := newError(http.StatusBadRequest, fe.Message)
		e.Fields = map[string]string{string(fe.Field): fe.Message}
		return e
	}
	return fail(op, err)
}

type userRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// userRefOf, bir kaydı değiştiren kullanıcıyı gösterim için okur; silinmişse nil.
func (d *Deps) userRefOf(r *http.Request, id *uuid.UUID) *userRef {
	if id == nil {
		return nil
	}
	u, err := d.users.GetByID(r.Context(), *id)
	if err != nil {
		return nil
	}
	name := u.Email
	if u.FullName != nil && *u.FullName != "" {
		name = *u.FullName
	}
	return &userRef{ID: u.ID, Name: name}
}

func actorOf(r *http.Request) *uuid.UUID {
	if id, ok := userIDFromContext(r.Context()); ok {
		return &id
	}
	return nil
}

// ---------------------------------------------------------------- çalışma zamanı ayarları

type settingsResponse struct {
	Values    map[settings.Field]any `json:"values"`
	Defaults  map[settings.Field]any `json:"defaults"`
	Changed   []settings.Field       `json:"changed"` // değeri varsayılanından farklı olanlar
	UpdatedAt time.Time              `json:"updated_at"`
	UpdatedBy *userRef               `json:"updated_by"`
}

func (d *Deps) settingsView(r *http.Request) settingsResponse {
	cur := d.appSettings.Current()
	changed := d.appSettings.Changed()
	if changed == nil {
		changed = []settings.Field{}
	}
	return settingsResponse{
		Values: settings.Values(cur), Defaults: settings.Values(d.appSettings.Defaults()), Changed: changed,
		UpdatedAt: cur.UpdatedAt, UpdatedBy: d.userRefOf(r, cur.UpdatedBy),
	}
}

func (d *Deps) handleGetSettings(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, d.settingsView(r))
	return nil
}

func (d *Deps) handleUpdateSettings(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("ayarlar kaydedilemedi")
	p, err := bind[settings.Patch](r)
	if err != nil {
		return err
	}
	changes, err := d.appSettings.Update(r.Context(), actorOf(r), p)
	if errors.Is(err, settings.ErrNothingToChange) {
		return badRequest("değiştirilecek bir ayar verilmedi")
	}
	if err != nil {
		return fieldError(err, fail, "update settings")
	}
	if len(changes) > 0 {
		d.logAudit(r, "settings.update", "settings", nil, changes)
	}
	writeJSON(w, http.StatusOK, d.settingsView(r))
	return nil
}

type resetSettingsRequest struct {
	Fields []settings.Field `json:"fields"`
}

func (d *Deps) handleResetSettings(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("ayarlar varsayılana döndürülemedi")
	req, err := bind[resetSettingsRequest](r)
	if err != nil {
		return err
	}
	changes, err := d.appSettings.Reset(r.Context(), actorOf(r), req.Fields)
	if errors.Is(err, settings.ErrNothingToChange) {
		return badRequest("varsayılana döndürülecek bir ayar verilmedi")
	}
	if err != nil {
		return fieldError(err, fail, "reset settings")
	}
	if len(changes) > 0 {
		d.logAudit(r, "settings.reset", "settings", nil, changes)
	}
	writeJSON(w, http.StatusOK, d.settingsView(r))
	return nil
}

// ---------------------------------------------------------------- bildirim kanalları

type channelResponse struct {
	Channel   string          `json:"channel"`
	Provider  string          `json:"provider"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
	SecretSet bool            `json:"secret_set"` // şifrenin kendisi asla verilmez
	// SecretUnreadable: kayıtlı şifre bu server'ın anahtarıyla çözülemiyor, yeniden girilmeli (Ready de false olur).
	SecretUnreadable bool       `json:"secret_unreadable"`
	VerifiedAt       *time.Time `json:"verified_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	UpdatedBy        *userRef   `json:"updated_by"`
	// Ready, ayarın gönderim için yeterli olduğunu; Implemented, server'ın bu kanalla gönderebildiğini; Personal,
	// kanalın kişiye gittiğini (yalnızca bunlar organizasyon/sunucu kurallarında seçilebilir) söyler.
	Ready       bool `json:"ready"`
	Implemented bool `json:"implemented"`
	Personal    bool `json:"personal"`
	RuleCount   int  `json:"rule_count"`
}

func (d *Deps) channelView(r *http.Request, ch model.NotificationChannel, ruleCount int) channelResponse {
	personal, implemented := d.alertEngine.Notifier(ch.Channel)
	return channelResponse{
		Channel: ch.Channel, Provider: ch.Provider, Enabled: ch.Enabled, Config: settings.DisplayConfig(ch),
		SecretSet: ch.SecretSet, SecretUnreadable: ch.SecretUnreadable, VerifiedAt: ch.VerifiedAt,
		UpdatedAt: ch.UpdatedAt, UpdatedBy: d.userRefOf(r, ch.UpdatedBy),
		Ready: settings.Ready(ch), Implemented: implemented, Personal: personal, RuleCount: ruleCount,
	}
}

func (d *Deps) handleListChannels(w http.ResponseWriter, r *http.Request) error {
	counts, err := d.channels.RuleCounts(r.Context())
	if err != nil {
		return failWith("bildirim kanalları alınamadı")("list channels: rule counts", err)
	}
	out := []channelResponse{}
	for _, ch := range d.channels.List() {
		out = append(out, d.channelView(r, ch, counts[ch.Channel]))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// channelOption, kural ekranının bir kanal hakkında bilmesi gerekenlerdir (ayrıntısız: config ya da şifre
// yok); notification.view ile okunur.
type channelOption struct {
	Channel     string `json:"channel"`
	Enabled     bool   `json:"enabled"`
	Ready       bool   `json:"ready"`
	Personal    bool   `json:"personal"`
	Implemented bool   `json:"implemented"`
}

func (d *Deps) handleChannelOptions(w http.ResponseWriter, r *http.Request) error {
	out := []channelOption{}
	for _, ch := range d.channels.List() {
		personal, implemented := d.alertEngine.Notifier(ch.Channel)
		out = append(out, channelOption{Channel: ch.Channel, Enabled: ch.Enabled, Ready: settings.Ready(ch), Personal: personal, Implemented: implemented})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// channelFromPath, yoldaki kanalı döndürür; yoksa 404.
func (d *Deps) channelFromPath(r *http.Request) (model.NotificationChannel, error) {
	ch, ok := d.channels.Get(r.PathValue("channel"))
	if !ok {
		return ch, notFound("bildirim kanalı bulunamadı")
	}
	return ch, nil
}

func (d *Deps) handleUpdateChannel(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bildirim kanalı güncellenemedi")
	ch, err := d.channelFromPath(r)
	if err != nil {
		return err
	}
	p, err := bind[settings.ChannelPatch](r)
	if err != nil {
		return err
	}
	changes, err := d.channels.Update(r.Context(), actorOf(r), ch.Channel, p)
	switch {
	case errors.Is(err, settings.ErrNothingToChange):
		return badRequest("değiştirilecek bir alan verilmedi")
	case err != nil:
		return fieldError(err, fail, "update channel")
	}
	if len(changes) > 0 {
		d.logAudit(r, "notification_channel.update", "notification_channel", &ch.Channel, changes)
	}
	counts, err := d.channels.RuleCounts(r.Context())
	if err != nil {
		return fail("update channel: rule counts", err)
	}
	updated, _ := d.channels.Get(ch.Channel)
	writeJSON(w, http.StatusOK, d.channelView(r, updated, counts[ch.Channel]))
	return nil
}

type testChannelRequest struct {
	To string `json:"to"`
}

func (d *Deps) handleTestChannel(w http.ResponseWriter, r *http.Request) error {
	ch, err := d.channelFromPath(r)
	if err != nil {
		return err
	}
	req, err := bind[testChannelRequest](r)
	if err != nil {
		return err
	}
	err = d.channels.Test(r.Context(), ch.Channel, req.To)
	var fe *settings.FieldError
	switch {
	case errors.As(err, &fe):
		return fieldError(err, nil, "")
	case errors.Is(err, settings.ErrUnknownChannel):
		return badRequest("bu kanal denenemiyor")
	case err != nil:
		d.logAudit(r, "notification_channel.test", "notification_channel", &ch.Channel, map[string]any{"to": req.To, "ok": false, "error": err.Error()})
		e := newError(http.StatusBadGateway, "deneme iletisi gönderilemedi: "+err.Error())
		e.Code = errorCodeChannelTestFailed
		return e
	}
	d.logAudit(r, "notification_channel.test", "notification_channel", &ch.Channel, map[string]any{"to": req.To, "ok": true})
	w.WriteHeader(http.StatusNoContent)
	return nil
}
