package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// diskLine, /proc/diskstats satırı üretir (20 sütunlu yeni biçim; eski 14 sütunlu biçim ayrıca denenir).
func diskLine(name string, reads, sectorsRead, msRead, writes, sectorsWritten, msWrite, ioTicks, weighted uint64) string {
	return fmt.Sprintf(" 259 0 %s %d 0 %d %d %d 0 %d %d 0 %d %d 0 0 0 0 0 0\n", name, reads, sectorsRead, msRead, writes, sectorsWritten, msWrite, ioTicks, weighted)
}

const netDevHeader = "Inter-|   Receive                                                |  Transmit\n" +
	" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"

func netLine(name string, rxBytes, rxErrs, rxDrop, txBytes, txErrs, txDrop uint64) string {
	return fmt.Sprintf("%6s: %d 10 %d %d 0 0 0 0 %d 10 %d %d 0 0 0 0\n", name, rxBytes, rxErrs, rxDrop, txBytes, txErrs, txDrop)
}

func snmpTcp(estab, outSegs, retrans uint64) string {
	return "Ip: Forwarding DefaultTTL\nIp: 1 64\n" +
		"Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors\n" +
		fmt.Sprintf("Tcp: 1 200 120000 -1 10 10 0 0 %d 100 %d %d 0 0 0\n", estab, outSegs, retrans)
}

type ioFixture struct {
	t    *testing.T
	root string
	now  time.Time
	c    *IOCollector
}

func newIOFixture(t *testing.T) *ioFixture {
	f := &ioFixture{t: t, root: t.TempDir(), now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	f.c = &IOCollector{root: f.root, now: func() time.Time { return f.now }}
	return f
}

func (f *ioFixture) write(diskstats, netdev, snmp, sockstat string) {
	writeProc(f.t, f.root, "proc/diskstats", diskstats)
	writeProc(f.t, f.root, "proc/net/dev", netDevHeader+netdev)
	writeProc(f.t, f.root, "proc/net/snmp", snmp)
	writeProc(f.t, f.root, "proc/net/sockstat", sockstat)
}

func TestIOCollectorRates(t *testing.T) {
	f := newIOFixture(t)
	f.write(diskLine("sda", 1000, 8000, 2000, 500, 4000, 1000, 3000, 6000)+diskLine("sda1", 900, 7000, 1900, 400, 3000, 900, 2900, 5900),
		netLine("lo", 5, 0, 0, 5, 0, 0)+netLine("eth0", 1000, 0, 0, 2000, 0, 0)+netLine("veth1a2b", 9, 0, 0, 9, 0, 0),
		snmpTcp(14, 1000, 10), "TCP: inuse 23 orphan 0 tw 2 alloc 59 mem 0\n")

	first := f.c.Sample([]string{"sda"})
	if first.Disks != nil || first.Net != nil || first.TCP.RetransPct != nil {
		t.Fatalf("first sample has rates %+v; there is nothing to compare with yet", first)
	}
	if first.TCP.Established == nil || *first.TCP.Established != 14 || first.TCP.TimeWait == nil || *first.TCP.TimeWait != 2 {
		t.Fatalf("instant TCP counts on the first sample = %+v", first.TCP)
	}

	f.now = f.now.Add(10 * time.Second)
	// 10 sn'de: 100 okuma + 300 yazma; 2000 sektör okundu, 8000 yazıldı; okumaya 400 ms, yazmaya 1200 ms; disk 5 sn meşgul.
	f.write(diskLine("sda", 1100, 10000, 2400, 800, 12000, 2200, 8000, 26000),
		netLine("lo", 99, 0, 0, 99, 0, 0)+netLine("eth0", 1001000, 2, 5, 2252000, 1, 0)+netLine("veth1a2b", 99, 0, 0, 99, 0, 0),
		snmpTcp(20, 1200, 13), "TCP: inuse 23 orphan 0 tw 7 alloc 59 mem 0\n")
	s := f.c.Sample([]string{"sda"})

	if len(s.Disks) != 1 {
		t.Fatalf("disks = %+v; want only the requested whole disk", s.Disks)
	}
	d := s.Disks[0]
	approx(t, d.ReadIOPS, 10, "read iops")
	approx(t, d.WriteIOPS, 30, "write iops")
	approx(t, d.ReadBps, 2000*512/10, "read bytes/s")
	approx(t, d.WriteBps, 8000*512/10, "write bytes/s")
	approx(t, d.AwaitMs, 4, "await ms") // (400+1200) ms / 400 işlem
	approx(t, d.UtilPct, 50, "util %")  // 5000 ms / 10000 ms
	approx(t, d.QueueDepth, 2, "queue") // 20000 ms / 10000 ms

	if len(s.Net) != 1 || s.Net[0].Interface != "eth0" {
		t.Fatalf("net = %+v; lo and veth* must be filtered out", s.Net)
	}
	n := s.Net[0]
	approx(t, n.RxBps, 1000000*8/10, "rx bit/s")
	approx(t, n.TxBps, 2250000*8/10, "tx bit/s")
	if n.RxErrors != 2 || n.RxDrops != 5 || n.TxErrors != 1 || n.TxDrops != 0 {
		t.Errorf("error/drop deltas = %+v", n)
	}
	if s.TCP.RetransPct == nil {
		t.Fatal("retransmit rate missing")
	}
	approx(t, *s.TCP.RetransPct, 1.5, "retrans %") // 3 / 200
	if *s.TCP.Established != 20 || *s.TCP.TimeWait != 7 {
		t.Errorf("tcp = established %d, time_wait %d", *s.TCP.Established, *s.TCP.TimeWait)
	}
}

// Sayaç geri giderse (yeniden açılış, arayüzün yeniden oluşması) o aralık atlanır; aralıkta işlem yoksa gecikme 0.
func TestIOCollectorCounterResetAndIdleDisk(t *testing.T) {
	f := newIOFixture(t)
	f.write(diskLine("nvme0n1", 1000, 8000, 2000, 500, 4000, 1000, 3000, 6000)+diskLine("sdb", 10, 10, 10, 10, 10, 10, 10, 10),
		netLine("eth0", 5000, 0, 0, 5000, 0, 0), snmpTcp(1, 1000, 10), "")
	f.c.Sample([]string{"nvme0n1", "sdb"})

	f.now = f.now.Add(30 * time.Second)
	f.write(diskLine("nvme0n1", 5, 5, 5, 5, 5, 5, 5, 5)+diskLine("sdb", 10, 10, 10, 10, 10, 10, 10, 10),
		netLine("eth0", 10, 0, 0, 10, 0, 0), snmpTcp(1, 50, 1), "")
	s := f.c.Sample([]string{"nvme0n1", "sdb"})
	if len(s.Disks) != 1 || s.Disks[0].Name != "sdb" {
		t.Fatalf("disks = %+v; the reset disk must be skipped, the idle one reported", s.Disks)
	}
	if idle := s.Disks[0]; idle.AwaitMs != 0 || idle.ReadIOPS != 0 || idle.UtilPct != 0 {
		t.Errorf("idle disk = %+v; want zeros", idle)
	}
	if s.Net != nil || s.TCP.RetransPct != nil {
		t.Errorf("reset counters still produced rates: net %+v, retrans %v", s.Net, s.TCP.RetransPct)
	}

	f.now = f.now.Add(30 * time.Second)
	if s := f.c.Sample([]string{"nvme0n1"}); len(s.Disks) != 1 {
		t.Fatalf("after the reset the next interval must be reported again: %+v", s.Disks)
	}
}

func TestIOCollectorWithoutProcFiles(t *testing.T) {
	f := newIOFixture(t)
	f.c.Sample([]string{"sda"})
	f.now = f.now.Add(10 * time.Second)
	if s := f.c.Sample([]string{"sda"}); s.Disks != nil || s.Net != nil || s.TCP != (TCPStats{}) {
		t.Fatalf("no /proc files gave %+v, want everything unknown", s)
	}
}

func TestParseDiskstatsOldFormatAndNetDevQuirks(t *testing.T) {
	// 2.6–4.17 çekirdekleri: 14 sütun (discard/flush yok).
	got := parseDiskstats(strings.NewReader("   8       0 sda 1 0 2 3 4 0 5 6 0 7 8\n   8 0 bad x y\n"))
	if c, ok := got["sda"]; !ok || c.reads != 1 || c.sectorsRead != 2 || c.writes != 4 || c.ioTicks != 7 || c.weighted != 8 || len(got) != 1 {
		t.Fatalf("parseDiskstats = %+v", got)
	}
	// Eski çekirdeklerde ad ile sayı bitişik yazılabilir: "eth0:1234".
	nets, order := parseNetDev(strings.NewReader(netDevHeader + "  eth0:1234 10 0 0 0 0 0 0 5678 10 0 0 0 0 0 0\n"))
	if c, ok := nets["eth0"]; !ok || c.rxBytes != 1234 || c.txBytes != 5678 || len(order) != 1 {
		t.Fatalf("parseNetDev = %+v %v", nets, order)
	}
}

func TestIgnoredInterfaces(t *testing.T) {
	for name, want := range map[string]bool{"lo": true, "docker0": true, "veth9f1": true, "br-0a1b": true, "eth0": false,
		"enp3s0": false, "wg0": false, "tun0": false, "bond0": false} {
		if got := ignoredInterface(name); got != want {
			t.Errorf("ignoredInterface(%q) = %v, want %v", name, got, want)
		}
	}
}
