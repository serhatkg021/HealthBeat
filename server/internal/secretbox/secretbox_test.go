package secretbox

import (
	"bytes"
	"strings"
	"testing"
)

func newBox(t *testing.T, fill byte) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t, 1)
	for _, pt := range []string{"s3cret-token_value", "", "ünïcödé ✓", strings.Repeat("x", 10_000)} {
		sealed, err := b.Seal(pt, "row-1")
		if err != nil {
			t.Fatal(err)
		}
		if !IsSealed(sealed) {
			t.Fatalf("Seal output %q lacks the sealed prefix", sealed)
		}
		if pt != "" && strings.Contains(sealed, pt) {
			t.Fatal("plaintext visible in sealed value")
		}
		got, err := b.Open(sealed, "row-1")
		if err != nil || got != pt {
			t.Fatalf("Open = %q, %v; want %q", got, err, pt)
		}
	}
}

func TestSealIsRandomized(t *testing.T) {
	b := newBox(t, 1)
	x, _ := b.Seal("same", "row")
	y, _ := b.Seal("same", "row")
	if x == y {
		t.Fatal("two seals of the same value are identical (nonce reuse)")
	}
}

func TestOpenRejectsWrongKeyAADAndTampering(t *testing.T) {
	a, other := newBox(t, 1), newBox(t, 2)
	sealed, _ := a.Seal("secret", "row-1")

	if _, err := other.Open(sealed, "row-1"); err == nil {
		t.Error("opened with a different key")
	}
	if _, err := a.Open(sealed, "row-2"); err == nil {
		t.Error("opened with different associated data (ciphertext could be moved between rows)")
	}

	tampered := sealed[:len(sealed)-2] + "AA"
	if tampered == sealed {
		tampered = sealed[:len(sealed)-2] + "BB"
	}
	if _, err := a.Open(tampered, "row-1"); err == nil {
		t.Error("opened a tampered value")
	}

	for _, bad := range []string{"", "plaintext", "enc:v1:", "enc:v1:!!!", "enc:v1:AAAA", "enc:v2:" + sealed[len("enc:v1:"):]} {
		if _, err := a.Open(bad, "row-1"); err == nil {
			t.Errorf("Open(%q) succeeded", bad)
		}
	}
	if _, err := a.Open("plaintext", "row-1"); err != ErrNotSealed {
		t.Errorf("unsealed input: err = %v, want ErrNotSealed", err)
	}
}

func TestParseKey(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	b64 := "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc="
	hexKey := strings.Repeat("07", 32)

	for name, in := range map[string]string{"base64": b64, "hex": hexKey, "padded whitespace": "  " + b64 + "\n"} {
		got, err := ParseKey(in)
		if err != nil || !bytes.Equal(got, raw) {
			t.Errorf("%s: ParseKey = %x, %v", name, got, err)
		}
	}
	for name, in := range map[string]string{
		"empty": "", "too short": "BwcH", "not encoding": "!!!not a key!!!",
		"31 bytes hex": strings.Repeat("07", 31), "33 bytes hex": strings.Repeat("07", 33),
	} {
		if _, err := ParseKey(in); err == nil {
			t.Errorf("%s: ParseKey accepted an invalid key", name)
		}
	}
}

func TestNewRejectsWrongKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33} {
		if _, err := New(make([]byte, n)); err == nil {
			t.Errorf("New accepted a %d-byte key", n)
		}
	}
}
