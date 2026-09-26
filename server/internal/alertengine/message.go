package alertengine

// Bildirim metni: konu satırı, gövde ve değerlerin biçimlendirilmesi (panelle aynı: virgül ondalık, birimli).

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
)

// messageContext, bildirim metninin alert dışındaki bilgileridir (bkz. Engine.messageContext).
type messageContext struct {
	OrgName   string
	HostTitle string
	HostIP    string
	Hostname  string
}

// buildMessage, alert bildiriminin konusunu ve gövdesini üretir. Saf fonksiyondur: aynı girdi her zaman aynı metni verir.
func buildMessage(alert model.Alert, mc messageContext, panelBaseURL string) notify.Message {
	hostLabel := mc.HostTitle
	if mc.HostIP != "" {
		hostLabel = fmt.Sprintf("%s(%s)", mc.HostTitle, mc.HostIP)
	}

	resolved := alert.Status == model.AlertStatusResolved
	levelWord := alertLevelLabel(alert.Level)
	if resolved {
		levelWord = "ÇÖZÜLDÜ"
	}
	headline := alertHeadline(alert.AlertType, resolved) + alertSubjectSuffix(alert.AlertType, alert.Subject)
	subject := fmt.Sprintf("[HealthBeat] -- %s / %s / %s - %s.", levelWord, mc.OrgName, hostLabel, headline)

	var body strings.Builder
	fmt.Fprintf(&body, "Sunucu:\n")
	fmt.Fprintf(&body, "Organizasyon: %s\n", mc.OrgName)
	fmt.Fprintf(&body, "Title: %s\n", mc.HostTitle)
	fmt.Fprintf(&body, "Hostname: %s\n", mc.Hostname)
	fmt.Fprintf(&body, "Sunucu IP: %s\n", mc.HostIP)
	fmt.Fprintf(&body, "\nAlert:\n")
	fmt.Fprintf(&body, "ID: %s\n", alert.ID)
	fmt.Fprintf(&body, "Seviye: %s\n", levelWord)
	fmt.Fprintf(&body, "Tür: %s\n", alertMetricLabel(alert.AlertType))
	if alert.Subject != "" {
		label := "Container"
		if alert.AlertType == model.MetricTypeDisk || alert.AlertType == model.AlertTypeDiskMissing {
			label = "Mount"
		}
		fmt.Fprintf(&body, "%s: %s\n", label, alert.Subject)
	}
	if alert.AlertType == model.AlertTypeDiskMissing {
		fmt.Fprintf(&body, "Ayrıntı: %s mount'u son %d raporda görünmedi (unmount edilmiş, hata vermiş ya da yanıt vermiyor).\n", alert.Subject, missingMountReports)
	}
	if alert.Value != nil && alert.Threshold != nil {
		fmt.Fprintf(&body, "Değer: %s\n", formatAlertReading(alert.AlertType, *alert.Value, *alert.Threshold))
	}
	fmt.Fprintf(&body, "Oluşturulma: %s\n", formatAlertTime(alert.CreatedAt))
	if resolved && alert.ResolvedAt != nil {
		fmt.Fprintf(&body, "Çözülme: %s\n", formatAlertTime(*alert.ResolvedAt))
		fmt.Fprintf(&body, "Çözüm Süresi: %s\n", formatResolutionDuration(alert.ResolvedAt.Sub(alert.CreatedAt)))
	}
	if panelBaseURL != "" {
		fmt.Fprintf(&body, "\nPanel: %s/hosts/%s?sekme=alertler\n", panelBaseURL, alert.HostID)
	}

	return notify.Message{Subject: subject, Body: body.String()}
}

// formatAlertReading bir alert'in ölçümünü ve eşiğini birimiyle (yüzde ya da restart sayısı) tek
// satırda, panelle aynı biçimde (virgül ondalık ayracı) yazar.
func formatAlertReading(alertType string, value, threshold float64) string {
	if alertType == model.MetricTypeDockerRestart {
		return fmt.Sprintf("%s restart (eşik: %s restart)", formatCount(value), formatCount(threshold))
	}
	return fmt.Sprintf("%%%s (eşik: %%%s)", formatPercent(value), formatPercent(threshold))
}

// formatPercent, yüzde değerlerini iki ondalıkla ve virgül ayracıyla yazar (34.703... -> "34,70").
func formatPercent(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1)
}

// formatCount, restart gibi tam sayı ölçümleri ondalıksız yazar.
func formatCount(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// formatAlertTime, e-postadaki zamanları tek biçimde ve saat dilimi açıkça belirtilerek yazar.
// Sunucu UTC'de çalışır; dönüştürmek yerine dilimi parantezde göstermek daha az yanıltıcı
// (yanlış bir dönüşüm hatasına açık kapı bırakmaz).
func formatAlertTime(t time.Time) string {
	return t.UTC().Format("02.01.2006 15:04:05") + " (UTC)"
}

// formatResolutionDuration, bir alert'in ne kadar açık kaldığını insan-okur biçimde yazar; baştaki
// sıfır birimler atlanır (5 dakikalık bir alert için "0 Gün 0 Saat 5 Dakika" değil "5 Dakika").
func formatResolutionDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int(d / time.Second)

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d Gün", days))
	}
	if hours > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%d Saat", hours))
	}
	if minutes > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%d Dakika", minutes))
	}
	parts = append(parts, fmt.Sprintf("%d Saniye", seconds))
	return strings.Join(parts, " ")
}

// alertSubjectSuffix, konu satırına alert'in subject'ini (mount yolu/container adı) ekler — 150
// sunucu arasında yalnızca "disk uyarısı" değil, hangi mount olduğunu da göstermek için.
func alertSubjectSuffix(alertType, subject string) string {
	if subject == "" {
		return ""
	}
	return " (" + subject + ")"
}

// alertHeadline, konu satırındaki insan-okur açıklamadır; açık/yükselmiş ve çözülmüş hâller için ayrıdır.
func alertHeadline(alertType string, resolved bool) string {
	open, done, ok := alertHeadlinePair(alertType)
	if !ok {
		label := alertMetricLabel(alertType)
		open, done = label+" uyarısı", label+" normale döndü"
	}
	if resolved {
		return done
	}
	return open
}

func alertHeadlinePair(alertType string) (open, done string, ok bool) {
	switch alertType {
	case model.MetricTypeCPU:
		return "CPU kullanım uyarısı", "CPU kullanımı normale döndü", true
	case model.MetricTypeRAM:
		return "RAM kullanım uyarısı", "RAM kullanımı normale döndü", true
	case model.MetricTypeDisk:
		return "disk kullanım uyarısı", "disk kullanımı normale döndü", true
	case model.MetricTypeDockerRestart:
		return "container restart uyarısı", "container restart sayısı normale döndü", true
	case model.AlertTypeHostOffline:
		return "sunucu çevrimdışı", "sunucu tekrar çevrimiçi", true
	case model.AlertTypeDiskMissing:
		return "disk kayboldu", "disk tekrar görünür oldu", true
	default:
		return "", "", false
	}
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
