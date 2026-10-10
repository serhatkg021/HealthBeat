package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Protokol 4 alert'lerinin kuralları. İki grup vardır:
//   - Sayısal alert'ler (disk gecikmesi, sıcaklık, servis yeniden başlatma, saat farkı) eşik sistemini kullanır: uyarı/
//     kritik seviyesi ve isteğe bağlı süre koşulu (ThresholdLevels.DurationSeconds).
//   - Durum alert'leri (servis çalışmıyor, RAID bozuk …) bir değer değil bir durum bildirir; seviyeleri ve süreleri durum
//     kurallarıyla (StatusRule) ayarlanır.
//
// İkisinde de sistem varsayılanı yoktur: kural ya da eşik tanımlanmadıkça alert açılmaz.

// Alert türleri (alerts.alert_type). Sayısal olanlar eşik türüyle aynı adı taşır (disk_latency, temperature); diğerleri
// durum alert'leridir.
const (
	AlertTypeServiceFailed      = "service_failed"
	AlertTypeServiceRestartLoop = "service_restart_loop"
	AlertTypeContainerUnhealthy = "container_unhealthy"
	AlertTypeContainerOOM       = "container_oom"
	AlertTypeOOMKill            = "oom_kill"
	AlertTypeFSReadOnly         = "fs_readonly"
	AlertTypeRAIDDegraded       = "raid_degraded"
	AlertTypeTimeSync           = "time_sync"
	AlertTypeRebootRequired     = "reboot_required"
	AlertTypeSecurityUpdates    = "security_updates"
)

// time_sync alert'inin konuları: üç ayrı sorun ayrı alert'tir; konu bildirimde nedeni söyler.
const (
	TimeSyncSubjectUnsynced = "unsynced" // saat senkron değil
	TimeSyncSubjectSource   = "source"   // saat kaynağına ulaşılamıyor ya da yanıtı geçersiz (saat hâlâ senkron)
	TimeSyncSubjectOffset   = "offset"   // saat farkı eşiği aştı (time_offset eşiği)
)

// Sayısal eşiklerin üst sınırları: ulaşılması anlamsız bir seviye "alert hiç yok" demektir.
const (
	maxLatencyLevelMs    = 600_000    // 10 dakika
	maxTemperatureLevel  = 500        // °C
	maxTimeOffsetLevelMs = 86_400_000 // 1 gün
	// MaxRuleDurationSeconds, eşik ve durum kurallarındaki sürenin üst sınırıdır (30 gün).
	MaxRuleDurationSeconds = 30 * 24 * 3600
)

// DurationMetricTypes, süre koşulu verilebilen eşik türleridir. CPU/RAM/disk/docker_restart anlık değerlendirilir.
var DurationMetricTypes = map[string]bool{
	MetricTypeDiskLatency: true, MetricTypeTemperature: true, MetricTypeServiceRestart: true, MetricTypeTimeOffset: true,
}

// ValidateThresholdDuration, bir eşiğin süresini denetler (nil = hemen, her türde geçerli).
func ValidateThresholdDuration(metricType string, d *int) error {
	if d == nil {
		return nil
	}
	if !DurationMetricTypes[metricType] {
		return fmt.Errorf("duration_seconds yalnızca %s için verilebilir", strings.Join(durationMetricNames(), ", "))
	}
	return validateDuration(*d)
}

func durationMetricNames() []string {
	var out []string
	for _, t := range ThresholdMetricTypes {
		if DurationMetricTypes[t] {
			out = append(out, t)
		}
	}
	return out
}

func validateDuration(d int) error {
	if d < 1 || d > MaxRuleDurationSeconds {
		return fmt.Errorf("duration_seconds 1 ile %d (30 gün) arasında olmalı", MaxRuleDurationSeconds)
	}
	return nil
}

func (l ThresholdLevels) validate(metricType string) error {
	if err := ValidateThresholdLevels(metricType, l.WarningLevel, l.CriticalLevel); err != nil {
		return err
	}
	return ValidateThresholdDuration(metricType, l.DurationSeconds)
}

// SubjectMetricTypes, konu bazlı (sunucunun tek bir diski, sensörü ya da servisi için) eşik verilebilen protokol 4
// türleridir ve konunun ne olduğudur. disk (mount) ve docker_restart (container) kendi alanlarıyla verilir.
var SubjectMetricTypes = map[string]string{
	MetricTypeDiskLatency:    "disk",
	MetricTypeTemperature:    "sensör",
	MetricTypeServiceRestart: "servis",
}

// SubjectThresholds, bir sunucunun SubjectMetricTypes türlerindeki konu bazlı eşikleridir: tür -> konu -> seviyeler
// (nil = o konunun eşiğini kaldır, sunucu genelindekini izlesin).
type SubjectThresholds map[string]map[string]*ThresholdLevels

func (s SubjectThresholds) Validate() error {
	for metricType, subjects := range s {
		what, ok := SubjectMetricTypes[metricType]
		if !ok {
			return fmt.Errorf("%q için konu bazlı eşik verilemez (disk_latency, temperature, service_restart)", metricType)
		}
		max := maxServiceText
		if metricType == MetricTypeDiskLatency {
			max = maxDiskNameBytes
		}
		err := validateSubjectLevels(subjects, metricType, what, func(name string) error {
			switch {
			case strings.TrimSpace(name) == "":
				return fmt.Errorf("%s adı boş olamaz", what)
			case len(name) > max:
				return fmt.Errorf("%s adı %d bayttan uzun olamaz", what, max)
			case strings.IndexFunc(name, unicode.IsControl) >= 0:
				return fmt.Errorf("%s adı kontrol karakteri içeremez", what)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", metricType, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- durum kuralları

// Durum kuralları: eşiği olmayan alert'lerin seviyesi ve süresi. Bir alert türü birden çok kurala bölünebilir (RAID
// bozuk / yeniden kuruluyor; saat senkron değil / kaynak sorunlu): her durumun seviyesi ayrı ayarlanır.
const (
	RuleServiceFailed      = "service_failed"      // izlenen servis çalışmıyor (failed/inactive)
	RuleContainerUnhealthy = "container_unhealthy" // Docker healthcheck "unhealthy"
	RuleContainerOOM       = "container_oom"       // container bellek yetmediği için öldürüldü
	RuleOOMKill            = "oom_kill"            // çekirdek bellek yetmediği için süreç öldürdü
	RuleFSReadOnly         = "fs_readonly"         // yazılabilir dosya sistemi salt okunur oldu
	RuleRAIDDegraded       = "raid_degraded"       // RAID bozuk (degraded/failed)
	RuleRAIDRebuilding     = "raid_rebuilding"     // RAID yeniden kuruluyor (recovering/resyncing)
	RuleTimeUnsynced       = "time_unsynced"       // saat senkron değil
	RuleTimeSource         = "time_source"         // saat kaynağı sorunlu, saat hâlâ senkron
	RuleRebootRequired     = "reboot_required"     // yeniden başlatma gerekli
	RuleSecurityUpdates    = "security_updates"    // bekleyen güvenlik güncellemesi
)

// StatusRules, durum kurallarıdır, gösterim sırasıyla.
var StatusRules = []string{
	RuleServiceFailed, RuleContainerUnhealthy, RuleContainerOOM, RuleOOMKill, RuleFSReadOnly, RuleRAIDDegraded,
	RuleRAIDRebuilding, RuleTimeUnsynced, RuleTimeSource, RuleRebootRequired, RuleSecurityUpdates,
}

// instantRules, anlık bir olaya ya da bayrağa bakan kurallardır: süre verilmez.
var instantRules = map[string]bool{RuleContainerOOM: true, RuleFSReadOnly: true, RuleRebootRequired: true}

// RuleTakesDuration, kurala süre verilip verilemeyeceğidir. oom_kill'de süre açılmayı değil kapanmayı belirler (bu
// kadar süre yeni artış olmazsa kapanır).
func RuleTakesDuration(rule string) bool { return ValidStatusRule(rule) && !instantRules[rule] }

func ValidStatusRule(rule string) bool {
	for _, r := range StatusRules {
		if r == rule {
			return true
		}
	}
	return false
}

// RuleLevelOff, kuralı kapatan seviyedir: üst kapsamda açık bir kuralı bu kapsamda kapatmak için.
const RuleLevelOff = "off"

// StatusRuleSetting, bir kapsamdaki bir kuralın ayarıdır.
type StatusRuleSetting struct {
	Level           string `json:"level"` // off | info | warning | critical
	DurationSeconds *int   `json:"duration_seconds,omitempty"`
}

// Enabled, kuralın alert ürettiğidir.
func (s StatusRuleSetting) Enabled() bool { return s.Level != "" && s.Level != RuleLevelOff }

func (s StatusRuleSetting) validate(rule string) error {
	if s.Level != RuleLevelOff && !ValidAlertLevel(s.Level) {
		return errors.New("level off, info, warning veya critical olmalı")
	}
	if s.DurationSeconds == nil {
		return nil
	}
	if !RuleTakesDuration(rule) {
		return errors.New("bu kurala süre verilmez (anlık bir olaydır)")
	}
	return validateDuration(*s.DurationSeconds)
}

// StatusRuleChanges, bir kapsamdaki kural değişiklikleridir: kural -> ayar ya da nil (bu kapsamdaki ayarı kaldır, üst
// kapsamınkini izle). Listede olmayan kurallara dokunulmaz.
type StatusRuleChanges map[string]*StatusRuleSetting

func (c StatusRuleChanges) Validate() error {
	for rule, setting := range c {
		if !ValidStatusRule(rule) {
			return fmt.Errorf("%q bir durum kuralı değil (%s)", rule, strings.Join(StatusRules, ", "))
		}
		if setting == nil {
			continue
		}
		if err := setting.validate(rule); err != nil {
			return fmt.Errorf("%s: %w", rule, err)
		}
	}
	return nil
}

// StatusRuleConfig, saklanan bir kural satırıdır. OrganizationID ve HostID ikisi de nil ise geneldir.
type StatusRuleConfig struct {
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	HostID         *uuid.UUID `json:"host_id,omitempty"`
	Rule           string     `json:"rule"`
	StatusRuleSetting
	UpdatedAt time.Time `json:"updated_at"`
}

// StatusRuleSet, bir sunucuya uygulanan kurallardır (kural -> geçerli ayar). Olmayan kural kapalıdır.
type StatusRuleSet map[string]StatusRuleSetting

// Rule, kuralın geçerli ayarıdır; tanımlı değilse kapalı.
func (s StatusRuleSet) Rule(rule string) StatusRuleSetting {
	if st, ok := s[rule]; ok {
		return st
	}
	return StatusRuleSetting{Level: RuleLevelOff}
}
