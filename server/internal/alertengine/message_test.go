package alertengine

import (
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
			msg := buildMessage(tc.alert, tc.mc, tc.panel)
			if msg.Subject != tc.wantSubject {
				t.Errorf("subject:\n got %q\nwant %q", msg.Subject, tc.wantSubject)
			}
			if msg.Body != tc.wantBody {
				t.Errorf("body:\n got %q\nwant %q", msg.Body, tc.wantBody)
			}
		})
	}
}
