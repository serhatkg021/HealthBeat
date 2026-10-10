package alertengine

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

// Bildirim metni alıcıların gördüğü sözleşmedir: biçim değişirse bu test bilerek güncellenir (ve CHANGELOG'a yazılır).
func TestBuildMessageGolden(t *testing.T) {
	created := time.Date(2026, 9, 26, 8, 15, 0, 0, time.UTC)
	resolved := created.Add(83 * time.Minute)
	value, threshold := 71.25, 85.0
	alertID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	hostID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	cases := []struct {
		name        string
		alert       model.Alert
		mc          messageContext
		panel       string
		wantSubject string
		wantBody    string
	}{
		{
			name: "resolved disk alert with panel link",
			alert: model.Alert{ID: alertID, HostID: hostID, AlertType: model.MetricTypeDisk, Subject: "/data",
				Level: model.AlertLevelCritical, Status: model.AlertStatusResolved, Value: &value, Threshold: &threshold,
				CreatedAt: created, ResolvedAt: &resolved},
			mc:          messageContext{OrgName: "Acme", HostTitle: "web-1", HostIP: "10.0.0.5", Hostname: "web1.acme"},
			panel:       "https://panel.example.com",
			wantSubject: "[HealthBeat] -- ÇÖZÜLDÜ / Acme / web-1(10.0.0.5) - disk kullanımı normale döndü (/data).",
			wantBody: `Sunucu:
Organizasyon: Acme
Title: web-1
Hostname: web1.acme
Sunucu IP: 10.0.0.5

Alert:
ID: 11111111-2222-3333-4444-555555555555
Seviye: ÇÖZÜLDÜ
Tür: disk
Mount: /data
Değer: %71,25 (eşik: %85,00)
Oluşturulma: 26.09.2026 08:15:00 (UTC)
Çözülme: 26.09.2026 09:38:00 (UTC)
Çözüm Süresi: 1 Saat 23 Dakika 0 Saniye

Panel: https://panel.example.com/hosts/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee?sekme=alertler
`,
		},
		{
			name: "offline alert without host details or panel link",
			alert: model.Alert{ID: alertID, HostID: hostID, AlertType: model.AlertTypeHostOffline,
				Level: model.AlertLevelCritical, Status: model.AlertStatusOpen, CreatedAt: created},
			mc:          messageContext{OrgName: "Acme", HostTitle: "web-1", Hostname: "—"},
			wantSubject: "[HealthBeat] -- KRİTİK / Acme / web-1 - sunucu çevrimdışı.",
			// "Sunucu IP: " satırı IP bilinmediğinde de yazılır (sonunda boşlukla).
			wantBody: "Sunucu:\nOrganizasyon: Acme\nTitle: web-1\nHostname: —\nSunucu IP: \n\n" +
				"Alert:\nID: 11111111-2222-3333-4444-555555555555\nSeviye: KRİTİK\nTür: sunucu çevrimdışı\n" +
				"Oluşturulma: 26.09.2026 08:15:00 (UTC)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := buildMessage(tc.alert, tc.mc, tc.panel, time.UTC)
			if msg.Subject != tc.wantSubject {
				t.Errorf("subject:\n got %q\nwant %q", msg.Subject, tc.wantSubject)
			}
			if msg.Body != tc.wantBody {
				t.Errorf("body:\n got %q\nwant %q", msg.Body, tc.wantBody)
			}
		})
	}
}

// Protokol 4 türlerinin bildirimleri: konu etiketi, okunur sorun adı ve birimli değer.
func TestBuildMessageProtocol4Types(t *testing.T) {
	created := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	mc := messageContext{OrgName: "Acme", HostTitle: "web-1", HostIP: "10.0.0.5", Hostname: "web1"}
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		alert       model.Alert
		wantSubject string
		wantLines   []string
	}{
		{model.Alert{AlertType: model.MetricTypeDiskLatency, Subject: "nvme0n1", Level: model.AlertLevelWarning, Status: model.AlertStatusOpen,
			Value: f(42.5), Threshold: f(30), CreatedAt: created},
			"[HealthBeat] -- UYARI / Acme / web-1(10.0.0.5) - disk gecikmesi uyarısı (nvme0n1).",
			[]string{"Tür: disk gecikmesi\n", "Disk: nvme0n1\n", "Değer: 42,50 ms (eşik: 30,00 ms)\n"}},
		{model.Alert{AlertType: model.MetricTypeTemperature, Subject: "coretemp/Package id 0", Level: model.AlertLevelCritical,
			Status: model.AlertStatusOpen, Value: f(97.25), Threshold: f(95), CreatedAt: created},
			"[HealthBeat] -- KRİTİK / Acme / web-1(10.0.0.5) - sıcaklık uyarısı (coretemp/Package id 0).",
			[]string{"Sensör: coretemp/Package id 0\n", "Değer: 97,2 °C (eşik: 95,0 °C)\n"}},
		{model.Alert{AlertType: model.AlertTypeServiceFailed, Subject: "nginx.service", Level: model.AlertLevelCritical,
			Status: model.AlertStatusResolved, CreatedAt: created, ResolvedAt: &created},
			"[HealthBeat] -- ÇÖZÜLDÜ / Acme / web-1(10.0.0.5) - servis yeniden çalışıyor (nginx.service).",
			[]string{"Tür: servis durumu\n", "Servis: nginx.service\n"}},
		{model.Alert{AlertType: model.AlertTypeTimeSync, Subject: model.TimeSyncSubjectSource, Level: model.AlertLevelInfo,
			Status: model.AlertStatusOpen, CreatedAt: created},
			"[HealthBeat] -- BİLGİ / Acme / web-1(10.0.0.5) - saat senkronu sorunu (saat kaynağı sorunlu).",
			[]string{"Sorun: saat kaynağı sorunlu\n"}},
		{model.Alert{AlertType: model.AlertTypeTimeSync, Subject: model.TimeSyncSubjectOffset, Level: model.AlertLevelWarning,
			Status: model.AlertStatusOpen, Value: f(152.4), Threshold: f(100), CreatedAt: created},
			"[HealthBeat] -- UYARI / Acme / web-1(10.0.0.5) - saat senkronu sorunu (saat farkı eşiği aştı).",
			[]string{"Değer: 152,40 ms (eşik: 100,00 ms)\n"}},
		{model.Alert{AlertType: model.AlertTypeServiceRestartLoop, Subject: "app.service", Level: model.AlertLevelWarning,
			Status: model.AlertStatusOpen, Value: f(6), Threshold: f(5), CreatedAt: created},
			"[HealthBeat] -- UYARI / Acme / web-1(10.0.0.5) - servis sürekli yeniden başlıyor (app.service).",
			[]string{"Değer: 10 dakikada 6 yeniden başlatma (eşik: 5)\n"}},
		{model.Alert{AlertType: model.AlertTypeFSReadOnly, Subject: "/data", Level: model.AlertLevelCritical,
			Status: model.AlertStatusOpen, CreatedAt: created},
			"[HealthBeat] -- KRİTİK / Acme / web-1(10.0.0.5) - dosya sistemi salt okunur oldu (/data).",
			[]string{"Mount: /data\n"}},
	}
	for _, tc := range cases {
		msg := buildMessage(tc.alert, mc, "", time.UTC)
		if msg.Subject != tc.wantSubject {
			t.Errorf("%s subject:\n got %q\nwant %q", tc.alert.AlertType, msg.Subject, tc.wantSubject)
		}
		for _, line := range tc.wantLines {
			if !strings.Contains(msg.Body, line) {
				t.Errorf("%s body misses %q:\n%s", tc.alert.AlertType, line, msg.Body)
			}
		}
	}
}

// Veritabanının kabul ettiği her alert türünün okunur bir başlığı ve etiketi vardır (yoksa bildirimde ham ad görünür).
func TestEveryAlertTypeHasAHeadline(t *testing.T) {
	for _, alertType := range []string{
		model.MetricTypeCPU, model.MetricTypeRAM, model.MetricTypeDisk, model.MetricTypeDockerRestart,
		model.AlertTypeHostOffline, model.AlertTypeDiskMissing,
		model.AlertTypeServiceFailed, model.AlertTypeServiceRestartLoop, model.AlertTypeContainerUnhealthy,
		model.AlertTypeContainerOOM, model.MetricTypeDiskLatency, model.AlertTypeOOMKill, model.AlertTypeFSReadOnly,
		model.AlertTypeRAIDDegraded, model.MetricTypeTemperature, model.AlertTypeTimeSync, model.AlertTypeRebootRequired,
		model.AlertTypeSecurityUpdates,
	} {
		if _, _, ok := alertHeadlinePair(alertType); !ok {
			t.Errorf("%s has no headline", alertType)
		}
		if alertMetricLabel(alertType) == alertType && alertType != model.MetricTypeDisk {
			t.Errorf("%s has no label", alertType)
		}
	}
}
