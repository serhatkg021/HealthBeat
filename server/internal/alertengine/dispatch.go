package alertengine

// Bildirimler: alert açıldığında, seviyesi değiştiğinde ya da çözüldüğünde bildirim satırları (alıcı başına bir satır:
// alıcılar birbirini görmez) değişiklikle AYNI transaction'da bildirim kuyruğuna (notification_outbox) yazılır; teslimi outbox.Worker yapar
// (yeniden deneme, geri çekilme). Server yeniden başlasa da bekleyen bildirim kaybolmaz.
//
// Alıcılar ve metnin sunucu bağlamı transaction'dan ÖNCE, havuzdan okunur (prepare): transaction yalnızca kendi
// bağlantısını kullanır; aksi halde eşzamanlı alert değişiklikleri havuzu tüketip birbirini bekleyebilirdi. Hazırlık
// yalnızca bildirim doğuracak bir değişiklik yapılacakken çalışır; sıradan bir rapor ek sorgu üretmez.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// pending, bir alert değişikliğinin bildirimi için önceden okunanlardır. recipients boşsa bildirim yazılmaz (alıcı yok
// ya da hiçbirinin kanalı gönderemiyor).
type pending struct {
	recipients []store.Recipient
	mc         messageContext
}

// prepare, hostID'deki level seviyesindeki bir alert'in alıcılarını ve bildirim metninin sunucu bağlamını okur. Kanalı
// kapalı ya da henüz uygulanmamış alıcılar atlanır ve loglanır. Alıcılar okunamazsa hata loglanır ve bildirimsiz devam
// edilir: alert yine kaydedilir ve panelde görünür.
func (e *Engine) prepare(ctx context.Context, hostID, orgID uuid.UUID, level string) pending {
	recipients, err := e.notifs.ResolveRecipients(ctx, hostID, orgID, level)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: resolve recipients", "host_id", hostID.String(), "err", err)
		return pending{}
	}
	var p pending
	for _, r := range recipients {
		switch _, implemented := e.notifiers[r.Channel]; {
		case r.ChannelOff:
			slog.WarnContext(ctx, "alert engine: notification channel is off, skipping rule", "channel", r.Channel, "recipient", r.Name, "host_id", hostID.String())
		case !implemented:
			slog.WarnContext(ctx, "alert engine: channel is not implemented, skipping recipient", "channel", r.Channel, "recipient", r.Name, "host_id", hostID.String())
		default:
			p.recipients = append(p.recipients, r)
		}
	}
	if len(p.recipients) > 0 {
		p.mc = e.messageContext(ctx, hostID, orgID)
	}
	return p
}

// enqueue, alert'in event olayının bildirimini tx içinde alıcı başına bir satır olarak kuyruğa yazar: her alıcı ayrı
// ileti alır, biri başarısız olursa yalnızca o yeniden denenir.
func (e *Engine) enqueue(ctx context.Context, tx Tx, p pending, event string, alert model.Alert) error {
	if len(p.recipients) == 0 {
		return nil
	}
	msg := buildMessage(alert, p.mc, *e.panelBaseURL.Load())
	alertID := alert.ID
	for _, r := range p.recipients {
		if _, err := tx.Outbox().Enqueue(ctx, store.OutboxMessage{
			Kind: store.OutboxKindAlert, Channel: r.Channel, Recipients: []string{r.Address},
			Subject: msg.Subject, Body: msg.Body, AlertID: &alertID, AlertEvent: event, AlertLevel: alert.Level,
			RequestID: logging.RequestID(ctx),
		}); err != nil {
			return fmt.Errorf("enqueue %s notification: %w", r.Channel, err)
		}
	}
	return nil
}

// change, bildirim doğurabilecek bir alert değişikliğini (fn) ve event olayının bildirimini tek transaction'da yapar.
// fn değişen alert'i ve bildirilip bildirilmeyeceğini döndürür. Commit'ten sonra işçi uyandırılır.
//
// Alert kaydı esastır: bildirim kuyruğa yazılamazsa (ör. o anki bir veritabanı hatası) yalnızca bildirim geri alınır,
// alert değişikliği yine kaydedilir ve ERROR loglanır. Alıcı o olayın e-postasını almaz ama sonraki bildirim (seviye
// değişimi, çözülme) normal gider; ayrıntı panelde görülür.
func (e *Engine) change(ctx context.Context, p pending, event string, fn func(tx Tx) (model.Alert, bool, error)) error {
	notified := false
	err := e.tx.InTx(ctx, func(tx Tx) error {
		alert, notify, err := fn(tx)
		if err != nil || !notify || len(p.recipients) == 0 {
			return err
		}
		if err := tx.Savepoint(ctx, func(sp Tx) error { return e.enqueue(ctx, sp, p, event, alert) }); err != nil {
			slog.ErrorContext(ctx, "alert engine: notification could not be queued; the alert is recorded without it",
				"alert_id", alert.ID.String(), "host_id", alert.HostID.String(), "err", err)
			return nil
		}
		notified = true
		return nil
	})
	if err == nil && notified && e.worker != nil {
		e.worker.Wake()
	}
	return err
}

// resolveAndNotify bir alert'i çözer ve gerçekten değiştiyse (daha önce zaten çözülmemişse) "çözüldü" bildirimini
// kuyruğa yazar. Eşzamanlı bir çağrı önce davranmışsa (store.ErrNotFound) sessizce döner — aynı çözülme için ikinci
// bir bildirim gitmesin diye. value/threshold, store.Alerts.Resolve'a olduğu gibi geçer: eşik-tabanlı çözülmede
// güncel okumayı taşır, diğer yollarda nil'dir.
func (e *Engine) resolveAndNotify(ctx context.Context, active model.Alert, orgID uuid.UUID, value, threshold *float64) {
	p := e.prepare(ctx, active.HostID, orgID, active.Level)
	err := e.change(ctx, p, store.AlertEventResolved, func(tx Tx) (model.Alert, bool, error) {
		alert, err := tx.Alerts().Resolve(ctx, active.ID, value, threshold)
		if errors.Is(err, store.ErrNotFound) {
			return model.Alert{}, false, nil
		}
		return alert, err == nil, err
	})
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: resolve alert", "alert_id", active.ID.String(), "err", err)
	}
}

// messageContext, bildirimin sunucu bağlamını okur: 150 sunucu arasında hangisi olduğu yalnızca "Title"tan
// anlaşılmaz (aynı ad birden çok müşteride tekrar edebilir) — organizasyon, IP ve makinenin kendi hostname'i de gerekir.
// Okunamayan alanlar kimlikle ya da "—" ile doldurulur: bağlam eksik diye bildirim düşmez.
func (e *Engine) messageContext(ctx context.Context, hostID, orgID uuid.UUID) messageContext {
	mc := messageContext{OrgName: orgID.String(), HostTitle: hostID.String(), Hostname: "—"}
	if host, err := e.hosts.GetByID(ctx, hostID); err == nil {
		mc.HostTitle, mc.HostIP = host.Title, host.IP
		if host.HostInfo != nil && host.HostInfo.Hostname != "" {
			mc.Hostname = host.HostInfo.Hostname
		}
	}
	if org, err := e.organizations.GetByID(ctx, orgID); err == nil {
		mc.OrgName = org.Name
	}
	return mc
}

// Flush, şu an teslim zamanı gelmiş alert bildirimlerini teslim etmeyi dener ve arka planda sürmekte olan bir teslim
// turunun bitmesini bekler. Testler içindir; üretimde teslimi RunNotifications yapar.
func (e *Engine) Flush() {
	if e.worker == nil {
		return
	}
	if _, err := e.worker.DeliverDue(context.Background()); err != nil {
		slog.Error("alert engine: flush notifications", "err", err)
	}
}

// RunNotifications, ctx bitene kadar alert bildirimlerini kuyruktan teslim eder (bkz. outbox.Worker). Kendi
// goroutine'inde çalıştırın.
func (e *Engine) RunNotifications(ctx context.Context) {
	if e.worker != nil {
		e.worker.Run(ctx)
	}
}

// Close, kapanışta teslim zamanı gelmiş bildirimleri ctx süresi içinde göndermeyi dener. Gönderilemeyenler kaybolmaz:
// kuyrukta kalır ve bir sonraki açılışta gönderilir.
func (e *Engine) Close(ctx context.Context) error {
	if e.worker == nil {
		return nil
	}
	_, err := e.worker.DeliverDue(ctx)
	return err
}
