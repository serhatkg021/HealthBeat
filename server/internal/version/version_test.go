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
	if Protocol < 2 {
		t.Errorf("Protocol = %d", Protocol)
	}
	if UserAgent() != "healthbeat-server/"+Version {
		t.Errorf("UserAgent = %q", UserAgent())
	}
}

func TestCompare(t *testing.T) {
	// Her satır küçükten büyüğe sıralı (SemVer 2.0.0 §11 örneği ve sayısal karşılaştırma).
	ordered := []string{
		"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
		"1.0.0-rc.1", "1.0.0", "1.0.1", "1.2.0", "1.10.0", "2.0.0", "10.0.0",
	}
	for i, a := range ordered {
		for j, b := range ordered {
			want := compareInt(i, j)
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", a, b, got, want)
			}
		}
	}
	for _, eq := range [][2]string{{"1.0.0", "1.0.0+build.5"}, {"1.0.0-rc.1+x", "1.0.0-rc.1"}} {
		if got := Compare(eq[0], eq[1]); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0 (build metadata is ignored)", eq[0], eq[1], got)
		}
	}
}
