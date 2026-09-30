package notify

import (
	"context"

	"healthbeat-server/internal/model"
)

// Message, bir bildirimin kanaldan bağımsız içeriğidir. Kanal onu kendi biçimine çevirir (e-postada konu ve düz metin
// gövde).
type Message struct {
	Subject string
	Body    string
}

// Notifier, bir bildirim kanalıdır (docs/MIMARI.md bölüm 8). Her ileti tek alıcıya gider: kişiye giden kanallarda
// alıcılar birbirini görmez (her alıcı kuyrukta ayrı bir satırdır). Yeni bir kanal (sms, slack…) yeni bir Notifier'dır.
type Notifier interface {
	// Channel, kanalın adıdır; notification_routes.channel değerleriyle aynıdır (model.Channel*).
	Channel() string
	// Personal, kanalın kişiye (e-posta adresi, telefon) mi yoksa ortak bir hedefe (Slack kanalı gibi) mi gittiğidir.
	// Organizasyon/sunucu kurallarında yalnızca kişiye giden kanallar seçilebilir; ortak kanallar yalnızca sistem
	// sahibine gider.
	Personal() bool
	// Send, msg'yi recipient'a (kanalın adres biçiminde: e-posta adresi, telefon…) iletir; ctx süreyi sınırlar.
	Send(ctx context.Context, recipient string, msg Message) error
}

// EmailChannel, Mailer'ı Notifier olarak sunar.
type EmailChannel struct {
	Mailer *Mailer
}

func (EmailChannel) Channel() string { return model.ChannelEmail }

func (EmailChannel) Personal() bool { return true }

func (c EmailChannel) Send(ctx context.Context, recipient string, msg Message) error {
	return c.Mailer.Send(ctx, []string{recipient}, msg.Subject, msg.Body)
}
