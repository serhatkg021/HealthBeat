package alertengine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
)

// ruleThresholds, durum kuralları ve (konu bazlı dahil) eşikleri olan bir eşik deposudur.
type ruleThresholds struct {
	rules      model.StatusRuleSet
	thresholds store.HostThresholds
}

func (r *ruleThresholds) ResolveHost(context.Context, uuid.UUID, uuid.UUID) (store.HostThresholds, error) {
	if r.thresholds == nil {
		return store.HostThresholds{}, nil
	}
	return r.thresholds, nil
}

func (r *ruleThresholds) ResolveStatusRules(context.Context, uuid.UUID, uuid.UUID) (model.StatusRuleSet, error) {
	return r.rules, nil
}

type statusEnv struct {
	t        *testing.T
	engine   *Engine
	alerts   *fakeAlerts
	hosts    *fakeHosts
	rules    *ruleThresholds
	previous []model.DiskUsage
	clock    time.Time
	host     uuid.UUID
}

func newStatusEnv(t *testing.T, rules model.StatusRuleSet) *statusEnv {
	t.Helper()
	s := &statusEnv{t: t, alerts: &fakeAlerts{}, rules: &ruleThresholds{rules: rules}, host: uuid.New(),
		clock: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	org := uuid.New()
	s.hosts = &fakeHosts{host: model.Host{ID: s.host, OrganizationID: org, Title: "web-1"}}
	s.engine = newEngineWith(Stores{
		Alerts: s.alerts, Pending: &fakePending{}, Thresholds: s.rules, Hosts: s.hosts,
		Metrics: fakeMetrics{previous: &s.previous}, Organizations: fakeOrgs{}, Recipients: fakeRecipients{},
		Tx: fakeTx{alerts: s.alerts, outbox: &fakeOutbox{}},
	}, []notify.Notifier{&fakeNotifier{channel: model.ChannelEmail}}, "", nil)
	s.engine.now = func() time.Time { return s.clock }
	return s
}

func (s *statusEnv) at(offset time.Duration, r Report) {
	s.t.Helper()
	s.clock = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).Add(offset)
	s.engine.EvaluateReport(context.Background(), s.host, s.hosts.host.OrganizationID, r)
}

// level, türün+konunun aktif alert'inin seviyesidir; "" = aktif alert yok.
func (s *statusEnv) level(alertType, subject string) string {
	s.alerts.mu.Lock()
	defer s.alerts.mu.Unlock()
	if a := s.alerts.active(s.host, alertType, subject, false); a != nil {
		return a.Level
	}
	return ""
}

// alert, türün+konunun aktif alert'idir (kopya); yoksa nil.
func (s *statusEnv) alert(alertType, subject string) *model.Alert {
	s.alerts.mu.Lock()
	defer s.alerts.mu.Unlock()
	if a := s.alerts.active(s.host, alertType, subject, false); a != nil {
		c := *a
		return &c
	}
	return nil
}

func (s *statusEnv) expect(alertType, subject, want string) {
	s.t.Helper()
	if got := s.level(alertType, subject); got != want {
		s.t.Errorf("%s/%s at %s: level %q, want %q", alertType, subject, s.clock.Format("15:04:05"), got, want)
	}
}

func rule(level string, seconds ...int) model.StatusRuleSetting {
	r := model.StatusRuleSetting{Level: level}
	if len(seconds) > 0 {
		r.DurationSeconds = &seconds[0]
	}
	return r
}

func bptr(v bool) *bool { return &v }

// Kural yoksa hiçbir durum alert'i açılmaz; kural açılınca açılır, kapatılınca çözülür.
func TestStatusAlertsFollowTheRule(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{})
	r := Report{HostInfo: &model.HostInfo{RebootRequired: bptr(true)},
		Containers: []model.DockerContainerReport{{Name: "db", Status: "running", Health: "unhealthy"}}}
	s.at(0, r)
	s.expect(model.AlertTypeRebootRequired, "", "")
	s.expect(model.AlertTypeContainerUnhealthy, "db", "")

	s.rules.rules = model.StatusRuleSet{model.RuleRebootRequired: rule(model.AlertLevelInfo), model.RuleContainerUnhealthy: rule(model.AlertLevelCritical)}
	s.at(time.Minute, r)
	s.expect(model.AlertTypeRebootRequired, "", model.AlertLevelInfo)
	s.expect(model.AlertTypeContainerUnhealthy, "db", model.AlertLevelCritical)

	// Seviye kuraldan gelir: değişince açık alert yeni seviyeye geçer.
	s.rules.rules[model.RuleRebootRequired] = rule(model.AlertLevelWarning)
	s.at(2*time.Minute, r)
	s.expect(model.AlertTypeRebootRequired, "", model.AlertLevelWarning)

	// Kapalı ("off" ya da hiç yok): açık alert'ler çözülür, veri raporda olmasa bile.
	s.rules.rules = model.StatusRuleSet{model.RuleRebootRequired: rule(model.RuleLevelOff)}
	s.at(3*time.Minute, Report{})
	s.expect(model.AlertTypeRebootRequired, "", "")
	s.expect(model.AlertTypeContainerUnhealthy, "db", "")
}

func TestServiceFailed(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{model.RuleServiceFailed: rule(model.AlertLevelCritical, 60)})
	t0 := s.clock
	failedAt := t0.Add(-30 * time.Second)
	s.hosts.services = []model.HostService{
		{Name: "nginx.service", Active: "failed", Since: &failedAt, Watched: true},
		{Name: "cron.service", Active: "failed", Since: &failedAt}, // izlenmiyor
		{Name: "ssh.service", Active: "active", Watched: true},
	}
	s.at(0, Report{})
	s.expect(model.AlertTypeServiceFailed, "nginx.service", "") // 30 sn: süre dolmadı
	s.at(31*time.Second, Report{})
	s.expect(model.AlertTypeServiceFailed, "nginx.service", model.AlertLevelCritical) // agent'ın "since"inden 61 sn
	s.expect(model.AlertTypeServiceFailed, "cron.service", "")

	s.hosts.services[0].Active = "active"
	s.at(time.Minute, Report{})
	s.expect(model.AlertTypeServiceFailed, "nginx.service", "")

	// "since" bilinmiyorsa süre bekleme kaydıyla sayılır; izlemeden çıkarılan servisin alert'i kapanır.
	s.hosts.services[0] = model.HostService{Name: "nginx.service", Active: "inactive", Watched: true}
	s.at(2*time.Minute, Report{})
	s.expect(model.AlertTypeServiceFailed, "nginx.service", "")
	s.at(3*time.Minute, Report{})
	s.expect(model.AlertTypeServiceFailed, "nginx.service", model.AlertLevelCritical)
	s.hosts.services[0].Watched = false
	s.at(4*time.Minute, Report{})
	s.expect(model.AlertTypeServiceFailed, "nginx.service", "")
}

func TestContainerHealthAndOOM(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{
		model.RuleContainerUnhealthy: rule(model.AlertLevelCritical),
		model.RuleContainerOOM:       rule(model.AlertLevelWarning),
	})
	containers := func(health string, oom *bool, names ...string) []model.DockerContainerReport {
		var out []model.DockerContainerReport
		for _, n := range names {
			out = append(out, model.DockerContainerReport{Name: n, Status: "running", Health: health, OOMKilled: oom})
		}
		return out
	}
	s.at(0, Report{Containers: containers("unhealthy", bptr(true), "api", "db")})
	s.expect(model.AlertTypeContainerUnhealthy, "api", model.AlertLevelCritical)
	s.expect(model.AlertTypeContainerOOM, "db", model.AlertLevelWarning)

	s.at(time.Minute, Report{}) // boş liste: toplama hatası olabilir, hiçbir şey kapanmaz
	s.expect(model.AlertTypeContainerUnhealthy, "api", model.AlertLevelCritical)

	s.at(2*time.Minute, Report{Containers: append(containers("healthy", bptr(false), "api"), containers("starting", nil, "x")...)})
	s.expect(model.AlertTypeContainerUnhealthy, "api", "")
	s.expect(model.AlertTypeContainerOOM, "api", "")
	s.expect(model.AlertTypeContainerUnhealthy, "db", "") // listeden kalktı
	s.expect(model.AlertTypeContainerOOM, "db", "")
}

func TestOOMKill(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{model.RuleOOMKill: rule(model.AlertLevelWarning, 1800)})
	inc := func(at time.Duration) Report {
		t := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).Add(at)
		return Report{State: &model.SystemState{OOMKills: ptrU(3), OOMLastIncreaseAt: &t}, OOMIncreased: true}
	}
	quiet := func(lastAt time.Duration) Report {
		t := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).Add(lastAt)
		return Report{State: &model.SystemState{OOMKills: ptrU(3), OOMLastIncreaseAt: &t}}
	}
	s.at(0, inc(0))
	s.expect(model.AlertTypeOOMKill, "", model.AlertLevelWarning)
	s.at(20*time.Minute, quiet(0))
	s.expect(model.AlertTypeOOMKill, "", model.AlertLevelWarning) // 30 dk dolmadı
	s.at(25*time.Minute, inc(25*time.Minute))                     // yeni artış: süre baştan
	s.at(50*time.Minute, quiet(25*time.Minute))
	s.expect(model.AlertTypeOOMKill, "", model.AlertLevelWarning)
	s.at(56*time.Minute, Report{}) // bilinmiyor: değişmez
	s.expect(model.AlertTypeOOMKill, "", model.AlertLevelWarning)
	s.at(56*time.Minute, quiet(25*time.Minute))
	s.expect(model.AlertTypeOOMKill, "", "")

	// Süre yoksa artış olmayan ilk raporda kapanır.
	s.rules.rules[model.RuleOOMKill] = rule(model.AlertLevelCritical)
	s.at(60*time.Minute, inc(60*time.Minute))
	s.expect(model.AlertTypeOOMKill, "", model.AlertLevelCritical)
	s.at(61*time.Minute, quiet(60*time.Minute))
	s.expect(model.AlertTypeOOMKill, "", "")
}

func ptrU(v uint64) *uint64 { return &v }

func TestFSReadOnlyOnlyOnTransition(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{model.RuleFSReadOnly: rule(model.AlertLevelCritical)})
	disk := func(mount string, ro *bool) model.DiskUsage { return model.DiskUsage{Mount: mount, ReadOnly: ro} }
	s.previous = []model.DiskUsage{disk("/data", bptr(false)), disk("/snap", bptr(true))}
	r := Report{Disks: []model.DiskUsage{disk("/data", bptr(true)), disk("/snap", bptr(true)), disk("/new", bptr(true)), disk("/old", nil)}}
	s.at(0, r)
	s.expect(model.AlertTypeFSReadOnly, "/data", model.AlertLevelCritical) // yazılabilirdi, salt okunur oldu
	s.expect(model.AlertTypeFSReadOnly, "/snap", "")                       // hep salt okunur
	s.expect(model.AlertTypeFSReadOnly, "/new", "")                        // önceki durum bilinmiyor

	s.previous = r.Disks // açık alert önceki rapora bakmadan sürer
	s.at(time.Minute, r)
	s.expect(model.AlertTypeFSReadOnly, "/data", model.AlertLevelCritical)
	s.at(2*time.Minute, Report{}) // boş liste: değişmez
	s.expect(model.AlertTypeFSReadOnly, "/data", model.AlertLevelCritical)
	s.at(3*time.Minute, Report{Disks: []model.DiskUsage{disk("/data", nil)}}) // eski agent: bilinmiyor, kalır
	s.expect(model.AlertTypeFSReadOnly, "/data", model.AlertLevelCritical)
	s.at(4*time.Minute, Report{Disks: []model.DiskUsage{disk("/data", bptr(false))}})
	s.expect(model.AlertTypeFSReadOnly, "/data", "")
}

func TestRAIDStates(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{
		model.RuleRAIDDegraded:   rule(model.AlertLevelCritical),
		model.RuleRAIDRebuilding: rule(model.AlertLevelWarning),
	})
	raid := func(arrays ...model.RAID) Report { return Report{State: &model.SystemState{RAID: arrays}} }
	s.at(0, raid(model.RAID{Name: "md0", State: "degraded"}, model.RAID{Name: "md1", State: "inactive"}, model.RAID{Name: "md2", State: "checking"}))
	s.expect(model.AlertTypeRAIDDegraded, "md0", model.AlertLevelCritical)
	s.expect(model.AlertTypeRAIDDegraded, "md1", model.AlertLevelCritical)
	s.expect(model.AlertTypeRAIDDegraded, "md2", "") // rutin tarama
	s.at(time.Minute, raid(model.RAID{Name: "md0", State: "recovering"}, model.RAID{Name: "md1", State: "inactive"}))
	s.expect(model.AlertTypeRAIDDegraded, "md0", model.AlertLevelWarning) // bozuk → yeniden kuruluyor: seviye değişimi
	s.at(2*time.Minute, Report{})                                         // RAID listesi yok: değişmez
	s.expect(model.AlertTypeRAIDDegraded, "md1", model.AlertLevelCritical)
	s.at(3*time.Minute, raid(model.RAID{Name: "md0", State: "clean"}))
	s.expect(model.AlertTypeRAIDDegraded, "md0", "")
	s.expect(model.AlertTypeRAIDDegraded, "md1", "") // listeden kalktı

	// Yeniden kurulum kuralı kapalıysa yeniden kurulan dizinin alert'i kapanır.
	delete(s.rules.rules, model.RuleRAIDRebuilding)
	s.at(4*time.Minute, raid(model.RAID{Name: "md0", State: "degraded"}))
	s.expect(model.AlertTypeRAIDDegraded, "md0", model.AlertLevelCritical)
	s.at(5*time.Minute, raid(model.RAID{Name: "md0", State: "resyncing"}))
	s.expect(model.AlertTypeRAIDDegraded, "md0", "")
}

func TestTimeSync(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{
		model.RuleTimeUnsynced: rule(model.AlertLevelWarning, 1800),
		model.RuleTimeSource:   rule(model.AlertLevelInfo),
	})
	// Protokol 3 agent: envanterdeki bayrak; süre bekleme kaydıyla sayılır.
	v3 := Report{HostInfo: &model.HostInfo{TimeSynced: bptr(false)}}
	s.at(0, v3)
	s.at(29*time.Minute, v3)
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectUnsynced, "")
	s.at(30*time.Minute, v3)
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectUnsynced, model.AlertLevelWarning)
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectSource, "") // senkron değilken kaynak sorunu ayrıca açılmaz

	// Protokol 4: ayrıntı envanterdeki bayrağa üstün gelir.
	now := time.Date(2026, 10, 7, 12, 31, 0, 0, time.UTC)
	ok := model.TimeSync{Synchronized: bptr(true), Stratum: ptrI(2), PollS: ptrI(64), LastSync: now.Add(-time.Minute).Format(time.RFC3339),
		Sources: []model.TimeSource{{Name: "a", State: "selected", Reach: ptrI(255)}}}
	v4 := func(ts model.TimeSync) Report {
		return Report{State: &model.SystemState{TimeSync: &ts}, HostInfo: &model.HostInfo{TimeSynced: bptr(false)}}
	}
	s.at(31*time.Minute, v4(ok))
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectUnsynced, "")
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectSource, "")

	// Senkron değilken kaynak sorunu ayrıca açılmaz (senkron değil alert'i yeter).
	broken := ok
	broken.Synchronized, broken.Ignored = bptr(false), bptr(true)
	s.at(31*time.Minute, v4(broken))
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectSource, "")
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectUnsynced, "") // önceki rapor senkrondu: süre yeniden başladı
	s.at(31*time.Minute, v4(ok))

	// Saat farkı (offset) alert'i eşikle değerlendirilir (numeric.go); durum değerlendirmesi ona dokunmaz. Eşik var,
	// raporda saat farkı yok: alert olduğu gibi kalmalı.
	offsetLevels := model.ThresholdConfig{MetricType: model.MetricTypeTimeOffset, WarningLevel: 100, CriticalLevel: 1000}
	s.rules.thresholds = store.HostThresholds{model.MetricTypeTimeOffset: {Base: &offsetLevels}}
	s.alerts.mu.Lock()
	s.alerts.alerts = append(s.alerts.alerts, &model.Alert{ID: uuid.New(), HostID: s.host, AlertType: model.AlertTypeTimeSync,
		Subject: model.TimeSyncSubjectOffset, Level: model.AlertLevelWarning, Status: model.AlertStatusOpen})
	s.alerts.mu.Unlock()
	s.at(31*time.Minute, v4(ok))
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, model.AlertLevelWarning)

	for name, mutate := range map[string]func(ts *model.TimeSync){
		"ignored":    func(ts *model.TimeSync) { ts.Ignored = bptr(true) },
		"leap alarm": func(ts *model.TimeSync) { ts.Leap = "alarm" },
		"stratum 16": func(ts *model.TimeSync) { ts.Stratum = ptrI(16) },
		"all unreachable": func(ts *model.TimeSync) {
			ts.Sources = []model.TimeSource{{Name: "a", State: "candidate", Reach: ptrI(0)}}
		},
		"stale sync": func(ts *model.TimeSync) { ts.LastSync = now.Add(-200 * time.Second).Format(time.RFC3339) },
	} {
		ts := ok
		ts.Sources = append([]model.TimeSource(nil), ok.Sources...)
		mutate(&ts)
		s.at(31*time.Minute, v4(ts))
		if got := s.level(model.AlertTypeTimeSync, model.TimeSyncSubjectSource); got != model.AlertLevelInfo {
			t.Errorf("%s: source alert %q, want info", name, got)
		}
		s.at(31*time.Minute, v4(ok))
		if got := s.level(model.AlertTypeTimeSync, model.TimeSyncSubjectSource); got != "" {
			t.Errorf("%s: source alert not resolved: %q", name, got)
		}
	}
}

func ptrI(v int) *int { return &v }

func TestRebootAndSecurityUpdates(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{
		model.RuleRebootRequired:  rule(model.AlertLevelInfo),
		model.RuleSecurityUpdates: rule(model.AlertLevelWarning, 7*24*3600),
	})
	updates := func(security int) *model.SystemState {
		return &model.SystemState{Updates: &model.Updates{Pending: 10, Security: security}}
	}
	s.at(0, Report{HostInfo: &model.HostInfo{RebootRequired: bptr(true)}, State: updates(2)})
	s.expect(model.AlertTypeRebootRequired, "", model.AlertLevelInfo)
	s.expect(model.AlertTypeSecurityUpdates, "", "")
	s.at(7*24*time.Hour, Report{HostInfo: &model.HostInfo{}, State: updates(1)}) // bayrak bilinmiyor: değişmez
	s.expect(model.AlertTypeRebootRequired, "", model.AlertLevelInfo)
	s.expect(model.AlertTypeSecurityUpdates, "", model.AlertLevelWarning)
	s.at(7*24*time.Hour+time.Minute, Report{HostInfo: &model.HostInfo{RebootRequired: bptr(false)}, State: updates(0)})
	s.expect(model.AlertTypeRebootRequired, "", "")
	s.expect(model.AlertTypeSecurityUpdates, "", "")
}
