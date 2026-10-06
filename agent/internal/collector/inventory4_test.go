package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- sıcaklık ----------------------------------------------------------------------------------------------------

func hwmon(t *testing.T, root, dir, driver string, sensors map[string]string) {
	t.Helper()
	writeProc(t, root, filepath.Join("sys/class/hwmon", dir, "name"), driver+"\n")
	for file, v := range sensors {
		writeProc(t, root, filepath.Join("sys/class/hwmon", dir, file), v+"\n")
	}
}

func TestReadTemperatures(t *testing.T) {
	root := t.TempDir()
	hwmon(t, root, "hwmon0", "acpitz", map[string]string{"temp1_input": "39000", "temp2_input": "41000"})
	hwmon(t, root, "hwmon2", "coretemp", map[string]string{
		"temp1_input": "41000", "temp1_label": "Package id 0", "temp1_max": "100000", "temp1_crit": "100000",
		"temp2_input": "40000", "temp2_label": "Core 0", "temp2_crit": "100000",
		"temp3_input": "47000", "temp3_label": "Core 1", "temp3_crit": "100000",
		"temp4_input": "43000", "temp4_label": "Core 2",
	})
	hwmon(t, root, "hwmon3", "nvme", map[string]string{"temp1_input": "27850", "temp1_label": "Composite", "temp1_max": "69850", "temp1_crit": "84850"})
	if err := os.Symlink("../../nvme0", filepath.Join(root, "sys/class/hwmon/hwmon3/device")); err != nil {
		t.Fatal(err)
	}
	hwmon(t, root, "hwmon4", "iwlwifi_1", map[string]string{"temp1_input": "garbage"})    // okunamayan sensör
	hwmon(t, root, "hwmon5", "pch_cometlake", map[string]string{"temp1_input": "500000"}) // akla yatkın değil

	got := map[string]TemperatureReading{}
	for _, r := range ReadTemperatures(root) {
		got[r.Sensor] = r
	}
	if len(got) != 5 {
		t.Fatalf("sensors = %v; want acpitz ×2, package, hottest core, nvme", got)
	}
	if pkg := got["coretemp/Package id 0"]; pkg.Kind != "cpu" || pkg.Celsius != 41 || pkg.Crit == nil || *pkg.Crit != 100 || *pkg.Max != 100 {
		t.Errorf("package = %+v", pkg)
	}
	if core := got["coretemp/hottest core"]; core.Celsius != 47 || core.Crit == nil || *core.Crit != 100 {
		t.Errorf("cores must collapse into the hottest one: %+v", core)
	}
	if nvme := got["nvme/nvme0/Composite"]; nvme.Kind != "disk" || nvme.Celsius != 27.85 || *nvme.Crit != 84.85 {
		t.Errorf("nvme = %+v; the disk must be named after its device", nvme)
	}
	if acpi := got["acpitz/temp2"]; acpi.Kind != "other" || acpi.Celsius != 41 || acpi.Max != nil {
		t.Errorf("unlabelled sensor = %+v", acpi)
	}
	if ReadTemperatures(t.TempDir()) != nil {
		t.Fatal("a machine without hwmon must report no temperatures")
	}
}

// ---- süreçler ----------------------------------------------------------------------------------------------------

// procFixture, sahte bir /proc'tur: pid -> (ad, durum, ppid, tick, rss sayfa, başlangıç).
type procFixture struct {
	t    *testing.T
	root string
}

func (f procFixture) proc(pid int, name string, state byte, ppid int, ticks uint64, rssPages int64, start uint64) {
	// alanlar: pid (ad) durum ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime
	// priority nice threads itrealvalue starttime vsize rss …
	line := fmt.Sprintf("%d (%s) %c %d 1 1 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 %d 1000 %d 0 0", pid, name, state, ppid, ticks, start, rssPages)
	writeProc(f.t, f.root, fmt.Sprintf("proc/%d/stat", pid), line)
}

func TestProcessCollector(t *testing.T) {
	root := t.TempDir()
	f := procFixture{t, root}
	pageMB := float64(os.Getpagesize()) / (1024 * 1024)
	pages := func(mb float64) int64 { return int64(mb / pageMB) }

	f.proc(1, "systemd", 'S', 0, 100, pages(10), 1)
	f.proc(2, "kthreadd", 'S', 0, 0, 0, 1)
	f.proc(50, "kworker/u16:2", 'I', 2, 5000, 0, 5)
	f.proc(100, "postgres", 'S', 1, 1000, pages(400), 10)
	f.proc(101, "postgres", 'S', 100, 1000, pages(300), 11)
	f.proc(200, "java", 'S', 1, 0, pages(2000), 12)
	f.proc(300, "my (odd) name", 'Z', 1, 0, 0, 13)
	writeProc(t, root, "proc/self/stat", "not a pid") // sayısal olmayan girdiler atlanır

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c := &ProcessCollector{root: root, now: func() time.Time { return now }, cpus: func() int { return 4 }}
	first, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 7 || first.Zombie != 1 || first.TopCPU != nil {
		t.Fatalf("first = %+v; want 7 processes, 1 zombie, no CPU ranking yet", first)
	}
	if top := first.TopRAM[0]; top.Name != "java" || top.Count != 1 {
		t.Fatalf("top RAM = %+v", first.TopRAM)
	}
	if pg := first.TopRAM[1]; pg.Name != "postgres" || pg.Count != 2 {
		t.Fatalf("second by RAM = %+v; same-named processes are one group", pg)
	}
	for _, g := range first.TopRAM {
		if strings.HasPrefix(g.Name, "kworker") || g.Name == "kthreadd" {
			t.Errorf("kernel thread %q in the groups", g.Name)
		}
	}
	if !hasGroup(first.TopRAM, "my (odd) name") {
		t.Errorf("a name with spaces and parentheses must be read whole: %+v", first.TopRAM)
	}

	// 60 sn, 4 CPU = 24000 tick kapasite. postgres +2400 tick (%10), java +1200 (%5), pid 101 yerine yeni bir süreç
	// (başlangıcı farklı) geldi: eski sayaçla karşılaştırılmaz.
	now = now.Add(time.Minute)
	f.proc(100, "postgres", 'S', 1, 3400, pages(400), 10)
	f.proc(101, "postgres", 'S', 100, 999999, pages(300), 99)
	f.proc(200, "java", 'S', 1, 1200, pages(2000), 12)
	second, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.TopCPU[0].Name != "postgres" || second.TopCPU[1].Name != "java" {
		t.Fatalf("top CPU = %+v", second.TopCPU)
	}
	approx(t, second.TopCPU[0].CPUPct, 10, "postgres cpu% of the whole machine")
	approx(t, second.TopCPU[1].CPUPct, 5, "java cpu%")
}

func hasGroup(gs []ProcessGroup, name string) bool {
	for _, g := range gs {
		if g.Name == name {
			return true
		}
	}
	return false
}

// hidepid ile bağlı /proc'ta PID 1 görünmez: eksik bir özet yanıltıcı olur, hata döner (alan gönderilmez).
func TestProcessCollectorRefusesAHiddenProcList(t *testing.T) {
	root := t.TempDir()
	procFixture{t, root}.proc(4242, "healthbeat-agen", 'R', 1, 1, 1, 1)
	c := &ProcessCollector{root: root, now: time.Now, cpus: func() int { return 1 }}
	if _, err := c.Collect(context.Background()); err == nil {
		t.Fatal("a /proc that hides other users' processes must not be reported as the full picture")
	}
}

// ---- güncellemeler -----------------------------------------------------------------------------------------------

func TestUpdatesCollector(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "etc/os-release", ubuntuOSRelease)
	writeProc(t, root, "var/lib/apt/lists/archive_noble-updates_InRelease", "x")
	writeProc(t, root, "var/lib/apt/lists/lock", "")
	stamp := time.Date(2026, 10, 6, 6, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(root, "var/lib/apt/lists/archive_noble-updates_InRelease"), stamp, stamp)

	out := `Listing...
alsa-ucm-conf/noble-updates,noble-updates 1.2.10-1ubuntu5.15 all [upgradable from: 1.2.10-1ubuntu5.14]
openssl/noble-updates,noble-security 3.0.13-0ubuntu3.6 amd64 [upgradable from: 3.0.13-0ubuntu3.5]
libssl3t64/noble-security 3.0.13-0ubuntu3.6 amd64 [upgradable from: 3.0.13-0ubuntu3.5]
code/stable 1.140.0-1790759618 amd64 [upgradable from: 1.137.0-1788902055]
`
	c := &UpdatesCollector{root: root, run: func(_ context.Context, name string, args ...string) (string, error) {
		if name != "apt" || strings.Join(args, " ") != "list --upgradable" {
			t.Errorf("unexpected command %s %v", name, args)
		}
		return out, nil
	}}
	got, err := c.Collect(context.Background())
	if err != nil || got == nil {
		t.Fatalf("Collect = %v, %v", got, err)
	}
	if got.Pending != 4 || got.Security != 2 || !got.ListsUpdatedAt.Equal(stamp) {
		t.Fatalf("updates = %+v; want 4 pending, 2 security, lists updated at %s", got, stamp)
	}

	rhel := t.TempDir()
	writeProc(t, rhel, "etc/os-release", "ID=rhel\nID_LIKE=\"fedora\"\n")
	c = &UpdatesCollector{root: rhel, run: func(context.Context, string, ...string) (string, error) {
		t.Error("apt must not run outside the apt family")
		return "", nil
	}}
	if got, err := c.Collect(context.Background()); got != nil || err != nil {
		t.Fatalf("non-apt family = %v, %v; want unknown", got, err)
	}
}

// ---- kapasite ----------------------------------------------------------------------------------------------------

func TestReadCapacity(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/sys/fs/file-nr", "12705\t0\t9223372036854775807\n")
	writeProc(t, root, "proc/sys/net/netfilter/nf_conntrack_count", "37\n")
	writeProc(t, root, "proc/sys/net/netfilter/nf_conntrack_max", "262144\n")
	writeProc(t, root, "proc/loadavg", "0.46 0.54 0.44 2/1234 99999\n")
	writeProc(t, root, "proc/sys/kernel/pid_max", "4194304\n")
	c := ReadCapacity(root)
	if *c.FileHandles != 12705 || *c.FileHandlesMax != 9223372036854775807 || *c.Conntrack != 37 || *c.ConntrackMax != 262144 ||
		*c.Tasks != 1234 || *c.PIDMax != 4194304 {
		t.Fatalf("capacity = %+v", c)
	}

	noConntrack := t.TempDir()
	writeProc(t, noConntrack, "proc/sys/net/netfilter/nf_conntrack_count", "5\n") // max yok: ikisi birlikte anlamlı
	if c := ReadCapacity(noConntrack); c != (CapacityStats{}) {
		t.Fatalf("capacity without sources = %+v; want everything unknown", c)
	}
}
