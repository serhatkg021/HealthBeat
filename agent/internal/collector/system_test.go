package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProc(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const meminfoSample = `MemTotal:       16203868 kB
MemFree:         1203868 kB
MemAvailable:    9431040 kB
Buffers:          204800 kB
Cached:          4915200 kB
SReclaimable:     122880 kB
`

func TestMemoryCollectorDetailAndSwapRate(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/meminfo", meminfoSample)
	writeProc(t, root, "proc/vmstat", "pswpin 1000\npswpout 400\noom_kill 3\n")
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c := &MemoryCollector{root: root, now: func() time.Time { return now }}

	first := c.Sample()
	if first.AvailableMB == nil || *first.AvailableMB != 9210 || first.CachedMB == nil || *first.CachedMB != 5120 {
		t.Fatalf("available/cached = %v/%v, want 9210/5120 MB", first.AvailableMB, first.CachedMB)
	}
	if first.OOMKills == nil || *first.OOMKills != 3 {
		t.Fatalf("oom_kills = %v, want 3", first.OOMKills)
	}
	if first.SwapInPerS != nil || first.SwapOutPerS != nil {
		t.Fatal("swap rate reported on the first sample; there is nothing to compare with yet")
	}

	now = now.Add(10 * time.Second)
	writeProc(t, root, "proc/vmstat", "pswpin 1050\npswpout 400\noom_kill 4\n")
	second := c.Sample()
	if second.SwapInPerS == nil || *second.SwapInPerS != 5 || second.SwapOutPerS == nil || *second.SwapOutPerS != 0 {
		t.Fatalf("swap in/out = %v/%v pages/s, want 5/0", second.SwapInPerS, second.SwapOutPerS)
	}

	now = now.Add(10 * time.Second)
	writeProc(t, root, "proc/vmstat", "pswpin 10\npswpout 5\noom_kill 0\n") // yeniden açılış: sayaçlar sıfırlandı
	if third := c.Sample(); third.SwapInPerS != nil {
		t.Fatalf("swap rate %v after the counters went backwards; that interval has no meaningful value", *third.SwapInPerS)
	}
}

func TestMemoryCollectorOnAnOldKernel(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/meminfo", "MemTotal: 1000 kB\nMemFree: 500 kB\nCached: 2048 kB\n") // MemAvailable yok
	writeProc(t, root, "proc/vmstat", "nr_free_pages 1\n")                                      // oom_kill, pswp* yok
	c := &MemoryCollector{root: root, now: time.Now}
	got := c.Sample()
	if got.AvailableMB != nil || got.OOMKills != nil || got.SwapInPerS != nil {
		t.Fatalf("missing kernel fields must stay unknown, got %+v", got)
	}
	if got.CachedMB == nil || *got.CachedMB != 2 {
		t.Fatalf("cached = %v, want 2 MB", got.CachedMB)
	}
	if empty := (&MemoryCollector{root: t.TempDir(), now: time.Now}).Sample(); empty != (MemoryStats{}) {
		t.Fatalf("no /proc at all gave %+v, want everything unknown", empty)
	}
}

func TestReadPressure(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/pressure/cpu", "some avg10=2.10 avg60=1.40 avg300=0.90 total=123\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	writeProc(t, root, "proc/pressure/io", "some avg10=4.00 avg60=3.20 avg300=1.00 total=9\nfull avg10=1.10 avg60=0.80 avg300=0.20 total=5\n")
	writeProc(t, root, "proc/pressure/memory", "garbage\n")
	got := ReadPressure(root)
	if got.CPU == nil || got.CPU.Some10 != 2.1 || got.CPU.Some60 != 1.4 || got.CPU.Full10 != nil {
		t.Fatalf("cpu = %+v; want some 2.1/1.4 and no system-level full", got.CPU)
	}
	if got.IO == nil || got.IO.Full10 == nil || *got.IO.Full10 != 1.1 || *got.IO.Full60 != 0.8 {
		t.Fatalf("io = %+v", got.IO)
	}
	if got.Memory != nil {
		t.Fatalf("unparsable memory pressure = %+v, want unknown", got.Memory)
	}
	if none := ReadPressure(t.TempDir()); none != (SystemPressure{}) {
		t.Fatalf("kernel without PSI gave %+v, want all unknown", none)
	}
}

func TestParseMDStat(t *testing.T) {
	text := `Personalities : [raid1] [raid6] [raid5] [raid4] [raid0]
md0 : active raid1 sdb1[1] sda1[0]
      976630464 blocks super 1.2 [2/2] [UU]
      bitmap: 0/8 pages [0KB], 65536KB chunk

md1 : active raid1 sdd1[1](F) sdc1[0]
      976630464 blocks super 1.2 [2/1] [U_]

md2 : active raid5 sdh[3] sdg[1] sdf[0]
      1953260544 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [UU_]
      [==>..................]  recovery = 12.6% (123297280/976630272) finish=74.2min speed=191652K/sec

md3 : active (auto-read-only) raid1 sdj[1] sdi[0]
      1000 blocks [2/2] [UU]
        resync=PENDING

md4 : active raid0 sdl[1] sdk[0]
      2000 blocks super 1.2 512k chunks

md5 : inactive sdm[0](S)
      1000 blocks

md6 : active raid1 sdo[1] sdn[0]
      1000 blocks [2/2] [UU]
      [=>...................]  check =  7.5% (75/1000) finish=1.0min speed=1K/sec

unused devices: <none>
`
	got := parseMDStat(text)
	want := []struct {
		name, level, state string
		devices, active    int
		sync               float64 // -1 = yok
	}{
		{"md0", "raid1", "clean", 2, 2, -1},
		{"md1", "raid1", "degraded", 2, 1, -1},
		{"md2", "raid5", "recovering", 3, 2, 12.6},
		{"md3", "raid1", "resyncing", 2, 2, -1},
		{"md4", "raid0", "clean", 2, 2, -1},
		{"md5", "", "inactive", 1, 1, -1},
		{"md6", "raid1", "checking", 2, 2, 7.5},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d arrays: %+v", len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.name || g.Level != w.level || g.State != w.state || g.Devices != w.devices || g.Active != w.active {
			t.Errorf("%s = %+v, want %+v", w.name, g, w)
		}
		if (w.sync < 0) != (g.SyncPct == nil) || (g.SyncPct != nil && *g.SyncPct != w.sync) {
			t.Errorf("%s sync = %v, want %v", w.name, g.SyncPct, w.sync)
		}
	}

	if arrays := parseMDStat("Personalities : \nunused devices: <none>\n"); arrays != nil {
		t.Fatalf("no arrays gave %+v", arrays)
	}
	if ReadRAID(t.TempDir()) != nil {
		t.Fatal("missing /proc/mdstat must mean no RAID data")
	}
}

func TestCPUBreakdownFromDelta(t *testing.T) {
	prev, _ := parseProcStatLine("cpu  100 0 100 700 50 0 0 50 0 0")
	cur, _ := parseProcStatLine("cpu  200 0 200 1300 100 0 0 200 0 0") // total 1000 → 2000: iowait +50, steal +150
	b := breakdownFromDelta(prev, cur)
	approx(t, *b.IOWaitPct, 5, "iowait%")
	approx(t, *b.StealPct, 15, "steal%")

	old, _ := parseProcStatLine("cpu  100 0 100 700")
	if b := breakdownFromDelta(old, old); b.IOWaitPct != nil || b.StealPct != nil {
		t.Fatalf("a kernel without iowait/steal columns must leave them unknown: %+v", b)
	}
	if b := breakdownFromDelta(cur, prev); b.IOWaitPct != nil {
		t.Fatal("counters going backwards must leave the breakdown unknown")
	}
}

func TestCPUCollectorReportsProcsBlockedOnThisHost(t *testing.T) {
	c := NewCPUCollector()
	if _, err := c.Sample(); err != nil {
		t.Skip(err)
	}
	b := c.Breakdown()
	if b.ProcsBlocked == nil || *b.ProcsBlocked < 0 || b.IOWaitPct == nil || b.StealPct == nil {
		t.Fatalf("breakdown on a modern kernel = %+v; want every field known", b)
	}
	if *b.IOWaitPct < 0 || *b.IOWaitPct > 100 {
		t.Fatalf("iowait = %v", *b.IOWaitPct)
	}
}
