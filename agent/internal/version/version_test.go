package version

import (
	"regexp"
	"strings"
	"testing"
)

// Server sürüm başlığını SemVer benzeri bir desenle doğrular; agent'ın kendi sürümü bunu geçmeli.
func TestVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`).MatchString(Version) {
		t.Errorf("Version = %q, want SemVer", Version)
	}
}

// Protokol 1 "sürüm bildirmeyen eski agent"tır; sürüm bildiren her agent en az 2 olmalı.
func TestProtocolIsAtLeastTwo(t *testing.T) {
	if Protocol < 2 {
		t.Errorf("Protocol = %d; 1 is reserved for legacy agents that send no headers", Protocol)
	}
}

func TestUserAgentAndString(t *testing.T) {
	if got := UserAgent(); got != "healthbeat-agent/"+Version {
		t.Errorf("UserAgent = %q", got)
	}
	if s := String(); !strings.Contains(s, Version) || !strings.Contains(s, "protocol") {
		t.Errorf("String = %q", s)
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1}, {"1.9.0", "1.10.0", -1}, {"2.0.0", "1.99.99", 1}, {"1.2.3", "1.2.3", 0},
		{"1.2.0-rc.1", "1.2.0", -1}, {"1.2.0", "1.2.0-rc.1", 1}, {"1.2.0-alpha", "1.2.0-beta", -1},
		{"1.2.0+build.5", "1.2.0", 0},
		{"dev", "1.0.0", 0}, {"1.0.0", "", 0}, {"1.0", "1.0.0", 0},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
