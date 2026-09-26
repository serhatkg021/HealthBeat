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

// Notifier, bir bildirim kanalıdır (docs/MIMARI.md bölüm 8). Alert motoru alıcıları kanala göre gruplar ve her grubu o
// kanalın Notifier'ına verir; yeni bir kanal (sms, slack…) yeni bir Notifier'dır.
type Notifier interface {
	// Channel, kanalın adıdır; notification_routes.channel değerleriyle aynıdır (model.Channel*).
	Channel() string
	// Send, msg'yi recipients'a (kanalın adres biçiminde: e-posta adresi, telefon…) iletir; ctx süreyi sınırlar.
	Send(ctx context.Context, recipients []string, msg Message) error
}

// EmailChannel, Mailer'ı Notifier olarak sunar.
type EmailChannel struct {
	Mailer *Mailer
}

func (EmailChannel) Channel() string { return model.ChannelEmail }

func (c EmailChannel) Send(ctx context.Context, recipients []string, msg Message) error {
	return c.Mailer.Send(ctx, recipients, msg.Subject, msg.Body)
}
