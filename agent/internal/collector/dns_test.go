package collector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Gerçek çıktılar: bu makinedeki systemd 255 resolved'ı (stub modu, enp4s0'da tek sunucu, yalnızca yönlendirme alanı).
const (
	busctlResolvedStub = `{"type":"a(iiayqs)","data":[[2,2,[192,168,1,1],0,""]]}
{"type":"a(iiayqs)","data":[]}
{"type":"a(isb)","data":[[2,"lab.local",true]]}
{"type":"s","data":"no"}
{"type":"s","data":"no"}
{"type":"s","data":"stub"}
{"type":"(iiayqs)","data":[0,0,[],0,""]}
`
	busctlGetLink2   = `{"type":"o","data":["/org/freedesktop/resolve1/link/_32"]}`
	busctlLinkServer = `{"type":"(iayqs)","data":[2,[192,168,1,1],0,""]}`
	stubResolvConf   = "# This is /run/systemd/resolve/stub-resolv.conf managed by man:systemd-resolved(8).\nnameserver 127.0.0.53\noptions edns0 trust-ad\nsearch .\n"
)

// dnsCollector, sahte kökte (resolv.conf, /sys/class/net) ve sahte busctl'le bir toplayıcı kurar; outputs anahtarı
// busctl argümanlarının önekidir ("get-property … Manager DNSEx" gibi). Eşleşmeyen çağrı hata döner (özellik yok).
func dnsCollector(t *testing.T, resolvConf string, outputs map[string]string) *DNSCollector {
	t.Helper()
	root := t.TempDir()
	if resolvConf != "" {
		writeProc(t, root, "etc/resolv.conf", resolvConf)
	}
	writeProc(t, root, "sys/class/net/lo/ifindex", "1\n")
	writeProc(t, root, "sys/class/net/enp4s0/ifindex", "2\n")
	writeProc(t, root, "sys/class/net/wg0/ifindex", "5\n")
	return &DNSCollector{root: root, run: func(_ context.Context, name string, args ...string) (string, error) {
		key := strings.Join(args, " ")
		for prefix, out := range outputs {
			if strings.HasPrefix(key, prefix) {
				return out, nil
			}
		}
		return "", errors.New("busctl: unknown property")
	}}
}

const managerEx = "--json=short get-property org.freedesktop.resolve1 /org/freedesktop/resolve1 org.freedesktop.resolve1.Manager DNSEx"

func TestDNSFromResolvedStub(t *testing.T) {
	c := dnsCollector(t, stubResolvConf, map[string]string{
		managerEx: busctlResolvedStub,
		"--json=short call org.freedesktop.resolve1 /org/freedesktop/resolve1 org.freedesktop.resolve1.Manager GetLink i 2":                      busctlGetLink2,
		"--json=short get-property org.freedesktop.resolve1 /org/freedesktop/resolve1/link/_32 org.freedesktop.resolve1.Link CurrentDNSServerEx": busctlLinkServer,
	})
	info, err := c.Collect(context.Background())
	if err != nil || info == nil {
		t.Fatalf("Collect = %v, %v", info, err)
	}
	want := &DNSInfo{Resolver: "resolved", Mode: "stub", CurrentServer: "192.168.1.1", CurrentInterface: "enp4s0",
		Servers: []DNSServer{{Address: "192.168.1.1", Interface: "enp4s0"}}, FallbackServers: nil,
		RoutingDomains: []string{"lab.local"}, DNSSEC: "no", DNSOverTLS: "no"}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("info = %+v\nwant   %+v", info, want)
	}
}

func TestDNSFromResolvedGlobalServersAndDoT(t *testing.T) {
	// Genel sunucu DNS-over-TLS'le (853, ad), arayüzde IPv6 sunucu, yedek sunucular, arama alanı, bilinmeyen arayüz.
	c := dnsCollector(t, stubResolvConf, map[string]string{managerEx: `{"type":"a(iiayqs)","data":[[0,2,[10,0,0,2],853,"dns.sirket.local"],[5,10,[253,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1],0,""],[9,2,[10,9,9,9],0,""]]}
{"type":"a(iiayqs)","data":[[0,2,[1,1,1,1],0,""],[0,2,[8,8,8,8],0,""]]}
{"type":"a(isb)","data":[[0,"sirket.local",false],[5,"vpn.sirket.local",true],[2,"sirket.local",false]]}
{"type":"s","data":"allow-downgrade"}
{"type":"s","data":"opportunistic"}
{"type":"s","data":"uplink"}
{"type":"(iiayqs)","data":[0,2,[10,0,0,2],853,"dns.sirket.local"]}
`})
	info, _ := c.Collect(context.Background())
	if info == nil || info.Mode != "uplink" || info.CurrentServer != "10.0.0.2:853#dns.sirket.local" || info.CurrentInterface != "" {
		t.Fatalf("info = %+v", info)
	}
	wantServers := []DNSServer{{"10.0.0.2:853#dns.sirket.local", ""}, {"fd00::1", "wg0"}, {"10.9.9.9", "#9"}}
	if !reflect.DeepEqual(info.Servers, wantServers) {
		t.Errorf("servers = %+v", info.Servers)
	}
	if !reflect.DeepEqual(info.FallbackServers, []string{"1.1.1.1", "8.8.8.8"}) ||
		!reflect.DeepEqual(info.SearchDomains, []string{"sirket.local"}) || !reflect.DeepEqual(info.RoutingDomains, []string{"vpn.sirket.local"}) ||
		info.DNSSEC != "allow-downgrade" || info.DNSOverTLS != "opportunistic" {
		t.Errorf("info = %+v", info)
	}
}

func TestDNSResolvedNotManagingResolvConf(t *testing.T) {
	// resolved çalışıyor ama resolv.conf'u NetworkManager yazıyor (foreign): uygulamalar resolv.conf'u kullanır.
	out := strings.Replace(busctlResolvedStub, `"data":"stub"`, `"data":"foreign"`, 1)
	c := dnsCollector(t, "nameserver 10.0.0.2\nsearch sirket.local\n", map[string]string{managerEx: out})
	info, _ := c.Collect(context.Background())
	if info == nil || info.Resolver != "resolv.conf" || len(info.Servers) != 1 || info.Servers[0].Address != "10.0.0.2" {
		t.Fatalf("info = %+v", info)
	}
}

func TestDNSFromResolvConfOnly(t *testing.T) {
	conf := `# yorum
; yorum
domain eski.local
search sirket.local lab.sirket.local
nameserver 10.0.0.2
nameserver fe80::1%eth0
nameserver yanlis
options timeout:2 attempts:3
options rotate
`
	info, err := dnsCollector(t, conf, nil).Collect(context.Background())
	if err != nil || info == nil {
		t.Fatalf("Collect = %v, %v", info, err)
	}
	want := &DNSInfo{Resolver: "resolv.conf", Servers: []DNSServer{{Address: "10.0.0.2"}, {Address: "fe80::1%eth0"}},
		SearchDomains: []string{"sirket.local", "lab.sirket.local"}, Options: []string{"timeout:2", "attempts:3", "rotate"}}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("info = %+v\nwant   %+v", info, want)
	}
}

func TestDNSWithoutServers(t *testing.T) {
	// Bilerek DNS'siz makine: bilgi gönderilir, sunucu listesi boştur.
	info, _ := dnsCollector(t, "# boş\n", nil).Collect(context.Background())
	if info == nil || info.Resolver != "resolv.conf" || len(info.Servers) != 0 {
		t.Fatalf("info = %+v", info)
	}
}

func TestDNSUnknown(t *testing.T) {
	if info, err := dnsCollector(t, "", nil).Collect(context.Background()); info != nil || err != nil {
		t.Fatalf("Collect = %+v, %v; want nil, nil", info, err)
	}
}

func TestDNSFromOldResolved(t *testing.T) {
	// systemd 245: "Ex" özellikleri ve ResolvConfMode yok; mod resolv.conf'un içeriğinden çıkarılır.
	legacy := "--json=short get-property org.freedesktop.resolve1 /org/freedesktop/resolve1 org.freedesktop.resolve1.Manager DNS "
	c := dnsCollector(t, "nameserver 127.0.0.53\n", map[string]string{legacy: `{"type":"a(iiay)","data":[[2,2,[10,0,0,2]]]}
{"type":"a(iiay)","data":[]}
{"type":"a(isb)","data":[]}
{"type":"s","data":"no"}
{"type":"s","data":"no"}
{"type":"(iiay)","data":[2,2,[10,0,0,2]]}
`})
	info, _ := c.Collect(context.Background())
	if info == nil || info.Resolver != "resolved" || info.Mode != "stub" || info.CurrentServer != "10.0.0.2" || info.CurrentInterface != "enp4s0" {
		t.Fatalf("info = %+v", info)
	}
}

func TestGuessResolvConfMode(t *testing.T) {
	for _, tc := range []struct {
		conf resolvConf
		want string
	}{
		{resolvConf{target: "../run/systemd/resolve/stub-resolv.conf"}, "stub"},
		{resolvConf{target: "/run/systemd/resolve/resolv.conf"}, "uplink"},
		{resolvConf{target: "/usr/lib/systemd/resolv.conf"}, "static"},
		{resolvConf{nameservers: []string{"127.0.0.53"}}, "stub"},
		{resolvConf{target: "/run/NetworkManager/resolv.conf", nameservers: []string{"10.0.0.2"}}, "foreign"},
	} {
		if got := guessResolvConfMode(tc.conf); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.conf, got, tc.want)
		}
	}
}

func TestDNSResolvConfSymlinkAndLimit(t *testing.T) {
	c := dnsCollector(t, "", nil)
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("nameserver 10.0.0.1\n")
	}
	writeProc(t, c.root, "run/stub-resolv.conf", b.String())
	if err := os.MkdirAll(filepath.Join(c.root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../run/stub-resolv.conf", filepath.Join(c.root, "etc/resolv.conf")); err != nil {
		t.Fatal(err)
	}
	info, _ := c.Collect(context.Background())
	if info == nil || len(info.Servers) != dnsListLimit {
		t.Fatalf("info = %+v", info)
	}
}
