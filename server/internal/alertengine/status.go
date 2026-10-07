package alertengine

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"healthbeat-server/internal/model"
)

// Durum alert'leri (protokol 4): bir değer değil bir durum bildirirler (servis çalışmıyor, RAID bozuk …). Her biri bir
// durum kuralına bağlıdır (model.StatusRules): kural kapalıysa alert üretmez ve açık alert'leri bir sonraki raporda
// kapanır; seviye ve süre kuraldan gelir.
//
// Veri raporda yoksa (eski agent, toplama hatası) hiçbir şey değişmez: açık alert açık kalır, yeni alert açılmaz.
// Liste raporda varsa ama bir konu (container, mount, RAID dizisi, servis) listeden kalktıysa o konunun alert'i kapanır.

// condition, bir konunun bu rapordaki durumudur.
type condition struct {
	level    string        // koşul sürüyorsa kuralın seviyesi; "" = koşul yok (alert kapanır)
	duration time.Duration // açılmadan önce koşulun sürmesi gereken süre
	since    *time.Time    // koşulun başladığı an veriden biliniyorsa (servis); nil = bekleme kaydıyla sayılır
	keep     bool          // bu konu hakkında bilgi yok: alert'ine dokunulmaz
}

// reconcile, bir alert türünün konularını conds'a göre günceller. closeMissing true ise (liste tamdır) conds'ta
// olmayan konuların aktif alert'leri kapanır.
func (e *Engine) reconcile(ctx context.Context, st *hostState, alertType string, conds map[string]condition, closeMissing bool) {
	subjects := make([]string, 0, len(conds))
	for subject := range conds {
		subjects = append(subjects, subject)
	}
	sort.Strings(subjects) // bildirimler ve kilitler belirli bir sırayla
	for _, subject := range subjects {
		c := conds[subject]
		if c.keep {
			continue
		}
		key := alertKey{alertType, subject}
		existing, active := st.active[key]
		if c.level == "" {
			e.clearPending(ctx, st, alertType, subject)
			if active {
				st.take(alertType, subject)
				e.resolveAndNotify(ctx, existing, st.orgID, nil, nil)
			}
			continue
		}
		if active {
			e.clearPending(ctx, st, alertType, subject)
			e.changeLevel(ctx, st, existing, c.level, nil, nil)
			continue
		}
		if c.since != nil {
			if e.now().Sub(*c.since) < c.duration {
				continue
			}
		} else if !e.sustained(ctx, st, alertType, subject, c.level, c.duration) {
			continue
		}
		e.clearPending(ctx, st, alertType, subject)
		e.open(ctx, st, alertType, subject, c.level, nil, nil)
	}
	if !closeMissing {
		return
	}
	for _, a := range st.activeOf(alertType) {
		if _, ok := conds[a.Subject]; !ok {
			st.take(a.AlertType, a.Subject)
			e.resolveAndNotify(ctx, a, st.orgID, nil, nil)
		}
	}
	for k := range st.pending {
		if _, ok := conds[k.subject]; k.alertType == alertType && !ok {
			e.clearPending(ctx, st, k.alertType, k.subject)
		}
	}
}

// resolveAll, bir türün bütün aktif alert'lerini ve bekleme kayıtlarını kapatır (kural kapatıldı).
func (e *Engine) resolveAll(ctx context.Context, st *hostState, alertType string) {
	e.reconcile(ctx, st, alertType, map[string]condition{}, true)
}

// holds, kural açıksa koşulun seviyesini ve süresini döndürür; kapalıysa ya da koşul yoksa boş koşul.
func holds(rule model.StatusRuleSetting, ok bool) condition {
	if !ok || !rule.Enabled() {
		return condition{}
	}
	return condition{level: rule.Level, duration: durationOf(rule.DurationSeconds)}
}

func (e *Engine) evaluateStatus(ctx context.Context, st *hostState, r Report) {
	e.evaluateServices(ctx, st)
	e.evaluateContainerStatus(ctx, st, r.Containers)
	e.evaluateOOMKills(ctx, st, r)
	e.evaluateReadOnly(ctx, st, r.Disks)
	e.evaluateRAID(ctx, st, r.State)
	e.evaluateTimeSync(ctx, st, r)
	e.evaluateFlag(ctx, st, model.RuleRebootRequired, model.AlertTypeRebootRequired, rebootRequired(r.HostInfo))
	e.evaluateFlag(ctx, st, model.RuleSecurityUpdates, model.AlertTypeSecurityUpdates, securityPending(r.State))
}

// evaluateServices: izlenen servis çalışmıyorsa (failed/inactive) service_failed. Süre, agent'ın bildirdiği "bu
// duruma geçtiği an"dan sayılır. Servis tablosu yalnızca kural açıkken ya da açık alert varken okunur.
func (e *Engine) evaluateServices(ctx context.Context, st *hostState) {
	rule := st.rules.Rule(model.RuleServiceFailed)
	if !rule.Enabled() {
		e.resolveAll(ctx, st, model.AlertTypeServiceFailed)
		return
	}
	services, err := e.hosts.Services(ctx, st.hostID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: read services", "host_id", st.hostID.String(), "err", err)
		return
	}
	conds := map[string]condition{}
	for _, s := range services {
		if !s.Watched {
			continue // izlenmeyen servisin açık alert'i aşağıda (closeMissing) kapanır
		}
		c := holds(rule, s.Active == "failed" || s.Active == "inactive")
		c.since = s.Since
		conds[s.Name] = c
	}
	e.reconcile(ctx, st, model.AlertTypeServiceFailed, conds, true)
}

// evaluateContainerStatus: healthcheck "unhealthy" → container_unhealthy; bellek yetmediği için öldürüldü →
// container_oom. Boş liste hiçbir şeyi değiştirmez (Docker toplaması başarısız olmuş olabilir).
func (e *Engine) evaluateContainerStatus(ctx context.Context, st *hostState, containers []model.DockerContainerReport) {
	unhealthy, oom := st.rules.Rule(model.RuleContainerUnhealthy), st.rules.Rule(model.RuleContainerOOM)
	if !unhealthy.Enabled() {
		e.resolveAll(ctx, st, model.AlertTypeContainerUnhealthy)
	}
	if !oom.Enabled() {
		e.resolveAll(ctx, st, model.AlertTypeContainerOOM)
	}
	if len(containers) == 0 {
		return
	}
	if unhealthy.Enabled() {
		conds := map[string]condition{}
		for _, c := range containers {
			conds[c.Name] = holds(unhealthy, c.Health == "unhealthy")
		}
		e.reconcile(ctx, st, model.AlertTypeContainerUnhealthy, conds, true)
	}
	if oom.Enabled() {
		conds := map[string]condition{}
		for _, c := range containers {
			conds[c.Name] = holds(oom, c.OOMKilled != nil && *c.OOMKilled) // anlık olay: kurala süre verilmez
		}
		e.reconcile(ctx, st, model.AlertTypeContainerOOM, conds, true)
	}
}

// evaluateOOMKills: çekirdeğin OOM sayacı bu raporda arttıysa oom_kill açılır; kuralın süresi kadar yeni artış
// olmazsa kapanır (süre yoksa artış olmayan ilk raporda).
func (e *Engine) evaluateOOMKills(ctx context.Context, st *hostState, r Report) {
	rule := st.rules.Rule(model.RuleOOMKill)
	if !rule.Enabled() {
		e.resolveAll(ctx, st, model.AlertTypeOOMKill)
		return
	}
	if r.State == nil || r.State.OOMKills == nil {
		return // bilinmiyor
	}
	existing, active := st.active[alertKey{model.AlertTypeOOMKill, ""}]
	switch {
	case r.OOMIncreased && active:
		e.changeLevel(ctx, st, existing, rule.Level, nil, nil)
	case r.OOMIncreased:
		e.open(ctx, st, model.AlertTypeOOMKill, "", rule.Level, nil, nil)
	case active:
		last := r.State.OOMLastIncreaseAt
		if last == nil || e.now().Sub(*last) >= durationOf(rule.DurationSeconds) {
			st.take(model.AlertTypeOOMKill, "")
			e.resolveAndNotify(ctx, existing, st.orgID, nil, nil)
		} else {
			e.changeLevel(ctx, st, existing, rule.Level, nil, nil)
		}
	}
}

// evaluateReadOnly: yazılabilir bir mount salt okunur olunca fs_readonly (disk hatasında çekirdek böyle yapar). Hep
// salt okunur bağlı mount'lar alert üretmez: önceki raporda yazılabilir olması gerekir. Önceki rapor yalnızca yeni bir
// salt okunur mount görülünce okunur.
func (e *Engine) evaluateReadOnly(ctx context.Context, st *hostState, disks []model.DiskUsage) {
	rule := st.rules.Rule(model.RuleFSReadOnly)
	if !rule.Enabled() {
		e.resolveAll(ctx, st, model.AlertTypeFSReadOnly)
		return
	}
	if len(disks) == 0 {
		return
	}
	var previous map[string]bool // mount -> salt okunur; nil = henüz okunmadı
	conds := map[string]condition{}
	for _, d := range disks {
		switch {
		case d.ReadOnly == nil:
			conds[d.Mount] = condition{keep: true} // eski agent: bilinmiyor
		case !*d.ReadOnly:
			conds[d.Mount] = condition{}
		default:
			if _, active := st.active[alertKey{model.AlertTypeFSReadOnly, d.Mount}]; active {
				conds[d.Mount] = holds(rule, true)
				continue
			}
			if previous == nil {
				previous = e.previousReadOnly(ctx, st)
			}
			if wasRO, known := previous[d.Mount]; known && !wasRO {
				conds[d.Mount] = holds(rule, true)
			} else {
				conds[d.Mount] = condition{keep: true} // geçiş yok: alert açılmaz (zaten açık da değil)
			}
		}
	}
	e.reconcile(ctx, st, model.AlertTypeFSReadOnly, conds, true)
}

// previousReadOnly, bir önceki raporun mount'larının salt okunur bilgisidir (bilinmeyenler yok). Okunamazsa boş:
// geçiş görülmez, alert açılmaz.
func (e *Engine) previousReadOnly(ctx context.Context, st *hostState) map[string]bool {
	out := map[string]bool{}
	disks, err := e.metrics.PreviousDisks(ctx, st.hostID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: read previous report", "host_id", st.hostID.String(), "err", err)
		return out
	}
	for _, d := range disks {
		if d.ReadOnly != nil {
			out[d.Mount] = *d.ReadOnly
		}
	}
	return out
}

// RAID durumları: bozuk (degraded, inactive) ve yeniden kuruluyor (recovering, resyncing, reshaping). clean ve checking
// (rutin tarama) sorun değildir. İkisi de raid_degraded alert'idir (konu dizi adı); geçiş seviye değişimidir.
var (
	raidDegradedStates   = map[string]bool{"degraded": true, "inactive": true, "failed": true}
	raidRebuildingStates = map[string]bool{"recovering": true, "resyncing": true, "reshaping": true}
)

func (e *Engine) evaluateRAID(ctx context.Context, st *hostState, state *model.SystemState) {
	degraded, rebuilding := st.rules.Rule(model.RuleRAIDDegraded), st.rules.Rule(model.RuleRAIDRebuilding)
	if !degraded.Enabled() && !rebuilding.Enabled() {
		e.resolveAll(ctx, st, model.AlertTypeRAIDDegraded)
		return
	}
	if state == nil || len(state.RAID) == 0 {
		return // bilinmiyor (dizi yoksa agent listeyi göndermez)
	}
	conds := map[string]condition{}
	for _, a := range state.RAID {
		switch {
		case raidDegradedStates[a.State]:
			conds[a.Name] = holds(degraded, true)
		case raidRebuildingStates[a.State]:
			conds[a.Name] = holds(rebuilding, true)
		default:
			conds[a.Name] = condition{}
		}
	}
	e.reconcile(ctx, st, model.AlertTypeRAIDDegraded, conds, true)
}

// evaluateTimeSync: saat senkron değilse time_sync/unsynced (protokol 3 agent'ların envanterindeki bayraktan da);
// senkron ama kaynak sorunluysa time_sync/source. Saat farkı (time_sync/offset) eşikle değerlendirilir (10c).
func (e *Engine) evaluateTimeSync(ctx context.Context, st *hostState, r Report) {
	unsynced, source := st.rules.Rule(model.RuleTimeUnsynced), st.rules.Rule(model.RuleTimeSource)
	var ts *model.TimeSync
	if r.State != nil {
		ts = r.State.TimeSync
	}
	var synced *bool
	switch {
	case ts != nil && ts.Synchronized != nil:
		synced = ts.Synchronized
	case r.HostInfo != nil:
		synced = r.HostInfo.TimeSynced
	}
	conds := map[string]condition{}
	switch {
	case !unsynced.Enabled():
		conds[model.TimeSyncSubjectUnsynced] = condition{}
	case synced != nil:
		conds[model.TimeSyncSubjectUnsynced] = holds(unsynced, !*synced)
	}
	switch {
	case !source.Enabled():
		conds[model.TimeSyncSubjectSource] = condition{}
	case ts != nil && synced != nil:
		conds[model.TimeSyncSubjectSource] = holds(source, *synced && timeSourceProblem(ts, e.now()))
	}
	e.reconcile(ctx, st, model.AlertTypeTimeSync, conds, false) // konular sabit; offset 10c'nin
}

// timeSourceProblem, saat senkron görünse de kaynağın sorunlu olduğudur: son yanıt geçersiz sayıldı, leap alarmı,
// stratum 16 (senkronize olmayan kaynak), bütün kaynaklara ulaşılamıyor ya da son senkron sorgu aralığının iki katından
// eski.
func timeSourceProblem(ts *model.TimeSync, now time.Time) bool {
	if (ts.Ignored != nil && *ts.Ignored) || ts.Leap == "alarm" || (ts.Stratum != nil && *ts.Stratum >= 16) {
		return true
	}
	if len(ts.Sources) > 0 {
		unreachable := 0
		for _, s := range ts.Sources {
			if s.State == "unreachable" || (s.Reach != nil && *s.Reach == 0) {
				unreachable++
			}
		}
		if unreachable == len(ts.Sources) {
			return true
		}
	}
	if ts.PollS != nil && *ts.PollS > 0 {
		if last, err := time.Parse(time.RFC3339, ts.LastSync); err == nil && now.Sub(last) > 2*time.Duration(*ts.PollS)*time.Second {
			return true
		}
	}
	return false
}

// evaluateFlag, sunucu geneli bir bayrağın (nil = bilinmiyor) alert'idir.
func (e *Engine) evaluateFlag(ctx context.Context, st *hostState, ruleName, alertType string, flag *bool) {
	rule := st.rules.Rule(ruleName)
	if !rule.Enabled() {
		e.resolveAll(ctx, st, alertType)
		return
	}
	if flag == nil {
		return
	}
	e.reconcile(ctx, st, alertType, map[string]condition{"": holds(rule, *flag)}, false)
}

func rebootRequired(h *model.HostInfo) *bool {
	if h == nil {
		return nil
	}
	return h.RebootRequired
}

func securityPending(s *model.SystemState) *bool {
	if s == nil || s.Updates == nil {
		return nil
	}
	v := s.Updates.Security > 0
	return &v
}
