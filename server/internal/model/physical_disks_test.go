package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestSanitizePhysicalDisksKeepsValidDisks(t *testing.T) {
	in := []PhysicalDisk{{Name: "nvme0n1", Model: "CT500P2SSD8", SizeBytes: 500107862016, Kind: "nvme", Mounts: []string{"/", "/boot/efi"}}}
	if got := sanitizePhysicalDisks(in); !reflect.DeepEqual(got, in) {
		t.Errorf("got %+v, want unchanged %+v", got, in)
	}
}

func TestSanitizePhysicalDisksUnknownIsNil(t *testing.T) {
	if got := sanitizePhysicalDisks(nil); got != nil {
		t.Errorf("nil in -> %+v, want nil", got)
	}
	// Adı olmayan tek diskten geriye bir şey kalmaz: "bilinmiyor" sayılmalı, boş liste değil.
	if got := sanitizePhysicalDisks([]PhysicalDisk{{Name: "  "}}); got != nil {
		t.Errorf("nameless -> %+v, want nil", got)
	}
}

func TestSanitizePhysicalDisksCleansHostileInput(t *testing.T) {
	long := "a" + strings.Repeat("ş", 200) // baştaki tek bayt, kırpma sınırını (16/128) rune ortasına iter
	in := []PhysicalDisk{
		{Name: "sda", Model: "M\x00od\x01el", SizeBytes: -5, Kind: long, Mounts: nil},
		{Name: "sda", Model: "duplicate"},                                  // tekrar
		{Name: "", Model: "no name"},                                       // adsız
		{Name: "sdb", Model: long, Mounts: []string{"", "/ok", "/a\x00b"}}, // boş/denetim karakterli mount
	}
	got := sanitizePhysicalDisks(in)
	if len(got) != 2 || got[0].Name != "sda" || got[1].Name != "sdb" {
		t.Fatalf("got %+v, want sda then sdb only", got)
	}
	if got[0].Model != "Model" || got[0].SizeBytes != 0 {
		t.Errorf("sda = %+v, want control chars stripped and negative size 0", got[0])
	}
	if got[0].Mounts == nil || len(got[0].Mounts) != 0 {
		t.Errorf("sda mounts = %#v, want empty non-nil slice (JSON [] not null)", got[0].Mounts)
	}
	if len(got[0].Kind) > maxDiskKindBytes || len(got[1].Model) > maxDiskModelBytes {
		t.Errorf("not truncated: kind %d, model %d bytes", len(got[0].Kind), len(got[1].Model))
	}
	for _, s := range []string{got[0].Kind, got[1].Model} {
		if strings.ContainsRune(s, '�') || strings.ToValidUTF8(s, "") != s {
			t.Errorf("truncation split a rune: %q", s)
		}
	}
	if !reflect.DeepEqual(got[1].Mounts, []string{"/ok", "/ab"}) {
		t.Errorf("sdb mounts = %v, want [/ok /ab]", got[1].Mounts)
	}
}

func TestSanitizePhysicalDisksCapsCounts(t *testing.T) {
	var in []PhysicalDisk
	for i := 0; i < maxPhysicalDisks+10; i++ {
		d := PhysicalDisk{Name: "d" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('A'+i/26))}
		for j := 0; j < maxMountsPerDisk+10; j++ {
			d.Mounts = append(d.Mounts, "/m"+string(rune('a'+j%26))+string(rune('A'+j/26)))
		}
		in = append(in, d)
	}
	got := sanitizePhysicalDisks(in)
	if len(got) > maxPhysicalDisks {
		t.Errorf("%d disks kept, cap is %d", len(got), maxPhysicalDisks)
	}
	if len(got[0].Mounts) != maxMountsPerDisk {
		t.Errorf("%d mounts kept, cap is %d", len(got[0].Mounts), maxMountsPerDisk)
	}
}

func TestHardwareUsesSanitizedDisks(t *testing.T) {
	r := MetricsIngestRequest{CPUCores: 4, RAMTotalMB: 8000, PhysicalDisks: []PhysicalDisk{{Name: "sda\x00"}}}
	hw := r.Hardware()
	if hw.CPUCores != 4 || hw.RAMTotalMB != 8000 || len(hw.PhysicalDisks) != 1 || hw.PhysicalDisks[0].Name != "sda" {
		t.Errorf("Hardware() = %+v", hw)
	}
}
