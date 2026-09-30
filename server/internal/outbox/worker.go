// Package outbox, bildirim kuyruğunu (notification_outbox) teslim eden işçidir: teslim zamanı gelmiş satırları alır,
// kanalın Notifier'ıyla gönderir, başarısızlıkta geri çekilerek yeniden dener ve bitmiş eski satırları temizler.
// Kuyruğa yazanlar alert motoru (alert bildirimleri) ve şifre e-postalarıdır; her biri kendi türleri için bir işçi
// çalıştırır. Birden çok işçi ya da server kopyası güvenle birlikte çalışır (bkz. store.Outbox.Claim).
package outbox

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
)

const (
	// PollInterval, uyandırılmayan işçinin kuyruğa bakma sıklığıdır (başka bir kopyanın ya da yeniden denemenin satırları).
	PollInterval = 2 * time.Second
	// MaxAttempts, bir satırdan vazgeçmeden önceki en çok deneme sayısıdır (geri çekilmeyle yaklaşık 4 saat).
	MaxAttempts = 10
	// RetainFinished, gönderilmiş ya da vazgeçilmiş satırların teslim durumu sorgulanabilsin diye tutulduğu süredir.
	RetainFinished = 30 * 24 * time.Hour

	batchSize       = 20
	deliveryTimeout = 45 * time.Second
	// lease, alınan satırın başka işçiye görünmez kaldığı süredir; bir teslimden uzun olmalı.
	lease       = 2 * time.Minute
	baseBackoff = 30 * time.Second
	maxBackoff  = time.Hour
	purgeEvery  = time.Hour
)

// Worker, belirli türlerdeki satırları teslim eder.
type Worker struct {
	store     *store.Outbox
	kinds     []string
	notifiers map[string]notify.Notifier
	wake      chan struct{}

	// mu, bu süreçte aynı anda tek teslim turu çalışmasını sağlar; DeliverDue (Flush) böylece arka plandaki turun
	// bitmesini de bekler.
	mu        sync.Mutex
	lastPurge time.Time
}

// NewWorker, kinds türlerindeki satırları notifiers ile teslim eden bir işçi kurar.
func NewWorker(st *store.Outbox, kinds []string, notifiers ...notify.Notifier) *Worker {
	w := &Worker{store: st, kinds: kinds, notifiers: make(map[string]notify.Notifier, len(notifiers)), wake: make(chan struct{}, 1)}
	for _, n := range notifiers {
		w.notifiers[n.Channel()] = n
	}
	return w
}

// Wake, işçiye beklemeden kuyruğa bakmasını söyler (satır yazılıp commit edildikten sonra çağrılır). Bloklamaz.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run, ctx bitene kadar kuyruğu teslim eder.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.DeliverDue(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "outbox: delivery pass failed", "kinds", w.kinds, "err", err)
		}
		w.purge(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
	}
}

// DeliverDue, şu an teslim zamanı gelmiş bütün satırları teslim etmeyi dener ve kaç tanesinin gönderildiğini döndürür.
// Başarısız satırlar ileri bir zamana ertelendiği için tur her zaman biter. Testler ve kapanıştaki boşaltma içindir.
func (w *Worker) DeliverDue(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if n, err := w.store.Expire(ctx, w.kinds); err != nil {
		return 0, err
	} else if n > 0 {
		slog.WarnContext(ctx, "outbox: notifications expired before delivery", "kinds", w.kinds, "count", n)
	}
	sent := 0
	for {
		items, err := w.store.Claim(ctx, w.kinds, batchSize, lease)
		if err != nil {
			return sent, err
		}
		if len(items) == 0 {
			return sent, nil
		}
		for _, it := range items {
			if w.deliver(ctx, it) {
				sent++
			}
		}
	}
}

// deliver bir satırı gönderir ve sonucunu kaydeder; gönderildiyse true.
func (w *Worker) deliver(parent context.Context, it store.OutboxItem) (sent bool) {
	ctx := parent
	if it.RequestID != "" {
		// Teslim satırları bildirimi doğuran isteğin kimliğini taşır: "gitmedi" satırı onu açan agent raporuna bağlanır.
		ctx = logging.WithRequestInfo(ctx, &logging.RequestInfo{ID: it.RequestID})
	}
	attrs := []any{"outbox_id", it.ID.String(), "kind", it.Kind, "channel", it.Channel, "attempt", it.Attempts}
	if it.AlertID != nil {
		attrs = append(attrs, "alert_id", it.AlertID.String())
	}
	defer logging.Recover(ctx, "notification delivery")

	giveUp := func(msg string, err error) {
		slog.ErrorContext(ctx, msg, append(attrs, "err", err)...)
		if err := w.store.MarkFailed(parent, it.ID, err.Error()); err != nil {
			slog.ErrorContext(ctx, "outbox: mark failed", append(attrs, "err", err)...)
		}
	}
	if it.OpenErr != nil {
		giveUp("notification cannot be decrypted; giving up", it.OpenErr)
		return false
	}
	n, ok := w.notifiers[it.Channel]
	if !ok {
		giveUp("notification channel is not available; giving up", errUnknownChannel(it.Channel))
		return false
	}

	sendCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	err := sendEach(sendCtx, n, it.Recipients, notify.Message{Subject: it.Subject, Body: it.Body})
	cancel()
	if err == nil {
		if err := w.store.MarkSent(parent, it.ID); err != nil {
			// Gönderildi ama işaretlenemedi: kira bitince yeniden gönderilebilir (en az bir kez teslim).
			slog.ErrorContext(ctx, "outbox: mark sent", append(attrs, "err", err)...)
		}
		return true
	}
	if it.Attempts >= MaxAttempts {
		giveUp("notification delivery failed; giving up", err)
		return false
	}
	next := time.Now().Add(Backoff(it.Attempts))
	slog.WarnContext(ctx, "notification delivery failed; will retry", append(attrs, "retry_at", next.UTC().Format(time.RFC3339), "err", err)...)
	if err := w.store.MarkRetry(parent, it.ID, next, err.Error()); err != nil {
		slog.ErrorContext(ctx, "outbox: mark retry", append(attrs, "err", err)...)
	}
	return false
}

// Backoff, attempt'inci başarısız denemeden sonraki bekleme süresidir: 30 sn, 1 dk, 2 dk… en çok 1 saat.
func Backoff(attempt int) time.Duration {
	d := baseBackoff
	for i := 1; i < attempt && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

func (w *Worker) purge(ctx context.Context) {
	if time.Since(w.lastPurge) < purgeEvery {
		return
	}
	w.lastPurge = time.Now()
	n, err := w.store.PurgeFinished(ctx, w.kinds, time.Now().Add(-RetainFinished))
	switch {
	case err != nil && ctx.Err() == nil:
		slog.ErrorContext(ctx, "outbox: purge finished notifications", "kinds", w.kinds, "err", err)
	case n > 0:
		slog.InfoContext(ctx, "outbox: purged finished notifications", "kinds", w.kinds, "count", n)
	}
}

type errUnknownChannel string

func (e errUnknownChannel) Error() string { return "no notifier for channel " + string(e) }

// sendEach, iletiyi her alıcıya ayrı gönderir. Yeni satırların tek alıcısı vardır; birden çok alıcılı satırı yalnızca
// güncelleme sırasında hâlâ çalışan eski bir server yazmış olabilir (alıcılar yine birbirini görmez). Bir alıcıda hata
// olursa satır yeniden denenir: o satırın önceki alıcıları iletiyi ikinci kez alabilir (en az bir kez teslim).
func sendEach(ctx context.Context, n notify.Notifier, recipients []string, msg notify.Message) error {
	for _, r := range recipients {
		if err := n.Send(ctx, r, msg); err != nil {
			return err
		}
	}
	return nil
}
