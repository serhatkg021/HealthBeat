package version

import "testing"

func TestValidSemver(t *testing.T) {
	for s, want := range map[string]bool{
		"1.0.0": true, "0.0.1": true, "10.20.30": true, "1.2.3-rc.1": true, "1.2.3+build.5": true,
		"1.0": false, "v1.0.0": false, "1.0.0.0": false, "": false, "latest": false, "1.0.0 ": false, "1.a.0": false,
	} {
		if got := ValidSemver(s); got != want {
			t.Errorf("ValidSemver(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestOwnVersionIsValidAndProtocolIsAtLeastTwo(t *testing.T) {
	if !ValidSemver(Version) {
		t.Errorf("Version = %q is not SemVer", Version)
	}
	if !ValidSemver(LatestAgent) {
		t.Errorf("LatestAgent = %q is not SemVer", LatestAgent)
	}
	if Protocol < 2 {
		t.Errorf("Protocol = %d", Protocol)
	}
	if UserAgent() != "healthbeat-server/"+Version {
		t.Errorf("UserAgent = %q", UserAgent())
	}
}
