package model

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func boolp(b bool) *bool { return &b }
func intp(i int) *int    { return &i }

func fullHostInfo() *HostInfo {
	return &HostInfo{
		Hostname:       "monster",
		OS:             &OSInfo{ID: "ubuntu", Name: "Ubuntu", PrettyName: "Ubuntu 24.04.5 LTS", VersionID: "24.04", IDLike: "debian"},
		Kernel:         &KernelInfo{Release: "7.0.0-31-generic", Arch: "x86_64"},
		CPUModel:       "Intel(R) Core(TM) i7",
		Virtualization: &VirtualizationInfo{Kind: "physical"},
		Machine:        &MachineInfo{Vendor: "MONSTER", Model: "ABRA"},
		MachineIDHash:  "0123456789abcdef0123456789abcdef",
		Timezone:       "Europe/Istanbul",
		Init:           "systemd",
		SecurityModule: "apparmor",
		BootTime:       "2026-09-20T09:30:00Z",
		UptimeSeconds:  18034,
		LoadAvg:        []float64{0.84, 1.09, 1.18},
		Swap:           &SwapInfo{TotalMB: 2047, UsedMB: 12},
		Addresses:      []NetworkAddress{{Interface: "enp3s0", Address: "192.168.1.106/24"}, {Interface: "enp3s0", Address: "2001:db8::106/64"}},
		RebootRequired: boolp(false), TimeSynced: boolp(true), FailedUnits: intp(0), DockerVersion: "29.8.1",
	}
}

func TestSanitizeHostInfoKeepsAValidInventoryUnchanged(t *testing.T) {
	in := fullHostInfo()
	if got := sanitizeHostInfo(in); !reflect.DeepEqual(got, fullHostInfo()) {
		t.Errorf("valid inventory changed:\n got  %+v\n want %+v", got, fullHostInfo())
	}
	// Girdiyi değiştirmemeli (işaretçiler paylaşılmamalı).
	if !reflect.DeepEqual(in, fullHostInfo()) {
		t.Error("input was mutated")
	}
}

func TestSanitizeHostInfoUnknownIsNil(t *testing.T) {
	for name, in := range map[string]*HostInfo{
		"nil":            nil,
		"empty":          {},
		"only garbage":   {Hostname: "\x00\x01", OS: &OSInfo{}, Kernel: &KernelInfo{}, Swap: &SwapInfo{TotalMB: -1}, LoadAvg: []float64{1}},
		"only bad addrs": {Addresses: []NetworkAddress{{Address: "not-an-ip"}}},
	} {
		if got := sanitizeHostInfo(in); got != nil {
			t.Errorf("%s: got %+v, want nil (\"unknown\" must keep the stored value)", name, got)
		}
	}
}

// Unknown ("nil") booleans/counters must stay distinguishable from false/0.
func TestSanitizeHostInfoKeepsUnknownDistinctFromFalseAndZero(t *testing.T) {
	got := sanitizeHostInfo(&HostInfo{Hostname: "h"})
	if got.RebootRequired != nil || got.TimeSynced != nil || got.FailedUnits != nil {
		t.Errorf("unknowns became values: %+v", got)
	}
	got = sanitizeHostInfo(&HostInfo{Hostname: "h", RebootRequired: boolp(false), TimeSynced: boolp(false), FailedUnits: intp(0)})
	if got.RebootRequired == nil || *got.RebootRequired || got.TimeSynced == nil || *got.TimeSynced || got.FailedUnits == nil || *got.FailedUnits != 0 {
		t.Errorf("false/0 were dropped: %+v", got)
	}
}

func TestSanitizeHostInfoCleansHostileText(t *testing.T) {
	long := "a" + strings.Repeat("ş", 300)
	got := sanitizeHostInfo(&HostInfo{
		Hostname: "ho\x00st\x1b[31m", CPUModel: long, Timezone: long, DockerVersion: long,
		OS:     &OSInfo{PrettyName: long, ID: "ubu\x00ntu"},
		Kernel: &KernelInfo{Release: long},
	})
	if got.Hostname != "host[31m" {
		t.Errorf("hostname = %q", got.Hostname)
	}
	if got.OS.ID != "ubuntu" {
		t.Errorf("os.id = %q", got.OS.ID)
	}
	for name, s := range map[string]string{"cpu": got.CPUModel, "tz": got.Timezone, "docker": got.DockerVersion, "pretty": got.OS.PrettyName, "kernel": got.Kernel.Release} {
		if len(s) > maxHostTextBytes || strings.ToValidUTF8(s, "") != s {
			t.Errorf("%s not truncated on a rune boundary: %d bytes", name, len(s))
		}
	}
}

func TestSanitizeHostInfoEnumsAndRanges(t *testing.T) {
	got := sanitizeHostInfo(&HostInfo{
		Hostname:       "h",
		Virtualization: &VirtualizationInfo{Kind: "hologram", Vendor: "X"},
		Init:           "sysvinit", SecurityModule: "tomoyo",
		MachineIDHash: "NOT-HEX-NOT-HEX-NOT-HEX-NOT-HEX!!",
		BootTime:      "yesterday", UptimeSeconds: -5,
		LoadAvg: []float64{math.NaN(), 1, 1},
		Swap:    &SwapInfo{TotalMB: 100, UsedMB: 200},
	})
	if got.Virtualization == nil || got.Virtualization.Kind != "unknown" || got.Virtualization.Vendor != "X" {
		t.Errorf("virtualization = %+v, want kind unknown", got.Virtualization)
	}
	if got.Init != "" || got.SecurityModule != "" || got.MachineIDHash != "" || got.BootTime != "" || got.UptimeSeconds != 0 || got.LoadAvg != nil || got.Swap != nil {
		t.Errorf("invalid values were kept: %+v", got)
	}
	for _, kind := range []string{"physical", "vm", "container", "unknown"} {
		if g := sanitizeHostInfo(&HostInfo{Virtualization: &VirtualizationInfo{Kind: kind}}); g == nil || g.Virtualization.Kind != kind {
			t.Errorf("kind %q not kept", kind)
		}
	}
}

func TestSanitizeHostInfoBootTimeAndUptimeBounds(t *testing.T) {
	future := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	if g := sanitizeHostInfo(&HostInfo{Hostname: "h", BootTime: future}); g.BootTime != "" {
		t.Errorf("a boot time in the future was kept: %q", g.BootTime)
	}
	if g := sanitizeHostInfo(&HostInfo{Hostname: "h", BootTime: "1969-12-31T00:00:00Z"}); g.BootTime != "" {
		t.Errorf("a pre-epoch boot time was kept: %q", g.BootTime)
	}
	if g := sanitizeHostInfo(&HostInfo{Hostname: "h", BootTime: "2026-09-20T12:30:00+03:00"}); g.BootTime != "2026-09-20T09:30:00Z" {
		t.Errorf("boot time not normalized to UTC: %q", g.BootTime)
	}
	if g := sanitizeHostInfo(&HostInfo{Hostname: "h", UptimeSeconds: maxUptimeSeconds + 1}); g.UptimeSeconds != 0 {
		t.Error("an absurd uptime was kept")
	}
}

func TestSanitizeHostInfoAddresses(t *testing.T) {
	got := sanitizeHostInfo(&HostInfo{Addresses: []NetworkAddress{
		{Interface: "eth0", Address: "192.168.1.10/24"},
		{Interface: "eth0", Address: "10.0.0.5"},                 // öneksiz kabul
		{Interface: "eth0", Address: "2001:0DB8:0:0:0:0:0:1/64"}, // kanonik hâle gelir
		{Interface: "eth0", Address: "999.1.1.1"},                // geçersiz
		{Interface: "eth0", Address: "1.2.3.4/33"},               // geçersiz ön ek
		{Interface: "et\x00h1", Address: "172.16.0.1/12"},
		{Interface: "eth0", Address: "'; DROP TABLE hosts; --"},
	}})
	want := []NetworkAddress{
		{"eth0", "192.168.1.10/24"}, {"eth0", "10.0.0.5"}, {"eth0", "2001:db8::1/64"}, {"eth1", "172.16.0.1/12"},
	}
	if !reflect.DeepEqual(got.Addresses, want) {
		t.Errorf("addresses = %+v\nwant       %+v", got.Addresses, want)
	}

	var many []NetworkAddress
	for i := 0; i < maxAddressEntries+20; i++ {
		many = append(many, NetworkAddress{Interface: "e", Address: "10.0.0." + string(rune('0'+i%10)) + "/24"})
	}
	if g := sanitizeHostInfo(&HostInfo{Addresses: many}); len(g.Addresses) != maxAddressEntries {
		t.Errorf("%d addresses kept, cap is %d", len(g.Addresses), maxAddressEntries)
	}
}

func TestHostInfoRoundTripsThroughJSON(t *testing.T) {
	b, err := json.Marshal(fullHostInfo())
	if err != nil {
		t.Fatal(err)
	}
	var back HostInfo
	if err := json.Unmarshal(b, &back); err != nil || !reflect.DeepEqual(&back, fullHostInfo()) {
		t.Errorf("round trip = %+v (err %v)", back, err)
	}
	// false ve 0 "bilinmiyor"dan ayırt edilebilmeli: omitempty işaretçiyi atmamalı
	for _, key := range []string{`"reboot_required":false`, `"time_synced":true`, `"failed_units":0`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON lacks %s: %s", key, b)
		}
	}
}

func TestIngestWithHostInfoFixtureYieldsSanitizedHardware(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(payloadDir, "v3_inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	req, unknown, err := ParseMetricsIngest(data)
	if err != nil || len(unknown) != 0 {
		t.Fatalf("parse: err %v unknown %v (host_info must be a KNOWN field)", err, unknown)
	}
	hw := req.Hardware()
	if hw.HostInfo == nil || hw.HostInfo.OS == nil || hw.HostInfo.OS.PrettyName != "Ubuntu 24.04.5 LTS" || len(hw.HostInfo.Addresses) != 2 {
		t.Errorf("HostInfo = %+v", hw.HostInfo)
	}
	if req.Disk[0].InodesUsedPct == nil || *req.Disk[0].InodesUsedPct != 4.2 || req.Disk[1].InodesUsedPct != nil {
		t.Errorf("inode pct not parsed: %+v", req.Disk)
	}
}

func TestParseMetricsIngestDropsBadInodePercentages(t *testing.T) {
	body := `{"cpu_usage_pct":1,"ram_usage_pct":1,"docker_containers":[],"disk":[
		{"mount":"/a","used_pct":1,"total":1,"free":1,"inodes_used_pct":101},
		{"mount":"/b","used_pct":1,"total":1,"free":1,"inodes_used_pct":-1},
		{"mount":"/c","used_pct":1,"total":1,"free":1,"inodes_used_pct":50},
		{"mount":"/d","used_pct":1,"total":1,"free":1,"inodes_used_pct":0}]}`
	req, _, err := ParseMetricsIngest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if req.Disk[0].InodesUsedPct != nil || req.Disk[1].InodesUsedPct != nil {
		t.Error("out-of-range inode percentages were kept")
	}
	if req.Disk[2].InodesUsedPct == nil || *req.Disk[2].InodesUsedPct != 50 || req.Disk[3].InodesUsedPct == nil || *req.Disk[3].InodesUsedPct != 0 {
		t.Errorf("valid inode percentages (incl. 0) were dropped: %+v", req.Disk)
	}
}

// Yük ortalaması: her konumda her geçersiz değer türü (NaN, ±Inf, negatif, devasa) tüm diziyi atar; yanlış
// uzunluk da öyle. Geçerli dizi (0 dahil) korunur.
func TestSanitizeHostInfoLoadAvgIsValidatedPerElement(t *testing.T) {
	bad := []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1, 1e12}
	for pos := 0; pos < 3; pos++ {
		for _, v := range bad {
			la := []float64{0.5, 0.5, 0.5}
			la[pos] = v
			if g := sanitizeHostInfo(&HostInfo{Hostname: "h", LoadAvg: la}); g.LoadAvg != nil {
				t.Errorf("load_avg %v (position %d) was kept", la, pos)
			}
		}
	}
	for _, la := range [][]float64{nil, {1}, {1, 2}, {1, 2, 3, 4}} {
		if g := sanitizeHostInfo(&HostInfo{Hostname: "h", LoadAvg: la}); g.LoadAvg != nil {
			t.Errorf("load_avg of length %d was kept", len(la))
		}
	}
	if g := sanitizeHostInfo(&HostInfo{Hostname: "h", LoadAvg: []float64{0, 0, 0}}); len(g.LoadAvg) != 3 {
		t.Error("an idle machine (0 0 0) must keep its load average")
	}
}
