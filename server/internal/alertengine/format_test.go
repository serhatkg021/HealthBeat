package alertengine

import (
	"testing"
	"time"

	"healthbeat-server/internal/model"
)

func TestFormatPercentUsesCommaAndTwoDecimals(t *testing.T) {
	if got := formatPercent(34.70318969647882); got != "34,70" {
		t.Fatalf("formatPercent = %q, want %q", got, "34,70")
	}
	if got := formatPercent(100); got != "100,00" {
		t.Fatalf("formatPercent = %q, want %q", got, "100,00")
	}
}

func TestFormatCountHasNoDecimals(t *testing.T) {
	if got := formatCount(7); got != "7" {
		t.Fatalf("formatCount = %q, want %q", got, "7")
	}
}

func TestFormatAlertReadingPicksUnitByMetric(t *testing.T) {
	if got := formatAlertReading(model.MetricTypeRAM, 34.7, 80); got != "%34,70 (eşik: %80,00)" {
		t.Fatalf("formatAlertReading(ram) = %q", got)
	}
	if got := formatAlertReading(model.MetricTypeDockerRestart, 7, 3); got != "7 restart (eşik: 3 restart)" {
		t.Fatalf("formatAlertReading(docker_restart) = %q", got)
	}
}

func TestFormatAlertTimeIncludesUTCSuffix(t *testing.T) {
	ts := time.Date(2026, 9, 22, 19, 53, 49, 0, time.UTC)
	if got := formatAlertTime(ts); got != "22.09.2026 19:53:49 (UTC)" {
		t.Fatalf("formatAlertTime = %q", got)
	}
}

func TestFormatResolutionDurationOmitsLeadingZeroUnits(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, "5 Dakika 0 Saniye"},
		{45 * time.Second, "45 Saniye"},
		{90 * time.Minute, "1 Saat 30 Dakika 0 Saniye"},
		{25*time.Hour + 3*time.Minute, "1 Gün 1 Saat 3 Dakika 0 Saniye"},
		{0, "0 Saniye"},
	}
	for _, c := range cases {
		if got := formatResolutionDuration(c.d); got != c.want {
			t.Errorf("formatResolutionDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestAlertHeadlineDiffersWhenResolved(t *testing.T) {
	open := alertHeadline(model.MetricTypeCPU, false)
	done := alertHeadline(model.MetricTypeCPU, true)
	if open == done {
		t.Fatalf("open and resolved headlines must differ, both = %q", open)
	}
	if open != "CPU kullanım uyarısı" || done != "CPU kullanımı normale döndü" {
		t.Fatalf("headline open=%q done=%q", open, done)
	}
}

func TestAlertSubjectSuffixOmittedWhenNoSubject(t *testing.T) {
	if got := alertSubjectSuffix(model.MetricTypeDisk, ""); got != "" {
		t.Fatalf("alertSubjectSuffix(no subject) = %q, want empty", got)
	}
	if got := alertSubjectSuffix(model.MetricTypeDisk, "/data"); got != " (/data)" {
		t.Fatalf("alertSubjectSuffix(/data) = %q", got)
	}
}
