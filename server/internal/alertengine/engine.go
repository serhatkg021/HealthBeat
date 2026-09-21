// Package alertengine, docs/MIMARI.md bölüm 8'in eşik değerlendirmesini ve alert yaşam
// döngüsünü uygular: OPEN -> (seviye değişebilir) -> RESOLVED (eşiğin altına dönünce) ya da
// ACKNOWLEDGED (bir panel kullanıcısınca, bkz. httpapi). Metrik okuması kaydedildikten sonra
// hem push ingest handler'ından hem pull scheduler'dan çağrılır; böylece iki toplama yolu
// tek bir alert yolunu paylaşır.
package alertengine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
)

type Engine struct {
	thresholds *store.Thresholds
	metrics    *store.Metrics
	alerts     *store.Alerts
	hosts      *store.Hosts
	notifs     *store.Notifications
	mailer     *notify.Mailer

	// Alert e-postaları arka plan işçileri tarafından teslim edilir; alert'i açan ingest/poll
	// çağrısının içinde asla değil: yavaş ya da ölü bir SMTP relay metrik alımını durdurmamalı.
	// Kuyruk sınırlıdır; dolduğunda yeni bildirimler bloklamak yerine atılır (ve loglanır) —
	// alert'in kendisi zaten kaydedilmiş ve panelde görünürdür.
	mailMu    sync.RWMutex
	mailQueue chan mailJob
	closed    bool
	pending   sync.WaitGroup // kuyruktaki + çalışan işler (Flush)
	workers   sync.WaitGroup // çalışan işçi goroutine'leri (Close)
}

type mailJob struct {
	orgID uuid.UUID
	alert model.Alert
	// prevLevel, alert'in yükseltildiği bir bildirimde önceki seviyedir; o seviyede zaten bilgilendirilmiş alıcılara
	// aynı alert için tekrar yazılmaz, yalnızca yeni seviyeyle kural eşiğini aşan alıcılara gider. Yeni alert'te boştur.
	prevLevel string
}

const (
	defaultMailQueueSize = 256
	defaultMailWorkers   = 2
	// mailDeliveryTimeout, alert başına alıcı aramasını + SMTP oturumunu sınırlar.
	mailDeliveryTimeout = 45 * time.Second
)

func New(pool *pgxpool.Pool, mailer *notify.Mailer) *Engine {
	return newEngine(pool, mailer, defaultMailQueueSize, defaultMailWorkers)
}

func newEngine(pool *pgxpool.Pool, mailer *notify.Mailer, queueSize, workers int) *Engine {
	e := &Engine{
		thresholds: store.NewThresholds(pool),
		metrics:    store.NewMetrics(pool),
		alerts:     store.NewAlerts(pool),
		hosts:      store.NewHosts(pool, nil), // yalnızca host adlarını okur; pull secret'lara asla dokunmaz
		notifs:     store.NewNotifications(pool),
		mailer:     mailer,
		mailQueue:  make(chan mailJob, queueSize),
	}
	for i := 0; i < workers; i++ {
		e.workers.Add(1)
		go e.mailWorker()
	}
	return e
}

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

// EvaluateMetrics, bir metrik alımından sonra cpu/ram/disk eşik denetimlerini çalıştırır.
// docker_restart, container başına EvaluateDocker tarafından ayrıca değerlendirilir.
func (e *Engine) EvaluateMetrics(ctx context.Context, hostID, orgID uuid.UUID, cpuPct, ramPct float64, disks []model.DiskUsage) {
	e.evaluate(ctx, hostID, orgID, model.MetricTypeCPU, cpuPct)
	e.evaluate(ctx, hostID, orgID, model.MetricTypeRAM, ramPct)
	e.evaluateDisks(ctx, hostID, orgID, disks)
	e.evaluateMissingMounts(ctx, hostID, orgID, disks)
}

// evaluateDisks MOUNT BAŞINA bir alert üretir (Alert.Subject mount yoludur); her biri, varsa o
// mount'un kendi eşiğine, yoksa host'ın disk eşiğine göre değerlendirilir; host sahibinin
// seçtiği mount'lar için (hosts.all_mounts_alert true = raporlanan her mount, false = yalnızca host_custom_mounts_alerts).
// Seçilmeyen mount'lar hiç alert üretmez; artık seçili olmayan ya da artık raporlanmayan bir
// mount'un açık alert'i çözülür, böylece kapatacak bir şey kalmadan açık kalmaz.
//
// BOŞ bir rapor hiçbir şeyi değiştirmez: agent, toplama başarısız olduğunda da boş disk listesi
// yollar ve bunu "her mount kayboldu" diye okumak, sonraki iyi raporda tüm alert'leri çözüp
// yeniden açmaya (ve yeniden bildirmeye) yol açardı.
func (e *Engine) evaluateDisks(ctx context.Context, hostID, orgID uuid.UUID, disks []model.DiskUsage) {
	if len(disks) == 0 {
		return
	}
	thresholds, err := e.thresholds.ResolveSubjects(ctx, hostID, orgID, model.MetricTypeDisk)
	if err != nil {
		log.Printf("alert engine: resolve disk thresholds for host=%s: %v", hostID, err)
		return
	}
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		return
	}
	allMounts, selection, err := e.hosts.DiskAlertMounts(ctx, hostID)
	if err != nil {
		log.Printf("alert engine: read disk alert mounts for host=%s: %v", hostID, err)
		return // seçim olmadan hangi mount'ların istendiğini bilemeyiz; tahmin etmek yerine hiçbir şey yapma
	}
	var selected map[string]struct{} // nil = raporlanan tüm mount'lar
	if !allMounts {
		selected = make(map[string]struct{}, len(selection))
		for _, m := range selection {
			selected[m] = struct{}{}
		}
	}

	evaluated := make(map[string]struct{}, len(disks))
	for _, d := range disks {
		if selected != nil {
			if _, ok := selected[d.Mount]; !ok {
				continue
			}
		}
		// Hiçbir eşiği olmayan mount (ne host geneli ne kendi eşiği) değerlendirilmez; böylece
		// önceki bir eşikten kalan alert aşağıda kapatılır.
		threshold, ok := thresholds.For(d.Mount)
		if !ok {
			continue
		}
		evaluated[d.Mount] = struct{}{}
		e.apply(ctx, hostID, orgID, model.MetricTypeDisk, d.Mount, d.UsedPct, threshold)
	}

	open, err := e.alerts.ListOpen(ctx, hostID, model.MetricTypeDisk)
	if err != nil {
		log.Printf("alert engine: list open disk alerts for host=%s: %v", hostID, err)
		return
	}
	for _, a := range open {
		if _, still := evaluated[a.Subject]; !still {
			if err := e.alerts.Resolve(ctx, a.ID); err != nil {
				log.Printf("alert engine: resolve alert %s for mount %q: %v", a.ID, a.Subject, err)
			}
		}
	}
}

// missingMountReports, beklenen bir mount'un kayıp sayılması için (herhangi bir disk listeleyen)
// kaç ardışık raporda bulunmaması gerektiğidir. Bir rapor yetmez: başarısız ya da yavaş bir
// statfs agent'ın bir mount'u tek raporda dışarıda bırakmasına yol açar ve her aksamada
// bir "disk kayboldu" alert'i insanlara onu görmezden gelmeyi öğretirdi.
const missingMountReports = 3

// expectedMounts, yokluğu alert değeri taşıyan mount'lardır: disk alert'i için seçilenler ya da
// — raporlanan her mount alert üretiyorsa (seçim yapılmamış) — kendi eşiği olanlar.
// "Raporlanan her mount" ve hiç eşik yokken hiçbir şey beklenmez: kaybolan bir mount'u
// hiç var olmamış olandan ayırmanın yolu yoktur.
func (e *Engine) expectedMounts(ctx context.Context, hostID uuid.UUID) (map[string]struct{}, error) {
	allMounts, selection, err := e.hosts.DiskAlertMounts(ctx, hostID)
	if err != nil {
		return nil, err
	}
	expected := map[string]struct{}{}
	if !allMounts {
		for _, m := range selection {
			expected[m] = struct{}{}
		}
		return expected, nil
	}
	own, err := e.thresholds.HostSubjectOverrides(ctx, hostID, model.MetricTypeDisk)
	if err != nil {
		return nil, err
	}
	for m := range own {
		expected[m] = struct{}{}
	}
	return expected, nil
}

// evaluateMissingMounts, son missingMountReports raporun hiçbirinde listelenmeyen beklenen bir
// mount için critical disk_missing alert'i (Alert.Subject = mount) üretir; mount yeniden
// raporlanınca ya da beklenmez olunca çözer. evaluateDisks gibi boş bir raporu tamamen yok
// sayar: o bir toplama hatasıdır, "her mount kayboldu" değil.
func (e *Engine) evaluateMissingMounts(ctx context.Context, hostID, orgID uuid.UUID, disks []model.DiskUsage) {
	if len(disks) == 0 {
		return
	}
	expected, err := e.expectedMounts(ctx, hostID)
	if err != nil {
		log.Printf("alert engine: read expected mounts for host=%s: %v", hostID, err)
		return
	}
	open, err := e.alerts.ListOpen(ctx, hostID, model.AlertTypeDiskMissing)
	if err != nil {
		log.Printf("alert engine: list open disk_missing alerts for host=%s: %v", hostID, err)
		return
	}

	present := make(map[string]struct{}, len(disks))
	for _, d := range disks {
		present[d.Mount] = struct{}{}
	}
	for _, a := range open {
		_, wanted := expected[a.Subject]
		_, back := present[a.Subject]
		if !wanted || back {
			if err := e.alerts.Resolve(ctx, a.ID); err != nil {
				log.Printf("alert engine: resolve alert %s for mount %q: %v", a.ID, a.Subject, err)
			}
		}
	}

	var candidates []string
	for m := range expected {
		if _, ok := present[m]; !ok {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return
	}
	history, err := e.metrics.RecentReportedMounts(ctx, hostID, missingMountReports)
	if err != nil {
		log.Printf("alert engine: read recent reports for host=%s: %v", hostID, err)
		return
	}
	if len(history) < missingMountReports {
		return // hiçbir şeye kayıp demek için yeterli rapor yok
	}
	for _, m := range candidates {
		seen := false
		for _, report := range history {
			if _, ok := report[m]; ok {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		alert, created, err := e.alerts.CreateIfNoneOpen(ctx, hostID, model.AlertTypeDiskMissing, m, model.AlertLevelCritical, nil, nil)
		if err != nil {
			log.Printf("alert engine: create disk_missing alert for host=%s mount=%q: %v", hostID, m, err)
			continue
		}
		if created {
			e.notify(ctx, orgID, alert)
		}
	}
}

func (e *Engine) evaluate(ctx context.Context, hostID, orgID uuid.UUID, metricType string, value float64) {
	threshold, found, err := e.thresholds.Resolve(ctx, hostID, orgID, metricType)
	if err != nil {
		log.Printf("alert engine: resolve threshold for host=%s metric=%s: %v", hostID, metricType, err)
		return
	}
	if !found {
		return
	}
	e.apply(ctx, hostID, orgID, metricType, "", value, threshold)
}

// EvaluateDocker, docker_restart eşiğini (container'ın kendisininki, yoksa host'ınki) raporlanan
// her container'ın restart_count'una uygular — container başına bir alert (Alert.Subject
// container adıdır), her biri olağan open -> yükselt -> resolved yaşam döngüsüyle.
//
// restart_count Docker'ın kümülatif sayacıdır; bu yüzden container'ın sayacı eşiğin üstünde
// ya da eşit olduğu sürece alert açık kalır ve container yeniden oluşturulunca (sayaç 0'a
// döner) ya da rapordan kaybolunca çözülür.
//
// BOŞ bir rapor hiçbir şeyi değiştirmez: agent, Docker toplaması başarısız olduğunda da boş
// liste yollar ve bunu "tüm container'lar kayboldu" diye okumak, sonraki iyi raporda tüm
// alert'leri çözüp yeniden açmaya (ve yeniden bildirmeye) yol açardı.
func (e *Engine) EvaluateDocker(ctx context.Context, hostID, orgID uuid.UUID, containers []model.DockerContainerReport) {
	if len(containers) == 0 {
		return
	}
	thresholds, err := e.thresholds.ResolveSubjects(ctx, hostID, orgID, model.MetricTypeDockerRestart)
	if err != nil {
		log.Printf("alert engine: resolve docker_restart thresholds for host=%s: %v", hostID, err)
		return
	}
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		return
	}

	present := make(map[string]struct{}, len(containers))
	for _, c := range containers {
		// Hiçbir eşiği olmayan container (ne host geneli ne kendi eşiği) değerlendirilmez; önceki
		// bir eşikten kalan açık alert'i aşağıda kapanır.
		threshold, ok := thresholds.For(c.Name)
		if !ok {
			continue
		}
		present[c.Name] = struct{}{}
		e.apply(ctx, hostID, orgID, model.MetricTypeDockerRestart, c.Name, float64(c.RestartCount), threshold)
	}

	open, err := e.alerts.ListOpen(ctx, hostID, model.MetricTypeDockerRestart)
	if err != nil {
		log.Printf("alert engine: list open docker_restart alerts for host=%s: %v", hostID, err)
		return
	}
	for _, a := range open {
		if _, stillThere := present[a.Subject]; !stillThere {
			if err := e.alerts.Resolve(ctx, a.ID); err != nil {
				log.Printf("alert engine: resolve alert %s for removed container %q: %v", a.ID, a.Subject, err)
			}
		}
	}
}

// apply, bir host+metrik+subject için alert yaşam döngüsünü çözümlenmiş bir eşiğe göre
// çalıştırır.
func (e *Engine) apply(ctx context.Context, hostID, orgID uuid.UUID, metricType, subject string, value float64, threshold model.ThresholdConfig) {
	existing, err := e.alerts.GetOpenSubject(ctx, hostID, metricType, subject)
	hasOpen := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("alert engine: get open alert for host=%s metric=%s subject=%q: %v", hostID, metricType, subject, err)
		return
	}

	var level string
	switch {
	case value >= threshold.CriticalLevel:
		level = model.AlertLevelCritical
	case value >= threshold.WarningLevel:
		level = model.AlertLevelWarning
	}

	if level == "" {
		if hasOpen {
			if err := e.alerts.Resolve(ctx, existing.ID); err != nil {
				log.Printf("alert engine: resolve alert %s: %v", existing.ID, err)
			}
		}
		return
	}

	// Alert'in kaydettiği değer: tetiklendiği andaki ölçüm ve o seviyeyi tetikleyen eşik.
	trigger := threshold.WarningLevel
	if level == model.AlertLevelCritical {
		trigger = threshold.CriticalLevel
	}
	valuePtr, triggerPtr := &value, &trigger

	if hasOpen {
		// Tekrar bildirimi önleme/bekleme: bu host+metrik için zaten açık bir alert bu olayı
		// kapsıyor — yalnızca seviyesi değiştiyse yükselt.
		if existing.Level != level {
			if err := e.alerts.UpdateLevel(ctx, existing.ID, level, valuePtr, triggerPtr); err != nil {
				log.Printf("alert engine: update alert %s level: %v", existing.ID, err)
				return
			}
			// Seviye yükseldiyse, en düşük seviyesi artık aşılan kural alıcıları (ör. "yalnızca kritik") ilk kez
			// haberdar edilir; önceki seviyede zaten bilgilendirilenlere tekrar yazılmaz. Düşüşte bildirim yok.
			if model.LevelRank(level) > model.LevelRank(existing.Level) {
				prev := existing.Level
				existing.Level, existing.Value, existing.Threshold = level, valuePtr, triggerPtr
				e.enqueue(mailJob{orgID: orgID, alert: existing, prevLevel: prev})
			}
		}
		return
	}

	alert, created, err := e.alerts.CreateIfNoneOpen(ctx, hostID, metricType, subject, level, valuePtr, triggerPtr)
	if err != nil {
		log.Printf("alert engine: create alert for host=%s metric=%s subject=%q: %v", hostID, metricType, subject, err)
		return
	}
	if !created {
		// Denetimimiz ile eklememiz arasında eşzamanlı bir değerlendirme açtı. Bildirim onundur;
		// biz yalnızca seviyesinin güncel olduğundan emin oluruz.
		if open, err := e.alerts.GetOpenSubject(ctx, hostID, metricType, subject); err == nil && open.Level != level {
			if err := e.alerts.UpdateLevel(ctx, open.ID, level, valuePtr, triggerPtr); err != nil {
				log.Printf("alert engine: update alert %s level: %v", open.ID, err)
			}
		}
		return
	}
	e.notify(ctx, orgID, alert)
}

// ResolveOffline, bir host yeniden rapor verdiğinde açık host_offline alert'ini kendiliğinden
// çözer — her başarılı push/pull alımından sonra çağrılır.
func (e *Engine) ResolveOffline(ctx context.Context, hostID uuid.UUID) {
	if err := e.alerts.ResolveOpenByHostAndMetric(ctx, hostID, model.AlertTypeHostOffline); err != nil {
		log.Printf("alert engine: resolve offline alert for host=%s: %v", hostID, err)
	}
}

// RaiseOffline, bir host sessizleştiğinde offline monitor tarafından çağrılır.
func (e *Engine) RaiseOffline(ctx context.Context, hostID, orgID uuid.UUID) {
	_, err := e.alerts.GetOpen(ctx, hostID, model.AlertTypeHostOffline)
	if err == nil {
		return // zaten açık — tekrar bildirimi önle
	}
	if !errors.Is(err, store.ErrNotFound) {
		log.Printf("alert engine: check open offline alert for host=%s: %v", hostID, err)
		return
	}

	alert, created, err := e.alerts.CreateIfNoneOpen(ctx, hostID, model.AlertTypeHostOffline, "", model.AlertLevelCritical, nil, nil)
	if err != nil {
		log.Printf("alert engine: create offline alert for host=%s: %v", hostID, err)
		return
	}
	if !created {
		return // eşzamanlı açıldı; bildirimi o çağıran yapar
	}
	e.notify(ctx, orgID, alert)
}

// notify alert e-postasını kuyruğa alır. Asla bloklamaz ve çağıranı asla başarısız kılmaz.
func (e *Engine) notify(_ context.Context, orgID uuid.UUID, alert model.Alert) {
	e.enqueue(mailJob{orgID: orgID, alert: alert})
}

func (e *Engine) enqueue(job mailJob) {
	alert := job.alert
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
		log.Printf("alert engine: mail queue full, dropping notification for alert %s (%s/%s); it is still visible in the panel",
			alert.ID, alert.AlertType, alert.Level)
	}
}

// deliver, kendi süre sınırıyla bir işçide çalışır: alert'i açan istek genellikle bu noktada
// bitmiş (ve context'ini iptal etmiş) olur.
func (e *Engine) deliver(job mailJob) {
	ctx, cancel := context.WithTimeout(context.Background(), mailDeliveryTimeout)
	defer cancel()
	orgID, alert := job.orgID, job.alert

	recipients, err := e.notifs.ResolveRecipients(ctx, alert.HostID, orgID, alert.Level)
	if err != nil {
		log.Printf("alert engine: resolve recipients for host=%s: %v", alert.HostID, err)
		return
	}
	if job.prevLevel != "" {
		already, err := e.notifs.ResolveRecipients(ctx, alert.HostID, orgID, job.prevLevel)
		if err != nil {
			log.Printf("alert engine: resolve previous recipients for host=%s: %v", alert.HostID, err)
			return
		}
		seen := make(map[string]bool, len(already))
		for _, r := range already {
			seen[r.Channel+"\x00"+r.Address] = true
		}
		fresh := recipients[:0:0]
		for _, r := range recipients {
			if !seen[r.Channel+"\x00"+r.Address] {
				fresh = append(fresh, r)
			}
		}
		recipients = fresh
	}
	var emails []string
	for _, r := range recipients {
		if r.Channel == model.ChannelEmail {
			emails = append(emails, r.Address)
			continue
		}
		// Diğer kanallar (sms, slack…) şemada hazır ama henüz uygulanmadı; API bunlara kural yazdırmaz.
		log.Printf("alert engine: channel %q is not implemented, skipping recipient %q for alert %s", r.Channel, r.Name, alert.ID)
	}
	if len(emails) == 0 {
		return
	}

	hostTitle := alert.HostID.String()
	if host, err := e.hosts.GetByID(ctx, alert.HostID); err == nil {
		hostTitle = host.Title
	}

	what := hostTitle
	body := fmt.Sprintf("Sunucu: %s\n", hostTitle)
	if alert.Subject != "" {
		kind, label := "container", "Container"
		if alert.AlertType == model.MetricTypeDisk || alert.AlertType == model.AlertTypeDiskMissing {
			kind, label = "mount", "Mount"
		}
		what = fmt.Sprintf("%s (%s %s)", hostTitle, kind, alert.Subject)
		body += fmt.Sprintf("%s: %s\n", label, alert.Subject)
	}
	if alert.AlertType == model.AlertTypeDiskMissing {
		body += fmt.Sprintf("Ayrıntı: %s mount'u son %d raporda görünmedi (unmount edilmiş, hata vermiş ya da yanıt vermiyor).\n", alert.Subject, missingMountReports)
	}
	if alert.Value != nil && alert.Threshold != nil {
		body += fmt.Sprintf("Değer: %s (eşik: %s)\n", formatAlertNumber(*alert.Value), formatAlertNumber(*alert.Threshold))
	}
	subject := fmt.Sprintf("[HealthBeat] %s %s alert'i: %s", alertLevelLabel(alert.Level), alertMetricLabel(alert.AlertType), what)
	body += fmt.Sprintf(
		"Tür: %s\nSeviye: %s\nAlert kimliği: %s\nOluşturulma: %s\n",
		alertMetricLabel(alert.AlertType), alertLevelLabel(alert.Level), alert.ID, alert.CreatedAt.Format(time.RFC3339),
	)

	if err := e.mailer.Send(ctx, emails, subject, body); err != nil {
		log.Printf("alert engine: send email for alert %s: %v", alert.ID, err)
	}
}

// formatAlertNumber, ölçümü gereksiz ondalık basamaksız yazar (95 → "95", 92.5 → "92.5").
func formatAlertNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// alertLevelLabel, e-postada gösterilen alert seviyesi adıdır (konu satırında büyük harfle).
func alertLevelLabel(level string) string {
	switch level {
	case model.AlertLevelCritical:
		return "KRİTİK"
	case model.AlertLevelWarning:
		return "UYARI"
	case model.AlertLevelInfo:
		return "BİLGİ"
	default:
		return strings.ToUpper(level)
	}
}

// alertMetricLabel, e-postada gösterilen metrik adıdır.
func alertMetricLabel(metricType string) string {
	switch metricType {
	case model.MetricTypeCPU:
		return "CPU"
	case model.MetricTypeRAM:
		return "RAM"
	case model.MetricTypeDisk:
		return "disk"
	case model.MetricTypeDockerRestart:
		return "docker restart"
	case model.AlertTypeHostOffline:
		return "sunucu çevrimdışı"
	case model.AlertTypeDiskMissing:
		return "disk kayboldu"
	default:
		return metricType
	}
}
