package agentpkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		want Info
	}{
		{"healthbeat-agent_1.1.0_amd64.deb", Info{"1.1.0", "amd64", "deb"}},
		{"healthbeat-agent_1.1.0_arm64.deb", Info{"1.1.0", "arm64", "deb"}},
		{"healthbeat-agent-1.1.0-1.x86_64.rpm", Info{"1.1.0", "amd64", "rpm"}},
		{"healthbeat-agent-1.1.0-1.aarch64.rpm", Info{"1.1.0", "arm64", "rpm"}},
		{"healthbeat-agent_1.1.0_linux_amd64.tar.gz", Info{"1.1.0", "amd64", "tar.gz"}},
		{"healthbeat-agent_1.1.0_linux_arm64.tar.gz", Info{"1.1.0", "arm64", "tar.gz"}},
		{"healthbeat-agent_1.2.0~rc.1_amd64.deb", Info{"1.2.0-rc.1", "amd64", "deb"}},
		{"healthbeat-agent-1.2.0~rc.1-1.x86_64.rpm", Info{"1.2.0-rc.1", "amd64", "rpm"}},
		{"healthbeat-agent_1.2.0-rc.1_linux_arm64.tar.gz", Info{"1.2.0-rc.1", "arm64", "tar.gz"}},
		{"healthbeat-agent_10.20.30_amd64.deb", Info{"10.20.30", "amd64", "deb"}},
	}
	for _, c := range cases {
		got, err := Parse(c.name)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, name := range []string{
		"",
		"SHA256SUMS",
		"SHA256SUMS.asc",
		"RELEASE_NOTES.md",
		"healthbeat-agent_1.1.0_linux_amd64",    // ham binary
		"healthbeat-agent_1.1.0_i386.deb",       // bilinmeyen mimari
		"healthbeat-agent-1.1.0-2.x86_64.rpm",   // paket sürümü 1 değil
		"healthbeat-agent_1.1_amd64.deb",        // eksik sürüm
		"healthbeat-agent_1.1.0-rc.1_amd64.deb", // .deb önsürümü '~' ile yazılır
		"healthbeat-agent_1.1.0~rc.1_linux_amd64.tar.gz",
		"../healthbeat-agent_1.1.0_amd64.deb",
		"x/healthbeat-agent_1.1.0_amd64.deb",
		"healthbeat-agent_1.1.0_amd64.deb/",
		"healthbeat-agent_1.1.0_amd64.deb.exe",
		"HEALTHBEAT-AGENT_1.1.0_AMD64.DEB",
		"healthbeat-server_2.2.0.tar.gz",
		"healthbeat-agent_1.1.0_amd64.deb\n",
	} {
		if info, err := Parse(name); !errors.Is(err, ErrUnknownFilename) {
			t.Errorf("Parse(%q) = %+v, %v; want ErrUnknownFilename", name, info, err)
		}
	}
}

// Biçimlerin ilk baytları (testlerde paket içeriğinin başına konur).
const (
	debHead = "!<arch>\n"
	rpmHead = "\xed\xab\xee\xdb"
	tgzHead = "\x1f\x8b"
)

func TestFileNameRoundTrip(t *testing.T) {
	cases := []struct {
		info Info
		want string
	}{
		{Info{"1.1.0", "amd64", "deb"}, "healthbeat-agent_1.1.0_amd64.deb"},
		{Info{"1.1.0", "arm64", "rpm"}, "healthbeat-agent-1.1.0-1.aarch64.rpm"},
		{Info{"1.1.0", "amd64", "rpm"}, "healthbeat-agent-1.1.0-1.x86_64.rpm"},
		{Info{"1.1.0", "arm64", "tar.gz"}, "healthbeat-agent_1.1.0_linux_arm64.tar.gz"},
		{Info{"1.2.0-rc.1", "amd64", "deb"}, "healthbeat-agent_1.2.0~rc.1_amd64.deb"},
		{Info{"1.2.0-rc.1", "arm64", "rpm"}, "healthbeat-agent-1.2.0~rc.1-1.aarch64.rpm"},
		{Info{"1.2.0-rc.1", "amd64", "tar.gz"}, "healthbeat-agent_1.2.0-rc.1_linux_amd64.tar.gz"},
	}
	for _, c := range cases {
		got, err := FileName(c.info)
		if err != nil || got != c.want {
			t.Errorf("FileName(%+v) = %q, %v; want %q", c.info, got, err, c.want)
			continue
		}
		if back, err := Parse(got); err != nil || back != c.info {
			t.Errorf("Parse(FileName(%+v)) = %+v, %v", c.info, back, err)
		}
	}
}

func TestFileNameRejects(t *testing.T) {
	for _, info := range []Info{
		{"", "amd64", "deb"},
		{"1.1", "amd64", "deb"},
		{"v1.1.0", "amd64", "deb"},
		{"1.1.0-rc-1", "amd64", "deb"}, // önsürümde '-' Parse ile geri okunamaz
		{"1.1.0+build", "amd64", "deb"},
		{"1.1.0/../x", "amd64", "deb"},
		{"1.1.0-" + strings.Repeat("a", 60), "amd64", "deb"},
		{"1.1.0", "i386", "deb"},
		{"1.1.0", "x86_64", "rpm"},
		{"1.1.0", "amd64", "zip"},
	} {
		if name, err := FileName(info); !errors.Is(err, ErrInvalidInfo) {
			t.Errorf("FileName(%+v) = %q, %v; want ErrInvalidInfo", info, name, err)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]string{
		debHead + "debian-binary": "deb",
		rpmHead + "\x03\x00":      "rpm",
		tgzHead + "\x08\x00":      "tar.gz",
		"PK\x03\x04":              "", // zip
		"#!/bin/sh\n":             "",
		"!<arch>":                 "", // eksik başlık
		"":                        "",
	}
	for head, want := range cases {
		if got := DetectFormat([]byte(head)); got != want {
			t.Errorf("DetectFormat(%q) = %q, want %q", head, got, want)
		}
	}
}

func TestStagePlaceOpenRemove(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent-packages")
	d, err := NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(debHead + "paket içeriği")
	s, err := d.Stage(bytes.NewReader(body), 1024)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if s.Size != int64(len(body)) || s.SHA256 != hex.EncodeToString(sum[:]) || s.Format != "deb" {
		t.Fatalf("staged size=%d sha=%s format=%s", s.Size, s.SHA256, s.Format)
	}
	if err := s.Place("1.1.0", "healthbeat-agent_1.1.0_amd64.deb"); err != nil {
		t.Fatal(err)
	}
	s.Discard() // Place'ten sonra bir şey yapmamalı

	info, err := os.Stat(filepath.Join(root, "1.1.0", "healthbeat-agent_1.1.0_amd64.deb"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode = %v, want 0640", info.Mode().Perm())
	}
	f, err := d.Open("1.1.0", "healthbeat-agent_1.1.0_amd64.deb")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	_ = f.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("read %q, want %q", got, body)
	}
	assertNoTemp(t, root)

	if err := d.Remove("1.1.0", "healthbeat-agent_1.1.0_amd64.deb"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "1.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty version directory left behind: %v", err)
	}
	if err := d.Remove("1.1.0", "healthbeat-agent_1.1.0_amd64.deb"); err != nil {
		t.Fatalf("removing a missing file: %v", err)
	}
}

func TestPlaceOverwritesOrphan(t *testing.T) {
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{debHead + "eski", debHead + "yeni"} {
		s, err := d.Stage(strings.NewReader(body), 1024)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Place("1.1.0", "healthbeat-agent_1.1.0_amd64.deb"); err != nil {
			t.Fatal(err)
		}
	}
	f, err := d.Open("1.1.0", "healthbeat-agent_1.1.0_amd64.deb")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got, _ := io.ReadAll(f); string(got) != debHead+"yeni" {
		t.Fatalf("content = %q, want the newer upload", got)
	}
}

func TestStageRejectsTooLargeAndEmpty(t *testing.T) {
	root := t.TempDir()
	d, err := NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stage(strings.NewReader(tgzHead+"123"), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("5 bytes with a 4 byte limit: %v, want ErrTooLarge", err)
	}
	if s, err := d.Stage(strings.NewReader(tgzHead+"12"), 4); err != nil || s.Format != "tar.gz" {
		t.Fatalf("exactly at the limit: %+v, %v", s, err)
	}
	if _, err := d.Stage(strings.NewReader(""), 4); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty body: %v, want ErrEmpty", err)
	}
	if _, err := d.Stage(strings.NewReader("SHA256SUMS içeriği"), 1024); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("unknown content: %v, want ErrUnknownFormat", err)
	}
	if s, err := d.Stage(strings.NewReader(rpmHead), 1024); err != nil || s.Format != "rpm" {
		t.Fatalf("rpm header only: %+v, %v", s, err)
	}
	// Reddedilenlerin geçici dosyası kalmaz; kabul edilen (yerine konmamış) iki tane kalır.
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatalf("entries after staging = %d, want 2 (the accepted ones)", len(entries))
	}
}

func TestNewDirRejectsUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	if _, err := NewDir(root); err == nil {
		t.Fatal("NewDir on a read-only directory succeeded")
	}
}

func assertNoTemp(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}
