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
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/outbox"
	"healthbeat-server/internal/store"
)

// Engine, eşikleri değerlendirir, alert'leri açar/günceller/çözer ve bildirimlerini kuyruğa yazar (dispatch.go).
type Engine struct {
	thresholds    ThresholdStore
	metrics       MetricStore
	alerts        AlertStore
	pending       PendingStore
	hosts         HostStore
	organizations OrgStore
	notifs        RecipientStore
	tx            TxRunner
	// notifiers, kanal adına göre bildirim kanallarıdır (bkz. notify.Notifier); olmayan kanalın alıcıları atlanır.
	notifiers map[string]notify.Notifier
	// panelBaseURL, bildirimlerde alert'e doğrudan giden bir bağlantı eklemek için (boşsa satır hiç eklenmez). Panelden
	// değişebilir (SetPanelBaseURL); her bildirim kuyruğa yazılırken okunur.
	panelBaseURL atomic.Pointer[string]
	// worker, alert bildirimlerini kuyruktan teslim eder; DB'siz testlerde nil.
	worker *outbox.Worker
	// now, süre koşullarının saatidir (testler değiştirir).
	now func() time.Time
}

// New, motoru ve alert bildirimlerinin teslim işçisini kurar; teslim için RunNotifications'ı çalıştırın.
func New(pool *pgxpool.Pool, mailer *notify.Mailer, panelBaseURL string) *Engine {
	alerts := store.NewAlerts(pool)
	queue := store.NewOutbox(pool, nil) // alert bildirimleri şifrelenmez
	email := notify.EmailChannel{Mailer: mailer}
	return newEngineWith(Stores{
		Thresholds:    store.NewThresholds(pool),
		Metrics:       store.NewMetrics(pool),
		Alerts:        alerts,
		Pending:       alerts,
		Hosts:         store.NewHosts(pool, nil), // yalnızca host adlarını okur; pull secret'lara asla dokunmaz
		Organizations: store.NewOrganizations(pool),
		Recipients:    store.NewNotifications(pool),
		Tx:            pgTx{pool: pool, alerts: alerts, outbox: queue},
	}, []notify.Notifier{email}, panelBaseURL, outbox.NewWorker(queue, []string{store.OutboxKindAlert}, email))
}

// newEngineWith, motoru verilen depolar ve kanallarla kurar; DB'siz testler sahte depolar ve sahte kanallar verir
// (worker nil olabilir: bildirimler yalnızca kuyruğa yazılır).
func newEngineWith(st Stores, notifiers []notify.Notifier, panelBaseURL string, worker *outbox.Worker) *Engine {
	e := &Engine{
		thresholds:    st.Thresholds,
		metrics:       st.Metrics,
		alerts:        st.Alerts,
		pending:       st.Pending,
		hosts:         st.Hosts,
		organizations: st.Organizations,
		notifs:        st.Recipients,
		tx:            st.Tx,
		notifiers:     make(map[string]notify.Notifier, len(notifiers)),
		worker:        worker,
		now:           time.Now,
	}
	e.SetPanelBaseURL(panelBaseURL)
	for _, n := range notifiers {
		e.notifiers[n.Channel()] = n
	}
	return e
}

// Notifier, kanalın bir göndericisi olup olmadığını (implemented) ve kişiye mi gittiğini (personal) söyler.
func (e *Engine) Notifier(channel string) (personal, implemented bool) {
	n, ok := e.notifiers[channel]
	if !ok {
		return false, false
	}
	return n.Personal(), true
}

// SetPanelBaseURL, bildirimlerdeki panel bağlantısının kökünü değiştirir ("" bağlantıyı kaldırır).
func (e *Engine) SetPanelBaseURL(url string) {
	url = strings.TrimSuffix(url, "/")
	e.panelBaseURL.Store(&url)
}

// Report, bir agent raporunun alert motorunu ilgilendiren kısmıdır. Metrik satırı EvaluateReport'tan önce yazılmış
// olmalıdır (salt okunur geçişi bir önceki raporla karşılaştırılır).
type Report struct {
	CPUPct, RAMPct float64
	Disks          []model.DiskUsage
	Containers     []model.DockerContainerReport
	// Durum alert'leri için (status.go): bu raporla saklanan anlık durum (nil = bilinmiyor), OOM sayacının bu raporda
	// artıp artmadığı ve makine envanteri (yeniden başlatma gerekli, saat senkronu; protokol 3 agent'larda da var).
	State        *model.SystemState
	OOMIncreased bool
	HostInfo     *model.HostInfo
	// DiskIO, disk G/Ç'sidir (disk gecikmesi alert'i; numeric.go). Sıcaklık ve saat farkı State'tedir.
	DiskIO []model.DiskIO
}

// EvaluateReport, bir alımdan sonra bütün alert denetimlerini çalıştırır: aktif host_offline alert'ini çözer, sonra
// container'ları, metrikleri ve durum alert'lerini değerlendirir. Host'un aktif alert'leri, eşikleri ve durum
// kuralları rapor başına bir kez okunur.
func (e *Engine) EvaluateReport(ctx context.Context, hostID, orgID uuid.UUID, r Report) {
	st, ok := e.loadState(ctx, hostID, orgID)
	if !ok {
		return
	}
	if a, ok := st.take(model.AlertTypeHostOffline, ""); ok {
		e.resolveAndNotify(ctx, a, orgID, nil, nil)
	}
	e.evaluateDocker(ctx, st, r.Containers)
	e.evaluateMetrics(ctx, st, r.CPUPct, r.RAMPct, r.Disks)
	e.evaluateStatus(ctx, st, r)
	e.evaluateNumeric(ctx, st, r)
}

// EvaluateMetrics, bir metrik alımından sonra cpu/ram/disk eşik denetimlerini çalıştırır.
// docker_restart, container başına EvaluateDocker tarafından ayrıca değerlendirilir.
func (e *Engine) EvaluateMetrics(ctx context.Context, hostID, orgID uuid.UUID, cpuPct, ramPct float64, disks []model.DiskUsage) {
	if st, ok := e.loadState(ctx, hostID, orgID); ok {
		e.evaluateMetrics(ctx, st, cpuPct, ramPct, disks)
	}
}

// hostState, bir raporun değerlendirmesinde bir kez okunan durumdur: host'un aktif alert'leri, bütün eşikleri, durum
// kuralları ve süre koşulu bekleyen koşulları. Her kalem (metrik, mount, container) veritabanına ayrı ayrı gitmek
// yerine buradan okur; motorun yaptığı değişiklikler de buraya yansıtılır. Aynı anda açılmaya karşı koruma yine
// veritabanındadır (CreateIfNoneActive).
type hostState struct {
	hostID, orgID uuid.UUID
	thresholds    store.HostThresholds
	rules         model.StatusRuleSet
	active        map[alertKey]model.Alert
	pending       map[alertKey]time.Time // koşulun başladığı an
	// serviceList, servis tablosudur; ihtiyaç olunca bir kez okunur (bkz. Engine.services).
	serviceList    []model.HostService
	servicesLoaded bool
}

type alertKey struct{ alertType, subject string }

func (e *Engine) loadState(ctx context.Context, hostID, orgID uuid.UUID) (*hostState, bool) {
	thresholds, err := e.thresholds.ResolveHost(ctx, hostID, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: resolve thresholds", "host_id", hostID.String(), "err", err)
		return nil, false
	}
	active, err := e.alerts.ListActiveForHost(ctx, hostID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: list active alerts", "host_id", hostID.String(), "err", err)
		return nil, false
	}
	rules, err := e.thresholds.ResolveStatusRules(ctx, hostID, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: resolve status rules", "host_id", hostID.String(), "err", err)
		return nil, false
	}
	pending, err := e.pending.ListPending(ctx, hostID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: list pending conditions", "host_id", hostID.String(), "err", err)
		return nil, false
	}
	st := &hostState{hostID: hostID, orgID: orgID, thresholds: thresholds, rules: rules,
		active: make(map[alertKey]model.Alert, len(active)), pending: make(map[alertKey]time.Time, len(pending))}
	for _, a := range active {
		st.active[alertKey{a.AlertType, a.Subject}] = a
	}
	for _, p := range pending {
		st.pending[alertKey{p.AlertType, p.Subject}] = p.Since
	}
	return st, true
}

// sustained, koşulun (tür+konu) en az d süredir sürdüğünü bildirir; d sıfırsa hemen true. Koşul ilk kez görülüyorsa
// başlangıcı now olarak kaydedilir (alert_pending): süre, server yeniden başlasa da kaldığı yerden sayılır. Kayıt
// yazılamazsa false döner ve bir sonraki raporda yeniden denenir (alert erken açılmaz).
func (e *Engine) sustained(ctx context.Context, st *hostState, alertType, subject, level string, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	k := alertKey{alertType, subject}
	since, ok := st.pending[k]
	if !ok {
		var err error
		since, err = e.pending.MarkPending(ctx, st.hostID, alertType, subject, level, e.now())
		if err != nil {
			slog.ErrorContext(ctx, "alert engine: mark pending condition", "host_id", st.hostID.String(), "alert_type", alertType, "err", err)
			return false
		}
		st.pending[k] = since
	}
	return e.now().Sub(since) >= d
}

// clearPending, koşulun bekleme kaydını siler (koşul kalktı ya da alert açıldı); kayıt yoksa hiçbir şey yapmaz.
func (e *Engine) clearPending(ctx context.Context, st *hostState, alertType, subject string) {
	k := alertKey{alertType, subject}
	if _, ok := st.pending[k]; !ok {
		return
	}
	if err := e.pending.ClearPending(ctx, st.hostID, alertType, subject); err != nil {
		slog.ErrorContext(ctx, "alert engine: clear pending condition", "host_id", st.hostID.String(), "alert_type", alertType, "err", err)
		return // kayıt kaldı: bir sonraki raporda yeniden denenir
	}
	delete(st.pending, k)
}

// durationOf, saniye cinsinden süreyi time.Duration'a çevirir (nil = 0, hemen).
func durationOf(seconds *int) time.Duration {
	if seconds == nil {
		return 0
	}
	return time.Duration(*seconds) * time.Second
}

// take, bu tür+subject'in aktif alert'ini durumdan çıkarıp döndürür (çözülmek üzere).
func (s *hostState) take(alertType, subject string) (model.Alert, bool) {
	k := alertKey{alertType, subject}
	a, ok := s.active[k]
	delete(s.active, k)
	return a, ok
}

// activeOf, bu türün aktif alert'leridir, subject sırasıyla.
func (s *hostState) activeOf(alertType string) []model.Alert {
	var out []model.Alert
	for k, a := range s.active {
		if k.alertType == alertType {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

func (e *Engine) evaluateMetrics(ctx context.Context, st *hostState, cpuPct, ramPct float64, disks []model.DiskUsage) {
	e.evaluate(ctx, st, model.MetricTypeCPU, cpuPct)
	e.evaluate(ctx, st, model.MetricTypeRAM, ramPct)
	// Hiç disk eşiği kalmadıysa açık disk alert'leri kapanır, rapor disk listesi taşımasa da: başka hiçbir şey onları
	// kapatamazdı.
	if t := st.thresholds.Metric(model.MetricTypeDisk); t.Base == nil && len(t.PerSubject) == 0 {
		e.resolveAll(ctx, st, model.MetricTypeDisk)
	}
	// BOŞ bir disk listesi hiçbir şeyi değiştirmez (bkz. evaluateDisks).
	if len(disks) == 0 {
		return
	}
	allMounts, selection, err := e.hosts.DiskAlertMounts(ctx, st.hostID)
	if err != nil {
		// Seçim olmadan hangi mount'ların istendiğini bilemeyiz; tahmin etmek yerine hiçbir şey yapma.
		slog.ErrorContext(ctx, "alert engine: read disk alert mounts", "host_id", st.hostID.String(), "err", err)
		return
	}
	e.evaluateDisks(ctx, st, disks, allMounts, selection)
	e.evaluateMissingMounts(ctx, st, disks, allMounts, selection)
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
func (e *Engine) evaluateDisks(ctx context.Context, st *hostState, disks []model.DiskUsage, allMounts bool, selection []string) {
	thresholds := st.thresholds.Metric(model.MetricTypeDisk)
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		return // açık alert'ler evaluateMetrics'te kapandı
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
		e.apply(ctx, st, model.MetricTypeDisk, d.Mount, d.UsedPct, threshold)
	}

	for _, a := range st.activeOf(model.MetricTypeDisk) {
		if _, still := evaluated[a.Subject]; !still {
			st.take(a.AlertType, a.Subject)
			e.resolveAndNotify(ctx, a, st.orgID, nil, nil)
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
func expectedMounts(st *hostState, allMounts bool, selection []string) map[string]struct{} {
	expected := map[string]struct{}{}
	if !allMounts {
		for _, m := range selection {
			expected[m] = struct{}{}
		}
		return expected
	}
	for m := range st.thresholds.Metric(model.MetricTypeDisk).PerSubject {
		expected[m] = struct{}{}
	}
	return expected
}

// evaluateMissingMounts, son missingMountReports raporun hiçbirinde listelenmeyen beklenen bir
// mount için critical disk_missing alert'i (Alert.Subject = mount) üretir; mount yeniden
// raporlanınca ya da beklenmez olunca çözer. evaluateDisks gibi boş bir raporu tamamen yok
// sayar: o bir toplama hatasıdır, "her mount kayboldu" değil.
func (e *Engine) evaluateMissingMounts(ctx context.Context, st *hostState, disks []model.DiskUsage, allMounts bool, selection []string) {
	hostID := st.hostID
	expected := expectedMounts(st, allMounts, selection)

	present := make(map[string]struct{}, len(disks))
	for _, d := range disks {
		present[d.Mount] = struct{}{}
	}
	stillOpen := map[string]struct{}{} // açık kalan disk_missing alert'leri: yeniden açılmaz
	for _, a := range st.activeOf(model.AlertTypeDiskMissing) {
		_, wanted := expected[a.Subject]
		_, back := present[a.Subject]
		if !wanted || back {
			st.take(a.AlertType, a.Subject)
			e.resolveAndNotify(ctx, a, st.orgID, nil, nil)
			continue
		}
		stillOpen[a.Subject] = struct{}{}
	}

	var candidates []string
	for m := range expected {
		_, ok := present[m]
		_, alreadyOpen := stillOpen[m]
		if !ok && !alreadyOpen {
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
		err := e.change(ctx, e.prepare(ctx, hostID, st.orgID, model.AlertLevelCritical), store.AlertEventOpened, func(tx Tx) (model.Alert, bool, error) {
			return tx.Alerts().CreateIfNoneActive(ctx, hostID, model.AlertTypeDiskMissing, m, model.AlertLevelCritical, nil, nil)
		})
		if err != nil {
			slog.ErrorContext(ctx, "alert engine: create disk_missing alert", "host_id", hostID.String(), "mount", m, "err", err)
		}
	}
}

// evaluate, sunucu geneli bir metriği (cpu, ram) eşiğine göre değerlendirir. Eşik kalmadıysa açık alert kapanır
// (çözüldü bildirimiyle): aksi halde hiçbir şey onu kapatamaz ve sonsuza dek açık görünürdü.
func (e *Engine) evaluate(ctx context.Context, st *hostState, metricType string, value float64) {
	threshold := st.thresholds.Metric(metricType).Base
	if threshold == nil {
		e.resolveAll(ctx, st, metricType)
		return
	}
	e.apply(ctx, st, metricType, "", value, *threshold)
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
	if st, ok := e.loadState(ctx, hostID, orgID); ok {
		e.evaluateDocker(ctx, st, containers)
	}
}

func (e *Engine) evaluateDocker(ctx context.Context, st *hostState, containers []model.DockerContainerReport) {
	// Hiç eşik kalmadıysa açık alert'ler kapanır, rapor container taşımasa da (başka hiçbir şey onları kapatamazdı).
	thresholds := st.thresholds.Metric(model.MetricTypeDockerRestart)
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		e.resolveAll(ctx, st, model.MetricTypeDockerRestart)
		return
	}
	if len(containers) == 0 {
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
		e.apply(ctx, st, model.MetricTypeDockerRestart, c.Name, float64(c.RestartCount), threshold)
	}

	for _, a := range st.activeOf(model.MetricTypeDockerRestart) {
		if _, stillThere := present[a.Subject]; !stillThere {
			st.take(a.AlertType, a.Subject)
			e.resolveAndNotify(ctx, a, st.orgID, nil, nil)
		}
	}
}

// apply, bir host+alert türü+subject için alert yaşam döngüsünü çözümlenmiş bir eşiğe göre çalıştırır. Alert türü
// çoğunlukla eşik türüyle aynıdır; service_restart eşiği service_restart_loop, time_offset eşiği time_sync alert'i üretir.
func (e *Engine) apply(ctx context.Context, st *hostState, metricType, subject string, value float64, threshold model.ThresholdConfig) {
	// Aktif alert: açık ya da onaylanmış. Onay "gördüm, sustur ama izle"dir: onaylanan alert yeni bir alert/bildirim
	// açılmasını engeller ve eşik altına inince çözülür (bkz. store: aktif alert).
	key := alertKey{metricType, subject}
	existing, hasActive := st.active[key]

	var level string
	switch {
	case value >= threshold.CriticalLevel:
		level = model.AlertLevelCritical
	case value >= threshold.WarningLevel:
		level = model.AlertLevelWarning
	}

	if level == "" {
		e.clearPending(ctx, st, metricType, subject)
		if hasActive {
			delete(st.active, key)
			// Çözülme okuması: eşiğin altına döndüğü andaki gerçek ölçüm ve uyarı eşiği — böylece
			// e-postadaki "Değer" alert'in son yükseltildiği eski, hâlâ eşik üstü okumayı değil,
			// artık gerçekten eşiğin altında olan güncel durumu gösterir.
			e.resolveAndNotify(ctx, existing, st.orgID, &value, &threshold.WarningLevel)
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
		e.changeLevel(ctx, st, existing, level, valuePtr, triggerPtr)
		return
	}
	// Süre koşulu: eşik kesintisiz threshold.DurationSeconds boyunca aşılmadıkça alert açılmaz (aktif alert'in seviye
	// değişimi beklemez; yukarıda).
	if !e.sustained(ctx, st, metricType, subject, level, durationOf(threshold.DurationSeconds)) {
		return
	}
	e.clearPending(ctx, st, metricType, subject)
	e.open(ctx, st, metricType, subject, level, valuePtr, triggerPtr)
}

// changeLevel, aktif alert'in seviyesini level yapar; seviye aynıysa hiçbir şey yapmaz (tekrar bildirimi önleme).
//
// Seviye değişimi (yükselme YA DA düşme) o alıcı için durumun gerçekten değiştiği anlamına gelir: uyarı mailini görüp
// "daha vaktim var" diyen biri kritiğe geçtiğinde, ya da tersine gereksiz yere endişelenmemesi için kritikten uyarıya
// düştüğünde bundan habersiz kalmamalı. Bu yüzden yeni seviyenin TÜM alıcılarına (daha önce bilgilendirilmiş olsalar
// bile) tekrar mail gider — açılış ve çözülme ile aynı kural. Onaylanmış bir alert YÜKSELİRSE onay da kalkar (durum
// ciddileşti, biri yeniden sahiplenmeli); düşüşte onay korunur.
func (e *Engine) changeLevel(ctx context.Context, st *hostState, existing model.Alert, level string, value, threshold *float64) {
	if existing.Level == level {
		return
	}
	key := alertKey{existing.AlertType, existing.Subject}
	reopen := reopensOnLevelChange(existing, level)
	err := e.change(ctx, e.prepare(ctx, st.hostID, st.orgID, level), store.AlertEventLevelChanged, func(tx Tx) (model.Alert, bool, error) {
		if err := tx.Alerts().UpdateLevel(ctx, existing.ID, level, value, threshold, reopen); err != nil {
			return model.Alert{}, false, err
		}
		changed := existing
		changed.Level, changed.Value, changed.Threshold = level, value, threshold
		if reopen {
			changed.Status, changed.AcknowledgedAt, changed.AcknowledgedBy = model.AlertStatusOpen, nil, nil
		}
		st.active[key] = changed
		return changed, true, nil
	})
	if err != nil {
		st.active[key] = existing
		slog.ErrorContext(ctx, "alert engine: update alert level", "alert_id", existing.ID.String(), "err", err)
	}
}

// open, yeni bir alert açar ve bildirimini kuyruğa yazar. Aynı anda başka bir değerlendirme açtıysa bildirim onundur;
// burada yalnızca seviyenin güncel olduğundan emin olunur.
func (e *Engine) open(ctx context.Context, st *hostState, alertType, subject, level string, value, threshold *float64) {
	hostID := st.hostID
	key := alertKey{alertType, subject}
	created := false
	err := e.change(ctx, e.prepare(ctx, hostID, st.orgID, level), store.AlertEventOpened, func(tx Tx) (model.Alert, bool, error) {
		alert, ok, err := tx.Alerts().CreateIfNoneActive(ctx, hostID, alertType, subject, level, value, threshold)
		created = ok
		if ok {
			st.active[key] = alert
		}
		return alert, ok, err
	})
	if err != nil {
		delete(st.active, key)
		slog.ErrorContext(ctx, "alert engine: create alert", "host_id", hostID.String(), "metric", alertType, "subject", subject, "err", err)
		return
	}
	if !created {
		if active, err := e.alerts.GetActiveSubject(ctx, hostID, alertType, subject); err == nil && active.Level != level {
			if err := e.alerts.UpdateLevel(ctx, active.ID, level, value, threshold, reopensOnLevelChange(active, level)); err != nil {
				slog.ErrorContext(ctx, "alert engine: update alert level", "alert_id", active.ID.String(), "err", err)
			}
		}
	}
}

// reopensOnLevelChange, onaylanmış bir alert'in yeni seviyeyle yeniden açılıp açılmayacağıdır: yalnızca seviye
// yükselirse (ör. uyarı → kritik). Düşüşte onay korunur — durum hafifledi, sahibi zaten ilgileniyor.
func reopensOnLevelChange(a model.Alert, newLevel string) bool {
	return a.Status == model.AlertStatusAcknowledged && model.LevelRank(newLevel) > model.LevelRank(a.Level)
}

// ResolveOffline, bir host yeniden rapor verdiğinde aktif (açık ya da onaylanmış) host_offline alert'ini kendiliğinden
// çözer ve "sunucu tekrar çevrimiçi" bildirimini kuyruğa yazar — her başarılı push/pull alımından sonra çağrılır.
// Offline alert yoksa (sıradan durum) tek bir okuma yapar.
func (e *Engine) ResolveOffline(ctx context.Context, hostID, orgID uuid.UUID) {
	active, err := e.alerts.GetActive(ctx, hostID, model.AlertTypeHostOffline)
	if errors.Is(err, store.ErrNotFound) {
		return // aktif değildi
	}
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: resolve offline alert", "host_id", hostID.String(), "err", err)
		return
	}
	e.resolveAndNotify(ctx, active, orgID, nil, nil)
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

	// Eşzamanlı açıldıysa (created=false) bildirimi o çağıran yapar.
	err = e.change(ctx, e.prepare(ctx, hostID, orgID, model.AlertLevelCritical), store.AlertEventOpened, func(tx Tx) (model.Alert, bool, error) {
		return tx.Alerts().CreateIfNoneActive(ctx, hostID, model.AlertTypeHostOffline, "", model.AlertLevelCritical, nil, nil)
	})
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: create offline alert", "host_id", hostID.String(), "err", err)
	}
}
