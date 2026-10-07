package model

import (
	"strings"
	"testing"
)

func TestNewThresholdTypesAndDuration(t *testing.T) {
	for _, m := range []string{MetricTypeDiskLatency, MetricTypeTemperature, MetricTypeServiceRestart, MetricTypeTimeOffset} {
		if !ValidThresholdMetricType(m) {
			t.Errorf("%s is not a threshold type", m)
		}
	}
	for name, err := range map[string]error{
		"latency above 10 min": ValidateThresholdLevels(MetricTypeDiskLatency, 30, 600_001),
		"temperature 501":      ValidateThresholdLevels(MetricTypeTemperature, 80, 501),
		"time offset > 1 day":  ValidateThresholdLevels(MetricTypeTimeOffset, 100, 86_400_001),
		"restart > 1e6":        ValidateThresholdLevels(MetricTypeServiceRestart, 5, 1_000_001),
		"duration on cpu":      ValidateThresholdDuration(MetricTypeCPU, ptr(60)),
		"duration 0":           ValidateThresholdDuration(MetricTypeDiskLatency, ptr(0)),
		"duration > 30 days":   ValidateThresholdDuration(MetricTypeTemperature, ptr(MaxRuleDurationSeconds+1)),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, err := range map[string]error{
		"latency":           ValidateThresholdLevels(MetricTypeDiskLatency, 30, 50),
		"temperature":       ValidateThresholdLevels(MetricTypeTemperature, 80, 95),
		"time offset":       ValidateThresholdLevels(MetricTypeTimeOffset, 100, 1000),
		"duration nil cpu":  ValidateThresholdDuration(MetricTypeCPU, nil),
		"duration latency":  ValidateThresholdDuration(MetricTypeDiskLatency, ptr(600)),
		"duration 30 days":  ValidateThresholdDuration(MetricTypeTimeOffset, ptr(MaxRuleDurationSeconds)),
		"override duration": ThresholdOverrides{MetricTypeTemperature: {WarningLevel: 80, CriticalLevel: 90, DurationSeconds: ptr(300)}}.Validate(),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Süre yalnızca desteklenen türlerde: mount ve host geneli disk eşiğine verilemez.
	if err := (MountThresholds{"/": {WarningLevel: 1, CriticalLevel: 2, DurationSeconds: ptr(60)}}).Validate(); err == nil {
		t.Error("a duration on a mount threshold was accepted")
	}
	if err := (ThresholdOverrides{MetricTypeRAM: {WarningLevel: 1, CriticalLevel: 2, DurationSeconds: ptr(60)}}).Validate(); err == nil {
		t.Error("a duration on the ram threshold was accepted")
	}
}

func TestSubjectThresholdsValidate(t *testing.T) {
	ok := SubjectThresholds{
		MetricTypeDiskLatency:    {"nvme0n1": {WarningLevel: 30, CriticalLevel: 50, DurationSeconds: ptr(600)}, "sda": nil},
		MetricTypeTemperature:    {"coretemp/Package id 0": {WarningLevel: 85, CriticalLevel: 95}},
		MetricTypeServiceRestart: {"nginx.service": {WarningLevel: 3, CriticalLevel: 6}},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid subject thresholds rejected: %v", err)
	}
	for name, bad := range map[string]SubjectThresholds{
		"cpu has no subjects":   {MetricTypeCPU: {"x": {WarningLevel: 1, CriticalLevel: 2}}},
		"disk uses mounts":      {MetricTypeDisk: {"/": {WarningLevel: 1, CriticalLevel: 2}}},
		"empty name":            {MetricTypeTemperature: {" ": {WarningLevel: 1, CriticalLevel: 2}}},
		"long disk name":        {MetricTypeDiskLatency: {strings.Repeat("d", 65): {WarningLevel: 1, CriticalLevel: 2}}},
		"control char":          {MetricTypeServiceRestart: {"a\x00b": {WarningLevel: 1, CriticalLevel: 2}}},
		"warning > critical":    {MetricTypeDiskLatency: {"sda": {WarningLevel: 9, CriticalLevel: 2}}},
		"duration out of range": {MetricTypeDiskLatency: {"sda": {WarningLevel: 1, CriticalLevel: 2, DurationSeconds: ptr(-1)}}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := map[string]*ThresholdLevels{}
	for i := 0; i <= maxSubjectThresholds; i++ {
		many[strings.Repeat("s", i+1)] = nil
	}
	if err := (SubjectThresholds{MetricTypeTemperature: many}).Validate(); err == nil {
		t.Error("more than the subject cap was accepted")
	}
}

func TestStatusRuleChangesValidate(t *testing.T) {
	ok := StatusRuleChanges{
		RuleServiceFailed:   {Level: AlertLevelCritical, DurationSeconds: ptr(60)},
		RuleOOMKill:         {Level: AlertLevelWarning, DurationSeconds: ptr(1800)}, // kapanma süresi
		RuleFSReadOnly:      {Level: AlertLevelCritical},
		RuleRebootRequired:  {Level: RuleLevelOff},
		RuleSecurityUpdates: nil,
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	for name, bad := range map[string]StatusRuleChanges{
		"unknown rule":            {"gpu_hot": {Level: AlertLevelInfo}},
		"bad level":               {RuleServiceFailed: {Level: "loud"}},
		"empty level":             {RuleServiceFailed: {}},
		"duration on fs_readonly": {RuleFSReadOnly: {Level: AlertLevelCritical, DurationSeconds: ptr(60)}},
		"duration on reboot":      {RuleRebootRequired: {Level: AlertLevelInfo, DurationSeconds: ptr(60)}},
		"duration on oom":         {RuleContainerOOM: {Level: AlertLevelWarning, DurationSeconds: ptr(60)}},
		"duration 0":              {RuleTimeUnsynced: {Level: AlertLevelWarning, DurationSeconds: ptr(0)}},
		"duration > 30 days":      {RuleSecurityUpdates: {Level: AlertLevelInfo, DurationSeconds: ptr(MaxRuleDurationSeconds + 1)}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Tanımlanmamış kural kapalıdır; "off" da kapalıdır.
func TestStatusRuleSetDefaultsToOff(t *testing.T) {
	set := StatusRuleSet{RuleServiceFailed: {Level: AlertLevelCritical}, RuleRebootRequired: {Level: RuleLevelOff}}
	if !set.Rule(RuleServiceFailed).Enabled() || set.Rule(RuleRebootRequired).Enabled() || set.Rule(RuleOOMKill).Enabled() {
		t.Errorf("enabled: service_failed=%v reboot_required=%v oom_kill=%v", set.Rule(RuleServiceFailed).Enabled(),
			set.Rule(RuleRebootRequired).Enabled(), set.Rule(RuleOOMKill).Enabled())
	}
	if len(StatusRules) != 11 {
		t.Errorf("%d status rules, want 11", len(StatusRules))
	}
	for _, r := range StatusRules {
		if !ValidStatusRule(r) {
			t.Errorf("%s not valid", r)
		}
	}
}
