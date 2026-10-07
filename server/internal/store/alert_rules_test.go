package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

// Durum kuralı çözümlemesi eşiklerle aynıdır: sunucu > organizasyon > üst organizasyon (en yakın) > genel; hiç kural
// yoksa kapalı. "off" üst kapsamdaki kuralı kapatır.
func TestStatusRulesResolvePrecedence(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	parent := testdb.Org(t, pool, "holding")
	child := testdb.ChildOrg(t, pool, "acme", parent)
	other := testdb.Org(t, pool, "other")
	host := testdb.PushHost(t, pool, child, "web-1", "h")
	otherHost := testdb.PushHost(t, pool, other, "o-1", "h")

	resolve := func(h, org uuid.UUID) model.StatusRuleSet {
		t.Helper()
		set, err := th.ResolveStatusRules(ctx, h, org)
		if err != nil {
			t.Fatal(err)
		}
		return set
	}
	if set := resolve(host, child); len(set) != 0 || set.Rule(model.RuleServiceFailed).Enabled() {
		t.Fatalf("no rules: %v, want everything off", set)
	}

	set := func(org *uuid.UUID, changes model.StatusRuleChanges) {
		t.Helper()
		if err := th.SetStatusRules(ctx, org, changes); err != nil {
			t.Fatal(err)
		}
	}
	set(nil, model.StatusRuleChanges{
		model.RuleServiceFailed:  {Level: model.AlertLevelWarning, DurationSeconds: ptr(60)},
		model.RuleRebootRequired: {Level: model.AlertLevelInfo},
	})
	set(&parent, model.StatusRuleChanges{
		model.RuleServiceFailed: {Level: model.AlertLevelCritical, DurationSeconds: ptr(120)},
		model.RuleOOMKill:       {Level: model.AlertLevelWarning},
	})
	set(&child, model.StatusRuleChanges{
		model.RuleRebootRequired: {Level: model.RuleLevelOff},
		model.RuleOOMKill:        {Level: model.AlertLevelCritical},
	})

	got := resolve(host, child)
	if r := got.Rule(model.RuleServiceFailed); r.Level != model.AlertLevelCritical || *r.DurationSeconds != 120 {
		t.Errorf("service_failed = %+v, want the parent's critical/120", r)
	}
	if got.Rule(model.RuleRebootRequired).Enabled() {
		t.Error("reboot_required is on; the child organization turned it off")
	}
	if r := got.Rule(model.RuleOOMKill); r.Level != model.AlertLevelCritical {
		t.Errorf("oom_kill = %+v, want the child's critical over the parent's warning (nearest wins)", r)
	}
	if r := resolve(otherHost, other).Rule(model.RuleServiceFailed); r.Level != model.AlertLevelWarning || *r.DurationSeconds != 60 {
		t.Errorf("other org's service_failed = %+v, want the global warning/60", r)
	}

	// Sunucunun kendi kuralı her şeyi geçer; kaldırılınca (nil) yeniden miras alır.
	if err := th.SetHostStatusRules(ctx, host, model.StatusRuleChanges{model.RuleServiceFailed: {Level: model.AlertLevelInfo}}); err != nil {
		t.Fatal(err)
	}
	if r := resolve(host, child).Rule(model.RuleServiceFailed); r.Level != model.AlertLevelInfo || r.DurationSeconds != nil {
		t.Errorf("host rule = %+v, want info without a duration", r)
	}
	defaults, err := th.StatusRuleDefaultsFor(ctx, child)
	if err != nil || defaults.Rule(model.RuleServiceFailed).Level != model.AlertLevelCritical || defaults.Rule(model.RuleRebootRequired).Level != model.RuleLevelOff ||
		defaults.Rule(model.RuleOOMKill).Level != model.AlertLevelCritical {
		t.Fatalf("defaults for the child = %v, %v", defaults, err)
	}
	own, err := th.HostStatusRules(ctx, host)
	if err != nil || len(own) != 1 {
		t.Fatalf("host's own rules = %v, %v", own, err)
	}
	if err := th.SetHostStatusRules(ctx, host, model.StatusRuleChanges{model.RuleServiceFailed: nil}); err != nil {
		t.Fatal(err)
	}
	if r := resolve(host, child).Rule(model.RuleServiceFailed); r.Level != model.AlertLevelCritical {
		t.Errorf("after removing the host rule: %+v, want the parent's critical", r)
	}

	// Listeleme: sunucu kuralları hariç; orgIDs verilirse yalnızca genel + o organizasyonlar.
	all, err := th.ListStatusRules(ctx, nil)
	if err != nil || len(all) != 6 {
		t.Fatalf("list all = %d rules, %v; want 6", len(all), err)
	}
	scoped, err := th.ListStatusRules(ctx, []uuid.UUID{child})
	if err != nil || len(scoped) != 4 {
		t.Fatalf("list for the child = %+v, %v; want the 2 global + 2 child rules", scoped, err)
	}
	none, err := th.ListStatusRules(ctx, []uuid.UUID{})
	if err != nil || len(none) != 2 {
		t.Fatalf("list without organizations = %d, %v; want only the 2 global rules", len(none), err)
	}

	// Hatalar: olmayan organizasyon/sunucu, veritabanının reddettiği süre; reddedilen yazma hiçbir şeyi değiştirmez.
	missing := uuid.New()
	if err := th.SetStatusRules(ctx, &missing, model.StatusRuleChanges{model.RuleOOMKill: {Level: model.AlertLevelWarning}}); !errors.Is(err, store.ErrOrganizationMissing) {
		t.Errorf("unknown organization: %v", err)
	}
	if err := th.SetHostStatusRules(ctx, missing, model.StatusRuleChanges{model.RuleOOMKill: {Level: model.AlertLevelWarning}}); !errors.Is(err, store.ErrHostMissing) {
		t.Errorf("unknown host: %v", err)
	}
	err = th.SetStatusRules(ctx, nil, model.StatusRuleChanges{
		model.RuleContainerUnhealthy: {Level: model.AlertLevelCritical},
		model.RuleFSReadOnly:         {Level: model.AlertLevelCritical, DurationSeconds: ptr(5)},
	})
	if !errors.Is(err, store.ErrStatusRuleInvalid) {
		t.Errorf("duration on fs_readonly: %v, want ErrStatusRuleInvalid", err)
	}
	if resolve(otherHost, other).Rule(model.RuleContainerUnhealthy).Enabled() {
		t.Error("a rejected write left container_unhealthy behind")
	}
}

// Eşiklerin süresi yazılır, okunur ve çözümlemeye taşınır; varsayılan eşiğin süresi ayrıca değiştirilip kaldırılabilir.
func TestThresholdDurations(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	org := testdb.Org(t, pool, "o")
	host := testdb.PushHost(t, pool, org, "h", "x")

	def, err := th.Create(ctx, store.CreateThresholdParams{MetricType: model.MetricTypeDiskLatency, WarningLevel: 30, CriticalLevel: 50, DurationSeconds: ptr(600)})
	if err != nil || def.DurationSeconds == nil || *def.DurationSeconds != 600 {
		t.Fatalf("create = %+v, %v", def, err)
	}
	// Seviyeyi değiştirmek süreye dokunmaz; SetDuration ile değişir ya da kaldırılır.
	upd, err := th.Update(ctx, def.ID, store.ThresholdPatch{WarningLevel: ptr(40.0)})
	if err != nil || upd.WarningLevel != 40 || *upd.DurationSeconds != 600 {
		t.Fatalf("update levels = %+v, %v", upd, err)
	}
	upd, err = th.Update(ctx, def.ID, store.ThresholdPatch{SetDuration: true, Duration: ptr(300)})
	if err != nil || *upd.DurationSeconds != 300 {
		t.Fatalf("update duration = %+v, %v", upd, err)
	}
	upd, err = th.Update(ctx, def.ID, store.ThresholdPatch{SetDuration: true})
	if err != nil || upd.DurationSeconds != nil || upd.WarningLevel != 40 {
		t.Fatalf("clear duration = %+v, %v", upd, err)
	}

	err = th.SetHostOverrides(ctx, host,
		model.ThresholdOverrides{model.MetricTypeTemperature: {WarningLevel: 80, CriticalLevel: 90, DurationSeconds: ptr(300)}}, nil, nil,
		model.SubjectThresholds{
			model.MetricTypeDiskLatency:    {"nvme0n1": {WarningLevel: 5, CriticalLevel: 10, DurationSeconds: ptr(120)}},
			model.MetricTypeServiceRestart: {"nginx.service": {WarningLevel: 2, CriticalLevel: 4}},
		})
	if err != nil {
		t.Fatal(err)
	}
	all, err := th.ResolveHost(ctx, host, org)
	if err != nil {
		t.Fatal(err)
	}
	if b := all.Metric(model.MetricTypeTemperature).Base; b == nil || *b.DurationSeconds != 300 {
		t.Errorf("temperature base = %+v", b)
	}
	if d, ok := all.Metric(model.MetricTypeDiskLatency).For("nvme0n1"); !ok || d.WarningLevel != 5 || *d.DurationSeconds != 120 {
		t.Errorf("nvme0n1 latency = %+v, %v", d, ok)
	}
	if d, ok := all.Metric(model.MetricTypeDiskLatency).For("sda"); !ok || d.WarningLevel != 40 || d.DurationSeconds != nil {
		t.Errorf("sda latency (global default) = %+v, %v", d, ok)
	}
	if d, ok := all.Metric(model.MetricTypeServiceRestart).For("nginx.service"); !ok || d.CriticalLevel != 4 {
		t.Errorf("nginx restart = %+v, %v", d, ok)
	}
	subjects, err := th.HostSubjectOverrides(ctx, host, model.MetricTypeDiskLatency)
	if err != nil || *subjects["nvme0n1"].DurationSeconds != 120 {
		t.Fatalf("subject overrides = %+v, %v", subjects, err)
	}
	// Konu bazlı eşik kaldırılınca sunucu genelini izler.
	if err := th.SetHostOverrides(ctx, host, model.ThresholdOverrides{}, nil, nil,
		model.SubjectThresholds{model.MetricTypeDiskLatency: {"nvme0n1": nil}}); err != nil {
		t.Fatal(err)
	}
	if subjects, _ := th.HostSubjectOverrides(ctx, host, model.MetricTypeDiskLatency); len(subjects) != 0 {
		t.Fatalf("after removing: %v", subjects)
	}
}

// Bekleyen koşulun başlangıcı ilk kayıtta sabitlenir; silinince yeniden başlar. Konu boşsa sunucu genelidir.
func TestAlertPendingConditions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "x")
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	since, err := alerts.MarkPending(ctx, host, model.MetricTypeDiskLatency, "sda", model.AlertLevelWarning, t0)
	if err != nil || !since.Equal(t0) {
		t.Fatalf("first mark = %v, %v", since, err)
	}
	since, err = alerts.MarkPending(ctx, host, model.MetricTypeDiskLatency, "sda", model.AlertLevelCritical, t0.Add(time.Minute))
	if err != nil || !since.Equal(t0) {
		t.Fatalf("second mark = %v, %v; want the first start kept", since, err)
	}
	if _, err := alerts.MarkPending(ctx, host, model.AlertTypeTimeSync, "", model.AlertLevelWarning, t0); err != nil {
		t.Fatal(err)
	}
	list, err := alerts.ListPending(ctx, host)
	if err != nil || len(list) != 2 {
		t.Fatalf("pending = %+v, %v", list, err)
	}
	if err := alerts.ClearPending(ctx, host, model.AlertTypeTimeSync, ""); err != nil {
		t.Fatal(err)
	}
	if err := alerts.ClearPending(ctx, host, model.MetricTypeDiskLatency, "sda"); err != nil {
		t.Fatal(err)
	}
	if list, _ := alerts.ListPending(ctx, host); len(list) != 0 {
		t.Fatalf("after clearing: %+v", list)
	}
	since, _ = alerts.MarkPending(ctx, host, model.MetricTypeDiskLatency, "sda", model.AlertLevelWarning, t0.Add(time.Hour))
	if !since.Equal(t0.Add(time.Hour)) {
		t.Errorf("restarted condition since = %v", since)
	}
}
