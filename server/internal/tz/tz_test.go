package tz

import (
	"testing"
	"time"
)

func TestFormatOffset(t *testing.T) {
	for sec, want := range map[int]string{
		0: "UTC", 3 * 3600: "UTC+3", 2 * 3600: "UTC+2", -4 * 3600: "UTC−4",
		5*3600 + 30*60: "UTC+5:30", -(3*3600 + 30*60): "UTC−3:30", 5*3600 + 45*60: "UTC+5:45",
	} {
		if got := FormatOffset(sec); got != want {
			t.Errorf("FormatOffset(%d) = %q, want %q", sec, got, want)
		}
	}
}

// Ofset o andaki kurala göredir: Berlin yazın UTC+2, kışın UTC+1; geçiş gecesi iki kez yaşanan 02:30 ofsetle ayrılır.
func TestLabelFollowsDaylightSaving(t *testing.T) {
	berlin, ist := Resolve("Europe/Berlin"), Resolve("Europe/Istanbul")
	for _, c := range []struct {
		at   time.Time
		loc  *time.Location
		want string
	}{
		{time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC), berlin, "Europe/Berlin, UTC+2"},
		{time.Date(2026, 12, 15, 10, 0, 0, 0, time.UTC), berlin, "Europe/Berlin, UTC+1"},
		{time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), berlin, "Europe/Berlin, UTC+2"},
		{time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), berlin, "Europe/Berlin, UTC+1"},
		{time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC), ist, "Europe/Istanbul, UTC+3"},
		{time.Date(2026, 12, 15, 10, 0, 0, 0, time.UTC), ist, "Europe/Istanbul, UTC+3"},
		{time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC), Resolve("Asia/Kolkata"), "Asia/Kolkata, UTC+5:30"},
		{time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC), time.UTC, "UTC"},
		{time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC), Resolve("UTC"), "UTC"},
	} {
		if got := Label(c.at, c.loc); got != c.want {
			t.Errorf("Label(%s, %s) = %q, want %q", c.at.Format(time.RFC3339), c.loc, got, c.want)
		}
	}
}

// Ayar geçerliyse o, değilse TZ, o da yoksa UTC.
func TestResolvePrecedence(t *testing.T) {
	t.Setenv("TZ", "")
	if got := Resolve(""); got != time.UTC {
		t.Errorf("no setting, no TZ: %s, want UTC", got)
	}
	if got := Resolve("Europe/Istanbul").String(); got != "Europe/Istanbul" {
		t.Errorf("setting: %s", got)
	}
	t.Setenv("TZ", ":Europe/Berlin")
	if got := Resolve("").String(); got != "Europe/Berlin" {
		t.Errorf("TZ=:Europe/Berlin: %s", got)
	}
	if got := Resolve("Europe/Istanbul").String(); got != "Europe/Istanbul" {
		t.Errorf("setting over TZ: %s", got)
	}
	t.Setenv("TZ", "Mars/Olympus")
	if got := Resolve("Nowhere/Else"); got != time.UTC {
		t.Errorf("invalid setting and TZ: %s, want UTC", got)
	}
}

func TestValid(t *testing.T) {
	for name, want := range map[string]bool{
		"Europe/Istanbul": true, "UTC": true, "America/Argentina/Buenos_Aires": true,
		"": false, "Local": false, "Mars/Olympus": false, "../etc/passwd": false, "europe/istanbul": false,
	} {
		if got := Valid(name); got != want {
			t.Errorf("Valid(%q) = %v, want %v", name, got, want)
		}
	}
}
