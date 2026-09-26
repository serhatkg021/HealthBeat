// Package alertengine, docs/MIMARI.md bölüm 8'in eşik değerlendirmesini ve alert yaşam
// döngüsünü uygular: OPEN -> (seviye değişebilir) -> RESOLVED (eşiğin altına dönünce) ya da
// ACKNOWLEDGED (bir panel kullanıcısınca, bkz. httpapi). Metrik okuması kaydedildikten sonra
// hem push ingest handler'ından hem pull scheduler'dan çağrılır; böylece iki toplama yolu
// tek bir alert yolunu paylaşır.
//
// Dosyalar: engine.go değerlendirme ve alert yaşam döngüsü; dispatch.go bildirim kuyruğu ve kanallara teslim;
// message.go bildirim metni; stores.go motorun kullandığı depo arayüzleri.
package alertengine

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
)

// Engine, eşikleri değerlendirir, alert'leri açar/günceller/çözer ve bildirimlerini kuyruğa alır.
type Engine struct {
	thresholds    ThresholdStore
	metrics       MetricStore
	alerts        AlertStore
	hosts         HostStore
	organizations OrgStore
	notifs        RecipientStore
	// notifiers, kanal adına göre bildirim kanallarıdır (bkz. notify.Notifier); olmayan kanalın alıcıları atlanır.
	notifiers map[string]notify.Notifier
	// panelBaseURL, bildirimlerde alert'e doğrudan giden bir bağlantı eklemek için (boşsa satır hiç eklenmez).
	panelBaseURL string

	// Bildirimler arka plan işçileri tarafından teslim edilir (dispatch.go); alert'i açan ingest/poll
	// çağrısının içinde asla değil: yavaş ya da ölü bir SMTP relay metrik alımını durdurmamalı.
	// Kuyruk sınırlıdır; dolduğunda yeni bildirimler bloklamak yerine atılır (ve loglanır) —
	// alert'in kendisi zaten kaydedilmiş ve panelde görünürdür.
	mailMu    sync.RWMutex
	mailQueue chan mailJob
	closed    bool
	pending   sync.WaitGroup // kuyruktaki + çalışan işler (Flush)
	workers   sync.WaitGroup // çalışan işçi goroutine'leri (Close)
}

func New(pool *pgxpool.Pool, mailer *notify.Mailer, panelBaseURL string) *Engine {
	return newEngine(pool, mailer, panelBaseURL, defaultMailQueueSize, defaultMailWorkers)
}

func newEngine(pool *pgxpool.Pool, mailer *notify.Mailer, panelBaseURL string, queueSize, workers int) *Engine {
	return newEngineWith(Stores{
		Thresholds:    store.NewThresholds(pool),
		Metrics:       store.NewMetrics(pool),
		Alerts:        store.NewAlerts(pool),
		Hosts:         store.NewHosts(pool, nil), // yalnızca host adlarını okur; pull secret'lara asla dokunmaz
		Organizations: store.NewOrganizations(pool),
		Recipients:    store.NewNotifications(pool),
	}, []notify.Notifier{notify.EmailChannel{Mailer: mailer}}, panelBaseURL, queueSize, workers)
}

// newEngineWith, motoru verilen depolar ve kanallarla kurar; DB'siz testler sahte depolar ve sahte kanallar verir.
func newEngineWith(st Stores, notifiers []notify.Notifier, panelBaseURL string, queueSize, workers int) *Engine {
	e := &Engine{
		thresholds:    st.Thresholds,
		metrics:       st.Metrics,
		alerts:        st.Alerts,
		hosts:         st.Hosts,
		organizations: st.Organizations,
		notifs:        st.Recipients,
		notifiers:     make(map[string]notify.Notifier, len(notifiers)),
		panelBaseURL:  strings.TrimSuffix(panelBaseURL, "/"),
		mailQueue:     make(chan mailJob, queueSize),
	}
	for _, n := range notifiers {
		e.notifiers[n.Channel()] = n
	}
	for i := 0; i < workers; i++ {
		e.workers.Add(1)
		go e.mailWorker()
	}
	return e
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
		slog.ErrorContext(ctx, "alert engine: resolve disk thresholds", "host_id", hostID.String(), "err", err)
		return
	}
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		return
	}
	allMounts, selection, err := e.hosts.DiskAlertMounts(ctx, hostID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: read disk alert mounts", "host_id", hostID.String(), "err", err)
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

	open, err := e.alerts.ListActive(ctx, hostID, model.MetricTypeDisk)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: list open disk alerts", "host_id", hostID.String(), "err", err)
		return
	}
	for _, a := range open {
		if _, still := evaluated[a.Subject]; !still {
			e.resolveAndNotify(ctx, a.ID, orgID, nil, nil)
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
		slog.ErrorContext(ctx, "alert engine: read expected mounts", "host_id", hostID.String(), "err", err)
		return
	}
	open, err := e.alerts.ListActive(ctx, hostID, model.AlertTypeDiskMissing)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: list open disk_missing alerts", "host_id", hostID.String(), "err", err)
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
			e.resolveAndNotify(ctx, a.ID, orgID, nil, nil)
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
		slog.ErrorContext(ctx, "alert engine: read recent reports", "host_id", hostID.String(), "err", err)
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
		alert, created, err := e.alerts.CreateIfNoneActive(ctx, hostID, model.AlertTypeDiskMissing, m, model.AlertLevelCritical, nil, nil)
		if err != nil {
			slog.ErrorContext(ctx, "alert engine: create disk_missing alert", "host_id", hostID.String(), "mount", m, "err", err)
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
		slog.ErrorContext(ctx, "alert engine: resolve threshold", "host_id", hostID.String(), "metric", metricType, "err", err)
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
		slog.ErrorContext(ctx, "alert engine: resolve docker_restart thresholds", "host_id", hostID.String(), "err", err)
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

	open, err := e.alerts.ListActive(ctx, hostID, model.MetricTypeDockerRestart)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: list open docker_restart alerts", "host_id", hostID.String(), "err", err)
		return
	}
	for _, a := range open {
		if _, stillThere := present[a.Subject]; !stillThere {
			e.resolveAndNotify(ctx, a.ID, orgID, nil, nil)
		}
	}
}

// apply, bir host+metrik+subject için alert yaşam döngüsünü çözümlenmiş bir eşiğe göre
// çalıştırır.
func (e *Engine) apply(ctx context.Context, hostID, orgID uuid.UUID, metricType, subject string, value float64, threshold model.ThresholdConfig) {
	// Aktif alert: açık ya da onaylanmış. Onay "gördüm, sustur ama izle"dir: onaylanan alert yeni bir alert/bildirim
	// açılmasını engeller ve eşik altına inince çözülür (bkz. store: aktif alert).
	existing, err := e.alerts.GetActiveSubject(ctx, hostID, metricType, subject)
	hasActive := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.ErrorContext(ctx, "alert engine: get active alert", "host_id", hostID.String(), "metric", metricType, "subject", subject, "err", err)
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
		if hasActive {
			// Çözülme okuması: eşiğin altına döndüğü andaki gerçek ölçüm ve uyarı eşiği — böylece
			// e-postadaki "Değer" alert'in son yükseltildiği eski, hâlâ eşik üstü okumayı değil,
			// artık gerçekten eşiğin altında olan güncel durumu gösterir.
			e.resolveAndNotify(ctx, existing.ID, orgID, &value, &threshold.WarningLevel)
		}
		return
	}

	// Alert'in kaydettiği değer: tetiklendiği andaki ölçüm ve o seviyeyi tetikleyen eşik.
	trigger := threshold.WarningLevel
	if level == model.AlertLevelCritical {
		trigger = threshold.CriticalLevel
	}
	valuePtr, triggerPtr := &value, &trigger

	if hasActive {
		// Tekrar bildirimi önleme/bekleme: bu host+metrik için zaten aktif (açık ya da onaylanmış) bir alert bu
		// olayı kapsıyor — yalnızca seviyesi değiştiyse yeniden bildirilir.
		if existing.Level != level {
			// Seviye değişimi (yükselme YA DA düşme) o alıcı için durumun gerçekten değiştiği
			// anlamına gelir: uyarı mailini görüp "daha vaktim var" diyen biri kritiğe geçtiğinde,
			// ya da tersine gereksiz yere endişelenmemesi için kritikten uyarıya düştüğünde bundan
			// habersiz kalmamalı. Bu yüzden yeni seviyenin TÜM alıcılarına (daha önce bilgilendirilmiş
			// olsalar bile) tekrar mail gider — açılış ve çözülme ile aynı kural. Onaylanmış bir alert
			// YÜKSELİRSE onay da kalkar (durum ciddileşti, biri yeniden sahiplenmeli); düşüşte onay korunur.
			reopen := reopensOnLevelChange(existing, level)
			if err := e.alerts.UpdateLevel(ctx, existing.ID, level, valuePtr, triggerPtr, reopen); err != nil {
				slog.ErrorContext(ctx, "alert engine: update alert level", "alert_id", existing.ID.String(), "err", err)
				return
			}
			existing.Level, existing.Value, existing.Threshold = level, valuePtr, triggerPtr
			if reopen {
				existing.Status, existing.AcknowledgedAt, existing.AcknowledgedBy = model.AlertStatusOpen, nil, nil
			}
			e.notify(ctx, orgID, existing)
		}
		return
	}

	alert, created, err := e.alerts.CreateIfNoneActive(ctx, hostID, metricType, subject, level, valuePtr, triggerPtr)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: create alert", "host_id", hostID.String(), "metric", metricType, "subject", subject, "err", err)
		return
	}
	if !created {
		// Denetimimiz ile eklememiz arasında eşzamanlı bir değerlendirme açtı. Bildirim onundur;
		// biz yalnızca seviyesinin güncel olduğundan emin oluruz.
		if active, err := e.alerts.GetActiveSubject(ctx, hostID, metricType, subject); err == nil && active.Level != level {
			if err := e.alerts.UpdateLevel(ctx, active.ID, level, valuePtr, triggerPtr, reopensOnLevelChange(active, level)); err != nil {
				slog.ErrorContext(ctx, "alert engine: update alert level", "alert_id", active.ID.String(), "err", err)
			}
		}
		return
	}
	e.notify(ctx, orgID, alert)
}

// reopensOnLevelChange, onaylanmış bir alert'in yeni seviyeyle yeniden açılıp açılmayacağıdır: yalnızca seviye
// yükselirse (ör. uyarı → kritik). Düşüşte onay korunur — durum hafifledi, sahibi zaten ilgileniyor.
func reopensOnLevelChange(a model.Alert, newLevel string) bool {
	return a.Status == model.AlertStatusAcknowledged && model.LevelRank(newLevel) > model.LevelRank(a.Level)
}

// ResolveOffline, bir host yeniden rapor verdiğinde aktif (açık ya da onaylanmış) host_offline alert'ini kendiliğinden
// çözer ve "sunucu tekrar çevrimiçi" e-postasını kuyruğa alır — her başarılı push/pull
// alımından sonra çağrılır.
func (e *Engine) ResolveOffline(ctx context.Context, hostID, orgID uuid.UUID) {
	alert, err := e.alerts.ResolveActiveByHostAndMetric(ctx, hostID, model.AlertTypeHostOffline)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return // aktif değildi
		}
		slog.ErrorContext(ctx, "alert engine: resolve offline alert", "host_id", hostID.String(), "err", err)
		return
	}
	e.enqueue(ctx, mailJob{orgID: orgID, alert: alert})
}

// RaiseOffline, bir host sessizleştiğinde offline monitor tarafından çağrılır.
func (e *Engine) RaiseOffline(ctx context.Context, hostID, orgID uuid.UUID) {
	_, err := e.alerts.GetActive(ctx, hostID, model.AlertTypeHostOffline)
	if err == nil {
		return // zaten aktif (açık ya da onaylanmış) — tekrar bildirimi önle
	}
	if !errors.Is(err, store.ErrNotFound) {
		slog.ErrorContext(ctx, "alert engine: check open offline alert", "host_id", hostID.String(), "err", err)
		return
	}

	alert, created, err := e.alerts.CreateIfNoneActive(ctx, hostID, model.AlertTypeHostOffline, "", model.AlertLevelCritical, nil, nil)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: create offline alert", "host_id", hostID.String(), "err", err)
		return
	}
	if !created {
		return // eşzamanlı açıldı; bildirimi o çağıran yapar
	}
	e.notify(ctx, orgID, alert)
}
