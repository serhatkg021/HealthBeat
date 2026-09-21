package collector

import (
	"net"
	"reflect"
	"strings"
	"testing"
)

const ubuntuOSRelease = `PRETTY_NAME="Ubuntu 24.04.5 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.5 LTS (Noble Numbat)"
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
`

func TestParseOSRelease(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want OSInfo
	}{
		"ubuntu": {ubuntuOSRelease, OSInfo{ID: "ubuntu", Name: "Ubuntu", PrettyName: "Ubuntu 24.04.5 LTS", VersionID: "24.04", IDLike: "debian"}},
		"rocky (multiple ID_LIKE)": {`NAME="Rocky Linux"
VERSION_ID="9.4"
ID="rocky"
ID_LIKE="rhel centos fedora"
PRETTY_NAME="Rocky Linux 9.4 (Blue Onyx)"`, OSInfo{ID: "rocky", Name: "Rocky Linux", PrettyName: "Rocky Linux 9.4 (Blue Onyx)", VersionID: "9.4", IDLike: "rhel centos fedora"}},
		"alpine (no quotes, no PRETTY_NAME)": {"NAME=Alpine Linux\nID=alpine\nVERSION_ID=3.19.1\n", OSInfo{ID: "alpine", Name: "Alpine Linux", VersionID: "3.19.1"}},
		"comments, blanks and junk lines":    {"# comment\n\nnot a pair\nID=arch\n  NAME = \"Arch\"  \n", OSInfo{ID: "arch"}},
		"single quotes and escapes":          {`NAME='Odd "OS"'` + "\n" + `PRETTY_NAME="say \"hi\" \$HOME"`, OSInfo{Name: `Odd "OS"`, PrettyName: `say "hi" $HOME`}},
		"empty":                              {"", OSInfo{}},
	} {
		if got := parseOSRelease(strings.NewReader(tc.in)); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}

func TestParseCPUModel(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"x86":                 {"processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Core(TM) i7-10750H CPU @ 2.60GHz\nflags\t\t: fpu vme\n\nprocessor\t: 1\nmodel name\t: Other\n", "Intel(R) Core(TM) i7-10750H CPU @ 2.60GHz"},
		"raspberry pi (arm)":  {"processor\t: 0\nBogoMIPS\t: 108.00\nCPU part\t: 0xd08\n\nHardware\t: BCM2711\nModel\t\t: Raspberry Pi 4 Model B Rev 1.4\n", "Raspberry Pi 4 Model B Rev 1.4"},
		"powerpc":             {"processor\t: 0\ncpu\t\t: POWER9 (raw), altivec supported\n", "POWER9 (raw), altivec supported"},
		"arm without a name":  {"processor\t: 0\nCPU implementer\t: 0x41\nCPU part\t: 0xd0c\n", ""},
		"empty":               {"", ""},
		"empty value ignored": {"model name\t:\nHardware\t: X\n", "X"},
	} {
		if got := parseCPUModel(strings.NewReader(tc.in)); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestCPUHasHypervisorFlag(t *testing.T) {
	long := "flags\t\t: " + strings.Repeat("fpu ", 20000) // çok uzun bayrak satırı ölçeklenmeli
	for name, tc := range map[string]struct {
		in   string
		want bool
	}{
		"vm":             {"flags\t\t: fpu vme hypervisor lahf_lm\n", true},
		"bare metal":     {"flags\t\t: fpu vme lahf_lm\n", false},
		"substring only": {"flags\t\t: fpu nothypervisorx\n", false},
		"very long line": {long + " hypervisor\n", true},
		"no flags line":  {"model name : x\n", false},
	} {
		if got := cpuHasHypervisorFlag(strings.NewReader(tc.in)); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestParseUptimeAndLoadAvg(t *testing.T) {
	if v, ok := parseUptime("26877.32 200000.11\n"); !ok || v != 26877.32 {
		t.Errorf("uptime = %v %v", v, ok)
	}
	for _, bad := range []string{"", "abc 1", "-5 1"} {
		if _, ok := parseUptime(bad); ok {
			t.Errorf("uptime %q accepted", bad)
		}
	}
	if v, ok := parseLoadAvg("0.84 1.09 1.18 6/2483 369537\n"); !ok || !reflect.DeepEqual(v, []float64{0.84, 1.09, 1.18}) {
		t.Errorf("loadavg = %v %v", v, ok)
	}
	for _, bad := range []string{"", "1 2", "a b c", "1 -2 3"} {
		if _, ok := parseLoadAvg(bad); ok {
			t.Errorf("loadavg %q accepted", bad)
		}
	}
}

func TestParseSwap(t *testing.T) {
	sw, ok := parseSwap(strings.NewReader("MemTotal: 16 kB\nSwapTotal:       4194300 kB\nSwapFree:        4194300 kB\n"))
	if !ok || sw != (SwapInfo{TotalMB: 4095, UsedMB: 0}) {
		t.Errorf("swap = %+v %v", sw, ok)
	}
	sw, _ = parseSwap(strings.NewReader("SwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n"))
	if sw != (SwapInfo{TotalMB: 2048, UsedMB: 1024}) {
		t.Errorf("swap in use = %+v", sw)
	}
	if _, ok := parseSwap(strings.NewReader("SwapTotal: 0 kB\nSwapFree: 0 kB\n")); ok {
		t.Error("a machine without swap must report no swap info")
	}
	if _, ok := parseSwap(strings.NewReader("MemTotal: 1 kB\n")); ok {
		t.Error("missing swap lines accepted")
	}
	// SwapFree > SwapTotal (bozuk okuma): kullanılan negatif olmamalı
	if sw, ok := parseSwap(strings.NewReader("SwapTotal: 1024 kB\nSwapFree: 4096 kB\n")); !ok || sw.UsedMB != 0 {
		t.Errorf("inconsistent swap = %+v %v", sw, ok)
	}
}

func TestMachineIDHash(t *testing.T) {
	id := "6bcbfc62a1b44b0f9b6f2f3a4c5d6e7f"
	h := machineIDHash(id + "\n")
	if len(h) != 32 || strings.Trim(h, "0123456789abcdef") != "" {
		t.Fatalf("hash = %q, want 32 lowercase hex", h)
	}
	if h != machineIDHash(id) {
		t.Error("hash is not stable across trailing whitespace")
	}
	if strings.Contains(h, id[:8]) || h == id {
		t.Error("the raw machine-id leaks into the hash")
	}
	if h == machineIDHash("6bcbfc62a1b44b0f9b6f2f3a4c5d6e70") {
		t.Error("different machine-ids collide")
	}
	if machineIDHash("  \n") != "" {
		t.Error("an empty machine-id must produce no hash")
	}
}

// Bu makinede alınan gerçek /proc/net/fib_trie (Local bölümü), route ve if_inet6 çıktıları.
const realFibTrie = `Main:
  +-- 0.0.0.0/0 3 0 5
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 172.16.0.0/14 3 0 4
        |-- 172.17.0.0
           /16 link UNICAST
     +-- 192.168.1.0/24 2 0 2
        |-- 192.168.1.0
           /24 link UNICAST
Local:
  +-- 0.0.0.0/0 3 0 4
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 127.0.0.0/8 2 0 2
        +-- 127.0.0.0/31 1 0 0
           |-- 127.0.0.0
              /8 host LOCAL
           |-- 127.0.0.1
              /32 host LOCAL
        |-- 127.255.255.255
           /32 link BROADCAST
     +-- 172.16.0.0/14 3 0 4
        +-- 172.17.0.0/31 1 0 0
           |-- 172.17.0.0
              /16 link UNICAST
           |-- 172.17.0.1
              /32 host LOCAL
        |-- 172.17.255.255
           /32 link BROADCAST
        +-- 172.18.0.0/31 1 0 0
           |-- 172.18.0.0
              /16 link UNICAST
           |-- 172.18.0.1
              /32 host LOCAL
        |-- 172.18.255.255
           /32 link BROADCAST
     +-- 192.168.1.0/24 2 0 1
        |-- 192.168.1.0
           /24 link UNICAST
        |-- 192.168.1.106
           /32 host LOCAL
        |-- 192.168.1.255
           /32 link BROADCAST
`

const realRoute = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
	"enp4s0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
	"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
	"br-c38abb1eec21\t000012AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
	"enp4s0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"

const realIfInet6 = `fe8000000000000044b42dfffef71d10 05 40 20 80 br-c38abb1eec21
fe80000000000000d00371fffe10d715 04 40 20 80  docker0
fe80000000000000b225aafffe3ed25a 02 40 20 80   enp4s0
00000000000000000000000000000001 01 80 10 80       lo
20010db8000000000000000000000106 02 40 00 80   enp4s0
fd000000000000000000000000000007 06 40 00 80  vethabc
`

func TestParseFibTrieLocalOnlyTakesLocalHostAddresses(t *testing.T) {
	got := parseFibTrieLocal(strings.NewReader(realFibTrie))
	var s []string
	for _, ip := range got {
		s = append(s, ip.String())
	}
	// "/8 host LOCAL" (ağın kendisi), BROADCAST ve UNICAST satırları adres DEĞİLDİR; Main: bölümü sayılmaz.
	want := []string{"127.0.0.1", "172.17.0.1", "172.18.0.1", "192.168.1.106"}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("local addresses = %v, want %v", s, want)
	}
}

func TestParseRoutes(t *testing.T) {
	rs := parseRoutes(strings.NewReader(realRoute))
	if len(rs) != 3 { // varsayılan rota (maske 0) atlanır
		t.Fatalf("routes = %+v, want 3 (default route excluded)", rs)
	}
	last := rs[2]
	if last.iface != "enp4s0" || last.dest != 0xC0A80100 || last.mask != 0xFFFFFF00 {
		t.Errorf("enp4s0 route = %+v, want 192.168.1.0/24 in host order", last)
	}
	// RTF_UP olmayan rota atlanır; bozuk satırlar atlanır
	if got := parseRoutes(strings.NewReader("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\n" + "eth0\t0001A8C0\t0\t0000\t0\t0\t0\t00FFFFFF\n" + "junk\n" + "eth1\tZZZZ\t0\t0001\t0\t0\t0\t00FFFFFF\n")); len(got) != 0 {
		t.Errorf("down/malformed routes kept: %+v", got)
	}
}

func TestLeToHost(t *testing.T) {
	if leToHost(0x0001A8C0) != 0xC0A80100 || leToHost(0x00FFFFFF) != 0xFFFFFF00 || leToHost(0x0000FFFF) != 0xFFFF0000 {
		t.Errorf("leToHost wrong: %x %x %x", leToHost(0x0001A8C0), leToHost(0x00FFFFFF), leToHost(0x0000FFFF))
	}
}

func TestParseIfInet6SkipsLoopbackAndLinkLocal(t *testing.T) {
	got := parseIfInet6(strings.NewReader(realIfInet6))
	want := []NetworkAddress{{Interface: "enp4s0", Address: "2001:db8::106/64"}, {Interface: "vethabc", Address: "fd00::7/64"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("if_inet6 = %+v, want %+v", got, want)
	}
}

// Gerçek makine: docker köprüleri ve veth'ler elenir, yalnızca gerçek arayüz kalır; ön ek rotadan gelir.
func TestBuildAddressesOnTheRealMachineLayout(t *testing.T) {
	got := buildAddresses(
		parseFibTrieLocal(strings.NewReader(realFibTrie)),
		parseRoutes(strings.NewReader(realRoute)),
		parseIfInet6(strings.NewReader(realIfInet6)),
	)
	want := []NetworkAddress{
		{Interface: "enp4s0", Address: "192.168.1.106/24"},
		{Interface: "enp4s0", Address: "2001:db8::106/64"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("addresses = %+v\nwant       %+v", got, want)
	}
}

func TestBuildAddressesEdgeCases(t *testing.T) {
	ip := func(s string) net.IP { return net.ParseIP(s) }
	routes := []route4{{iface: "eth0", dest: 0x0A000000, mask: 0xFF000000}, {iface: "eth1", dest: 0x0A010000, mask: 0xFFFF0000}}
	got := buildAddresses([]net.IP{
		ip("10.1.2.3"),    // iki rota kapsar: en uzun ön ek (eth1 /16) kazanır
		ip("10.9.9.9"),    // yalnızca eth0 /8
		ip("169.254.1.1"), // link-local: atlanır
		ip("127.0.0.1"),   // loopback: atlanır
		ip("203.0.113.5"), // ağa özgü rota yok: arayüzsüz, öneksiz
	}, routes, nil)
	want := []NetworkAddress{
		{Address: "203.0.113.5"},
		{Interface: "eth0", Address: "10.9.9.9/8"},
		{Interface: "eth1", Address: "10.1.2.3/16"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edge cases = %+v\nwant       %+v", got, want)
	}

	// Sınır: en çok maxReportedAddresses adres
	var many []net.IP
	for i := 1; i <= maxReportedAddresses+10; i++ {
		many = append(many, net.IPv4(10, 0, 0, byte(i)))
	}
	if g := buildAddresses(many, []route4{{iface: "eth0", dest: 0x0A000000, mask: 0xFF000000}}, nil); len(g) != maxReportedAddresses {
		t.Errorf("%d addresses, cap is %d", len(g), maxReportedAddresses)
	}
}

func TestIsVirtualInterface(t *testing.T) {
	for _, n := range []string{"lo", "docker0", "br-c38abb1eec21", "veth123", "virbr0", "cni0", "flannel.1", "cali1a2b", "kube-ipvs0", "lxcbr0", "podman0", "vmnet8", "vboxnet0"} {
		if !isVirtualInterface(n) {
			t.Errorf("%q should be virtual", n)
		}
	}
	for _, n := range []string{"eth0", "enp4s0", "ens3", "wlo1", "bond0", "wg0", "tailscale0", "tun0", "eno1", "loop", "lower0"} {
		if isVirtualInterface(n) {
			t.Errorf("%q should be a real interface", n)
		}
	}
}
