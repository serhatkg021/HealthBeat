package model

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	ok := map[string]string{
		"alice@example.com":       "alice@example.com",
		"  Alice@Example.COM  ":   "alice@example.com",
		"first.last+tag@sub.x.io": "first.last+tag@sub.x.io",
		"ADMIN@HEALTHBEAT.LOCAL":  "admin@healthbeat.local",
		"a@x.com\n":               "a@x.com", // çevredeki boşluklar kırpılır, reddedilmez
	}
	for in, want := range ok {
		got, err := NormalizeEmail(in)
		if err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "   ", "no-at-sign", "@x.com", "a@", "a b@x.com", "a@x.com, b@y.com",
		"Alice <alice@x.com>", "<alice@x.com>", "a@x.com\r\nBcc: e@evil.test", "a;b@x.com",
	} {
		if got, err := NormalizeEmail(bad); err == nil {
			t.Errorf("NormalizeEmail(%q) = %q, want an error", bad, got)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for _, ok := range []string{
		"exactly12chr", "correct horse battery staple", "ünïcödé-pässwörd-1", strings.Repeat("a", 72),
	} {
		if err := ValidatePassword(ok); err != nil {
			t.Errorf("ValidatePassword(%q) = %v, want ok", ok, err)
		}
	}
	for name, bad := range map[string]string{
		"empty": "", "eleven chars": "elevenchars", "short": "abc", "73 bytes": strings.Repeat("a", 73),
		"multibyte over 72 bytes": strings.Repeat("ş", 37), // 37 rune, 74 bayt
	} {
		if err := ValidatePassword(bad); err == nil {
			t.Errorf("%s: ValidatePassword accepted %q", name, bad)
		}
	}
	// Uzunluk bayt değil karakter olarak sayılır: 12 iki baytlık rune yeterlidir.
	if err := ValidatePassword(strings.Repeat("ş", 12)); err != nil {
		t.Errorf("12 multibyte characters rejected: %v", err)
	}
}

func TestValidateMountList(t *testing.T) {
	for _, ok := range [][]string{
		nil, {}, {"/"}, {"/", "/data", "/var/lib/docker"}, {"/mnt/My Disk"}, {"/mnt/yedek-ş"}, {"/a/b/c.d_e-f"},
	} {
		if err := ValidateMountList(ok); err != nil {
			t.Errorf("ValidateMountList(%q) = %v, want ok", ok, err)
		}
	}
	many := make([]string, 65)
	for i := range many {
		many[i] = fmt.Sprintf("/m%d", i)
	}
	for name, bad := range map[string][]string{
		"relative":      {"data"},
		"empty entry":   {""},
		"control char":  {"/da\x00ta"},
		"newline":       {"/data\n"},
		"duplicate":     {"/data", "/data"},
		"too long":      {"/" + strings.Repeat("a", 255)},
		"too many":      many,
		"windows drive": {"C:\\data"},
	} {
		if err := ValidateMountList(bad); err == nil {
			t.Errorf("%s: accepted %q", name, bad)
		}
	}
}
