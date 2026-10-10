package alertengine

import (
	"testing"
	"time"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

func levels(metricType string, warning, critical float64, seconds ...int) model.ThresholdConfig {
	t := model.ThresholdConfig{MetricType: metricType, WarningLevel: warning, CriticalLevel: critical}
	if len(seconds) > 0 {
		t.DurationSeconds = &seconds[0]
	}
	return t
}

func TestDiskLatency(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{})
	base := levels(model.MetricTypeDiskLatency, 30, 50, 600)
	s.rules.thresholds = store.HostThresholds{model.MetricTypeDiskLatency: {Base: &base,
		PerSubject: map[string]model.ThresholdConfig{"sda": levels(model.MetricTypeDiskLatency, 10, 20)}}}
	io := func(disks map[string]float64) Report {
		var r Report
		for name, ms := range disks {
			r.DiskIO = append(r.DiskIO, model.DiskIO{Name: name, AwaitMs: ms})
		}
		return r
	}
	s.at(0, io(map[string]float64{"nvme0n1": 40, "sda": 25}))
	s.expect(model.MetricTypeDiskLatency, "sda", model.AlertLevelCritical) // kendi eşiği, süresiz
	s.expect(model.MetricTypeDiskLatency, "nvme0n1", "")                   // genel eşik 10 dk bekler
	s.at(10*time.Minute, io(map[string]float64{"nvme0n1": 55, "sda": 25}))
	s.expect(model.MetricTypeDiskLatency, "nvme0n1", model.AlertLevelCritical)
	if a := s.alert(model.MetricTypeDiskLatency, "nvme0n1"); a == nil || *a.Value != 55 || *a.Threshold != 50 {
		t.Fatalf("alert value/threshold = %+v", a)
	}
	s.at(11*time.Minute, Report{}) // G/Ç verisi yok: değişmez
	s.expect(model.MetricTypeDiskLatency, "sda", model.AlertLevelCritical)
	s.at(12*time.Minute, io(map[string]float64{"nvme0n1": 5}))
	s.expect(model.MetricTypeDiskLatency, "nvme0n1", "")
	s.expect(model.MetricTypeDiskLatency, "sda", "") // listeden kalktı

	// Eşik kaldırılınca açık alert kapanır.
	s.at(13*time.Minute, io(map[string]float64{"sda": 25}))
	s.expect(model.MetricTypeDiskLatency, "sda", model.AlertLevelCritical)
	s.rules.thresholds = nil
	s.at(14*time.Minute, Report{})
	s.expect(model.MetricTypeDiskLatency, "sda", "")
}

func TestTemperature(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{})
	s.rules.thresholds = store.HostThresholds{model.MetricTypeTemperature: {
		PerSubject: map[string]model.ThresholdConfig{"coretemp/Package id 0": levels(model.MetricTypeTemperature, 80, 95)}}}
	temps := func(c float64) Report {
		return Report{State: &model.SystemState{Temperatures: []model.Temperature{
			{Sensor: "coretemp/Package id 0", Celsius: c}, {Sensor: "nvme0", Celsius: 99}}}}
	}
	s.at(0, temps(85))
	s.expect(model.MetricTypeTemperature, "coretemp/Package id 0", model.AlertLevelWarning)
	s.expect(model.MetricTypeTemperature, "nvme0", "")     // eşiği yok (donanım varsayılanı yok)
	s.at(time.Minute, Report{State: &model.SystemState{}}) // sanal makine: sıcaklık yok, değişmez
	s.expect(model.MetricTypeTemperature, "coretemp/Package id 0", model.AlertLevelWarning)
	s.at(2*time.Minute, temps(70))
	s.expect(model.MetricTypeTemperature, "coretemp/Package id 0", "")
}

func TestServiceRestartLoop(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{})
	base := levels(model.MetricTypeServiceRestart, 5, 10)
	s.rules.thresholds = store.HostThresholds{model.MetricTypeServiceRestart: {Base: &base}}
	t0 := s.clock
	history := func(at ...time.Duration) [][3]int64 {
		var out [][3]int64
		for i, d := range at {
			out = append(out, [3]int64{t0.Add(d).Unix(), int64(i), int64(i + 1)})
		}
		return out
	}
	s.hosts.services = []model.HostService{
		{Name: "app.service", Watched: true, RestartHistory: history(-9*time.Minute, -8*time.Minute, -7*time.Minute, -3*time.Minute, -time.Minute, -30*time.Second)},
		{Name: "other.service", RestartHistory: history(-time.Minute, -time.Minute, -time.Minute, -time.Minute, -time.Minute, -time.Minute)}, // izlenmiyor
	}
	s.at(0, Report{})
	s.expect(model.AlertTypeServiceRestartLoop, "app.service", model.AlertLevelWarning) // 10 dk'da 6
	s.expect(model.AlertTypeServiceRestartLoop, "other.service", "")
	if a := s.alert(model.AlertTypeServiceRestartLoop, "app.service"); a == nil || *a.Value != 6 {
		t.Fatalf("alert = %+v", a)
	}
	s.at(3*time.Minute, Report{}) // ilk üçü pencereden çıktı: 3 < 5
	s.expect(model.AlertTypeServiceRestartLoop, "app.service", "")

	s.at(4*time.Minute, Report{})
	s.hosts.services[0].RestartHistory = history(3*time.Minute, 3*time.Minute, 3*time.Minute, 3*time.Minute, 3*time.Minute)
	s.at(4*time.Minute, Report{})
	s.expect(model.AlertTypeServiceRestartLoop, "app.service", model.AlertLevelWarning)
	s.hosts.services[0].Watched = false
	s.at(5*time.Minute, Report{})
	s.expect(model.AlertTypeServiceRestartLoop, "app.service", "")
}

func TestTimeOffset(t *testing.T) {
	s := newStatusEnv(t, model.StatusRuleSet{model.RuleTimeUnsynced: rule(model.AlertLevelWarning)})
	base := levels(model.MetricTypeTimeOffset, 100, 1000)
	s.rules.thresholds = store.HostThresholds{model.MetricTypeTimeOffset: {Base: &base}}
	offset := func(ms float64, synced bool) Report {
		return Report{State: &model.SystemState{TimeSync: &model.TimeSync{Synchronized: &synced, OffsetMs: &ms}}}
	}
	s.at(0, offset(-1500, false)) // mutlak değer
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, model.AlertLevelCritical)
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectUnsynced, model.AlertLevelWarning)
	s.at(time.Minute, Report{State: &model.SystemState{}}) // saat bilgisi yok: değişmez
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, model.AlertLevelCritical)

	// Eşik kaldırılınca yalnızca saat farkı alert'i kapanır.
	s.rules.thresholds = nil
	s.at(2*time.Minute, offset(-1500, false))
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, "")
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectUnsynced, model.AlertLevelWarning)

	s.rules.thresholds = store.HostThresholds{model.MetricTypeTimeOffset: {Base: &base}}
	s.at(3*time.Minute, offset(150, true))
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, model.AlertLevelWarning)
	s.at(4*time.Minute, offset(2, true))
	s.expect(model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, "")
}
