package alertengine

import (
	"strings"
	"testing"
	"time"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Bir türün hiç eşiği kalmayınca açık alert'leri bir sonraki raporda "çözüldü" bildirimiyle kapanır; rapor o türün
// verisini taşımasa da (boş disk/container listesi). Önceden açık kalıyordu ve kapatmanın hiçbir yolu yoktu.
func TestAlertsCloseWhenTheirThresholdsAreRemoved(t *testing.T) {
	cpu, ram := levels(model.MetricTypeCPU, 70, 90), levels(model.MetricTypeRAM, 70, 90)
	disk, docker := levels(model.MetricTypeDisk, 80, 90), levels(model.MetricTypeDockerRestart, 3, 5)
	all := func() store.HostThresholds {
		return store.HostThresholds{
			model.MetricTypeCPU: {Base: &cpu}, model.MetricTypeRAM: {Base: &ram},
			model.MetricTypeDisk:          {Base: &disk, PerSubject: map[string]model.ThresholdConfig{}},
			model.MetricTypeDockerRestart: {Base: &docker, PerSubject: map[string]model.ThresholdConfig{}},
		}
	}
	breach := Report{CPUPct: 95, RAMPct: 95, Disks: []model.DiskUsage{{Mount: "/", UsedPct: 95}},
		Containers: []model.DockerContainerReport{{Name: "web", Status: "running", RestartCount: 9}}}

	for name, removed := range map[string][]string{
		"cpu":            {model.MetricTypeCPU},
		"ram":            {model.MetricTypeRAM},
		"disk":           {model.MetricTypeDisk},
		"docker_restart": {model.MetricTypeDockerRestart},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStatusEnv(t, model.StatusRuleSet{})
			outbox := &fakeOutbox{}
			s.engine.tx = fakeTx{alerts: s.alerts, outbox: outbox}
			s.engine.notifs = fakeRecipients{{Name: "Ops", Channel: model.ChannelEmail, Address: "ops@example.com"}}
			s.rules.thresholds = all()
			s.at(0, breach)
			subject := map[string]string{model.MetricTypeDisk: "/", model.MetricTypeDockerRestart: "web"}[name]
			s.expect(name, subject, model.AlertLevelCritical)

			th := all()
			for _, m := range removed {
				delete(th, m)
			}
			s.rules.thresholds = th
			s.at(time.Minute, Report{CPUPct: 95, RAMPct: 95}) // değerler hâlâ yüksek; disk/container listesi yok
			s.expect(name, subject, "")
			resolved := 0
			for _, subj := range outbox.subjects() {
				if strings.Contains(subj, "ÇÖZÜLDÜ") {
					resolved++
				}
			}
			if resolved != 1 {
				t.Errorf("resolved notifications = %d, want 1: %v", resolved, outbox.subjects())
			}
			// Diğer türler eşikleri durduğu için açık kalır.
			for _, other := range []string{model.MetricTypeCPU, model.MetricTypeRAM} {
				if other != name && s.level(other, "") != model.AlertLevelCritical {
					t.Errorf("%s closed although its threshold stayed", other)
				}
			}
		})
	}
}
