package alertengine

// Bildirim metni: konu satırı, gövde ve değerlerin biçimlendirilmesi (panelle aynı: virgül ondalık, birimli).

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/tz"
)

// messageContext, bildirim metninin alert dışındaki bilgileridir (bkz. Engine.messageContext).
type messageContext struct {
	OrgName   string
	HostTitle string
	HostIP    string
	Hostname  string
}

// buildMessage, alert bildiriminin konusunu ve gövdesini üretir; zamanlar loc'ta yazılır. Saf fonksiyondur: aynı girdi
// her zaman aynı metni verir.
func buildMessage(alert model.Alert, mc messageContext, panelBaseURL string, loc *time.Location) notify.Message {
	hostLabel := mc.HostTitle
	if mc.HostIP != "" {
		hostLabel = fmt.Sprintf("%s(%s)", mc.HostTitle, mc.HostIP)
	}

	resolved := alert.Status == model.AlertStatusResolved
	levelWord := alertLevelLabel(alert.Level)
	if resolved {
		levelWord = "ÇÖZÜLDÜ"
	}
	headline := alertHeadline(alert.AlertType, resolved) + alertSubjectSuffix(alert.AlertType, alert.Subject, resolved)
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
		label, subject := alertSubjectLabel(alert.AlertType, alert.Subject)
		fmt.Fprintf(&body, "%s: %s\n", label, subject)
	}
	if alert.AlertType == model.AlertTypeDiskMissing {
		fmt.Fprintf(&body, "Ayrıntı: %s mount'u son %d raporda görünmedi (unmount edilmiş, hata vermiş ya da yanıt vermiyor).\n", alert.Subject, missingMountReports)
	}
	if alert.Value != nil && alert.Threshold != nil {
		fmt.Fprintf(&body, "Değer: %s\n", formatAlertReading(alert.AlertType, *alert.Value, *alert.Threshold))
	}
	fmt.Fprintf(&body, "Oluşturulma: %s\n", formatAlertTime(alert.CreatedAt, loc))
	if resolved && alert.ResolvedAt != nil {
		fmt.Fprintf(&body, "Çözülme: %s\n", formatAlertTime(*alert.ResolvedAt, loc))
		fmt.Fprintf(&body, "Çözüm Süresi: %s\n", formatResolutionDuration(alert.ResolvedAt.Sub(alert.CreatedAt)))
	}
	if panelBaseURL != "" {
		fmt.Fprintf(&body, "\nPanel: %s/hosts/%s?sekme=alertler\n", panelBaseURL, alert.HostID)
	}

	return notify.Message{Subject: subject, Body: body.String()}
}

// formatAlertReading bir alert'in ölçümünü ve eşiğini birimiyle (yüzde, sayı, ms ya da °C) tek
// satırda, panelle aynı biçimde (virgül ondalık ayracı) yazar.
func formatAlertReading(alertType string, value, threshold float64) string {
	switch alertType {
	case model.MetricTypeDockerRestart:
		return fmt.Sprintf("%s restart (eşik: %s restart)", formatCount(value), formatCount(threshold))
	case model.AlertTypeServiceRestartLoop:
		return fmt.Sprintf("10 dakikada %s yeniden başlatma (eşik: %s)", formatCount(value), formatCount(threshold))
	case model.MetricTypeDiskLatency, model.AlertTypeTimeSync: // time_sync'te değer yalnızca saat farkındadır
		return fmt.Sprintf("%s ms (eşik: %s ms)", formatPercent(value), formatThreshold(threshold, 2))
	case model.MetricTypeTemperature:
		return fmt.Sprintf("%s °C (eşik: %s °C)", formatOneDecimal(value), formatThreshold(threshold, 1))
	}
	return fmt.Sprintf("%%%s (eşik: %%%s)", formatPercent(value), formatThreshold(threshold, 2))
}

// formatThreshold, eşiği ölçümle aynı ondalıkla yazar; bu ondalık eşiği değiştiriyorsa (0,002 ms gibi küçük bir eşik
// "0,00" olurdu) eşik kendi tam hâliyle yazılır. Eşik kullanıcının girdiği değerdir, yuvarlanıp kaybolmamalı.
func formatThreshold(v float64, digits int) string {
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if r, err := strconv.ParseFloat(s, 64); err == nil && r != v {
		s = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strings.Replace(s, ".", ",", 1)
}

// formatOneDecimal, sıcaklık gibi değerleri tek ondalıkla ve virgül ayracıyla yazar (64.25 -> "64,3").
func formatOneDecimal(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

// formatPercent, yüzde değerlerini iki ondalıkla ve virgül ayracıyla yazar (34.703... -> "34,70").
func formatPercent(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1)
}

// formatCount, restart gibi tam sayı ölçümleri ondalıksız yazar.
func formatCount(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// formatAlertTime, e-postadaki zamanları kurulumun saat diliminde, dilimin adı ve o andaki UTC ofsetiyle yazar:
// "10.10.2026 21:11:08 (Europe/Istanbul, UTC+3)"; saat dilimi ayarlanmamışsa "… (UTC)". Ofset, yaz saati
// geçişinde iki kez yaşanan saati de ayırt eder.
func formatAlertTime(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02.01.2006 15:04:05") + " (" + tz.Label(t, loc) + ")"
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
// sunucu arasında yalnızca "disk uyarısı" değil, hangi mount olduğunu da göstermek için. time_sync'te konu sorunun
// türüdür: açılışta nedeni ("saat farkı eşiği aştı"), çözülmede yalnızca sorunun adı ("saat farkı") yazılır; yoksa
// "normale döndü (saat farkı eşiği aştı)" gibi çelişkili bir konu satırı çıkar.
func alertSubjectSuffix(alertType, subject string, resolved bool) string {
	if subject == "" {
		return ""
	}
	if resolved && alertType == model.AlertTypeTimeSync {
		if topic, ok := timeSyncTopics[subject]; ok {
			return " (" + topic + ")"
		}
	}
	_, shown := alertSubjectLabel(alertType, subject)
	return " (" + shown + ")"
}

// timeSyncTopics, time_sync konularının (sorun türü) kısa adıdır; çözülme bildiriminde kullanılır.
var timeSyncTopics = map[string]string{
	model.TimeSyncSubjectUnsynced: "saat senkronu",
	model.TimeSyncSubjectSource:   "saat kaynağı",
	model.TimeSyncSubjectOffset:   "saat farkı",
}

// timeSyncReasons, time_sync alert'inin konularının (sorun türü) okunur adıdır.
var timeSyncReasons = map[string]string{
	model.TimeSyncSubjectUnsynced: "saat senkron değil",
	model.TimeSyncSubjectSource:   "saat kaynağı sorunlu",
	model.TimeSyncSubjectOffset:   "saat farkı eşiği aştı",
}

// alertSubjectLabel, gövdedeki konu satırının etiketi ve gösterilecek değeridir (time_sync'te konu bir sorun türüdür).
func alertSubjectLabel(alertType, subject string) (label, shown string) {
	switch alertType {
	case model.MetricTypeDisk, model.AlertTypeDiskMissing, model.AlertTypeFSReadOnly:
		return "Mount", subject
	case model.MetricTypeDiskLatency:
		return "Disk", subject
	case model.MetricTypeTemperature:
		return "Sensör", subject
	case model.AlertTypeServiceFailed, model.AlertTypeServiceRestartLoop:
		return "Servis", subject
	case model.AlertTypeRAIDDegraded:
		return "RAID", subject
	case model.AlertTypeTimeSync:
		if reason, ok := timeSyncReasons[subject]; ok {
			return "Sorun", reason
		}
		return "Sorun", subject
	}
	return "Container", subject
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
	case model.MetricTypeDiskLatency:
		return "disk gecikmesi uyarısı", "disk gecikmesi normale döndü", true
	case model.MetricTypeTemperature:
		return "sıcaklık uyarısı", "sıcaklık normale döndü", true
	case model.AlertTypeServiceFailed:
		return "servis çalışmıyor", "servis yeniden çalışıyor", true
	case model.AlertTypeServiceRestartLoop:
		return "servis sürekli yeniden başlıyor", "servisin yeniden başlatmaları durdu", true
	case model.AlertTypeContainerUnhealthy:
		return "container sağlıksız", "container yeniden sağlıklı", true
	case model.AlertTypeContainerOOM:
		return "container bellek yetmediği için öldürüldü", "container yeniden çalışıyor", true
	case model.AlertTypeOOMKill:
		return "bellek yetmediği için süreç öldürüldü", "yeni bellek yetmezliği olayı yok", true
	case model.AlertTypeFSReadOnly:
		return "dosya sistemi salt okunur oldu", "dosya sistemi yeniden yazılabilir", true
	case model.AlertTypeRAIDDegraded:
		return "RAID dizisi sorunlu", "RAID dizisi normale döndü", true
	case model.AlertTypeTimeSync:
		return "saat senkronu sorunu", "saat senkronu normale döndü", true
	case model.AlertTypeRebootRequired:
		return "yeniden başlatma gerekli", "yeniden başlatma artık gerekmiyor", true
	case model.AlertTypeSecurityUpdates:
		return "bekleyen güvenlik güncellemesi", "güvenlik güncellemeleri uygulandı", true
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
	case model.MetricTypeDiskLatency:
		return "disk gecikmesi"
	case model.MetricTypeTemperature:
		return "sıcaklık"
	case model.AlertTypeServiceFailed:
		return "servis durumu"
	case model.AlertTypeServiceRestartLoop:
		return "servis yeniden başlatma"
	case model.AlertTypeContainerUnhealthy:
		return "container sağlığı"
	case model.AlertTypeContainerOOM:
		return "container bellek yetmezliği"
	case model.AlertTypeOOMKill:
		return "bellek yetmezliği (OOM)"
	case model.AlertTypeFSReadOnly:
		return "salt okunur dosya sistemi"
	case model.AlertTypeRAIDDegraded:
		return "RAID"
	case model.AlertTypeTimeSync:
		return "saat senkronu"
	case model.AlertTypeRebootRequired:
		return "yeniden başlatma"
	case model.AlertTypeSecurityUpdates:
		return "güvenlik güncellemeleri"
	default:
		return metricType
	}
}
