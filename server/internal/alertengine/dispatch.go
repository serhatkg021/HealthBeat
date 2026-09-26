package alertengine

// Bildirim kuyruğu ve teslimi: alert açıldığında, seviyesi değiştiğinde ya da çözüldüğünde iş kuyruğa alınır; işçiler
// alıcıları çözer, metni kurar (message.go) ve her kanalın Notifier'ına verir.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

type mailJob struct {
	orgID uuid.UUID
	alert model.Alert
	// logInfo, alert'i doğuran isteğin log kimlikleridir (varsa): teslim satırları o isteğin request_id'sini taşır,
	// böylece "e-posta gitmedi" satırı alert'i açan agent raporuna bağlanır.
	logInfo *logging.RequestInfo
}

const (
	defaultMailQueueSize = 256
	defaultMailWorkers   = 2
	// mailDeliveryTimeout, alert başına alıcı aramasını + SMTP oturumunu sınırlar.
	mailDeliveryTimeout = 45 * time.Second
)

func (e *Engine) mailWorker() {
	defer e.workers.Done()
	for job := range e.mailQueue {
		e.deliver(job)
		e.pending.Done()
	}
}

// Flush, şimdiye kadar kuyruğa alınan her bildirim teslim edilene (ya da başarısız olana)
// kadar bloklar. Testler ve düzenli kapanış içindir.
func (e *Engine) Flush() { e.pending.Wait() }

// Close, bildirim kabul etmeyi bırakır, kuyruktakileri teslim eder ve işçileri durdurur;
// ctx bitince vazgeçer. Birden fazla kez çağrılması güvenlidir.
func (e *Engine) Close(ctx context.Context) error {
	e.mailMu.Lock()
	if !e.closed {
		e.closed = true
		close(e.mailQueue)
	}
	e.mailMu.Unlock()

	done := make(chan struct{})
	go func() { e.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notify alert e-postasını kuyruğa alır. Asla bloklamaz ve çağıranı asla başarısız kılmaz.
func (e *Engine) notify(ctx context.Context, orgID uuid.UUID, alert model.Alert) {
	e.enqueue(ctx, mailJob{orgID: orgID, alert: alert})
}

// resolveAndNotify bir alert'i çözer ve gerçekten değiştiyse (daha önce zaten çözülmemişse) "çözüldü"
// e-postasını kuyruğa alır. Eşzamanlı bir çağrı önce davranmışsa (store.ErrNotFound) sessizce döner —
// aynı çözülme için ikinci bir bildirim gitmesin diye. value/threshold, store.Alerts.Resolve'a olduğu
// gibi geçer: eşik-tabanlı çözülmede güncel okumayı taşır, diğer yollarda nil'dir.
func (e *Engine) resolveAndNotify(ctx context.Context, id, orgID uuid.UUID, value, threshold *float64) {
	alert, err := e.alerts.Resolve(ctx, id, value, threshold)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		slog.ErrorContext(ctx, "alert engine: resolve alert", "alert_id", id.String(), "err", err)
		return
	}
	e.enqueue(ctx, mailJob{orgID: orgID, alert: alert})
}

func (e *Engine) enqueue(ctx context.Context, job mailJob) {
	alert := job.alert
	job.logInfo = logging.RequestInfoFrom(ctx)
	e.mailMu.RLock()
	defer e.mailMu.RUnlock()
	if e.closed {
		return
	}

	e.pending.Add(1)
	select {
	case e.mailQueue <- job:
	default:
		e.pending.Done()
		slog.WarnContext(ctx, "alert engine: mail queue full, dropping notification; it is still visible in the panel",
			"alert_id", alert.ID.String(), "alert_type", alert.AlertType, "level", alert.Level)
	}
}

// deliver, kendi süre sınırıyla bir işçide çalışır: alert'i açan istek genellikle bu noktada
// bitmiş (ve context'ini iptal etmiş) olur. Alıcılar kanala göre gruplanır ve her grup o kanalın Notifier'ına verilir;
// Notifier'ı olmayan kanalın alıcıları loglanıp atlanır.
//
// Bir panic yalnızca o bildirimi düşürür; işçi kuyruğu tüketmeye devam eder.
func (e *Engine) deliver(job mailJob) {
	ctx, cancel := context.WithTimeout(context.Background(), mailDeliveryTimeout)
	defer cancel()
	if job.logInfo != nil {
		ctx = logging.WithRequestInfo(ctx, job.logInfo)
	}
	defer logging.Recover(ctx, "alert mail delivery")
	orgID, alert := job.orgID, job.alert

	recipients, err := e.notifs.ResolveRecipients(ctx, alert.HostID, orgID, alert.Level)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: resolve recipients", "alert_id", alert.ID.String(), "host_id", alert.HostID.String(), "err", err)
		return
	}
	var channels []string // alıcı sırasına göre; teslim sırası belirli olsun
	byChannel := map[string][]string{}
	for _, r := range recipients {
		if _, ok := e.notifiers[r.Channel]; !ok {
			// Diğer kanallar (sms, slack…) şemada hazır ama henüz uygulanmadı; API bunlara kural yazdırmaz.
			slog.WarnContext(ctx, "alert engine: channel is not implemented, skipping recipient", "channel", r.Channel, "recipient", r.Name, "alert_id", alert.ID.String())
			continue
		}
		if _, seen := byChannel[r.Channel]; !seen {
			channels = append(channels, r.Channel)
		}
		byChannel[r.Channel] = append(byChannel[r.Channel], r.Address)
	}
	if len(channels) == 0 {
		return
	}

	msg := buildMessage(alert, e.messageContext(ctx, alert, orgID), e.panelBaseURL)
	for _, ch := range channels {
		if err := e.notifiers[ch].Send(ctx, byChannel[ch], msg); err != nil {
			slog.ErrorContext(ctx, "alert engine: send "+ch, "alert_id", alert.ID.String(), "err", err)
		}
	}
}

// messageContext, bildirimin sunucu bağlamını okur: 150 sunucu arasında hangisi olduğu yalnızca "Title"tan
// anlaşılmaz (aynı ad birden çok müşteride tekrar edebilir) — organizasyon, IP ve makinenin kendi hostname'i de gerekir.
// Okunamayan alanlar kimlikle ya da "—" ile doldurulur: bağlam eksik diye bildirim düşmez.
func (e *Engine) messageContext(ctx context.Context, alert model.Alert, orgID uuid.UUID) messageContext {
	mc := messageContext{OrgName: orgID.String(), HostTitle: alert.HostID.String(), Hostname: "—"}
	if host, err := e.hosts.GetByID(ctx, alert.HostID); err == nil {
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
