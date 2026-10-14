package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// DNS yapılandırması (yalnızca bilgi; ad çözme denemesi yapılmaz). Hepsi yetkisiz: systemd-resolved'a busctl ile D-Bus
// üzerinden sorulur (AF_UNIX), /etc/resolv.conf ve /sys/class/net okunur. resolved'ın yönettiği makinelerde
// resolv.conf yalnızca 127.0.0.53'ü gösterir; gerçek sunucular resolved'dan okunur. resolv.conf'u resolved yönetmiyorsa
// (foreign) uygulamalar onu doğrudan kullanır ve o raporlanır.

// DNSInfo, makinenin DNS yapılandırmasıdır.
type DNSInfo struct {
	Resolver         string // resolved | resolv.conf
	Mode             string // resolved'da resolv.conf'un yönetimi: stub | uplink | static (foreign = Resolver "resolv.conf")
	CurrentServer    string // resolved'ın o an sorduğu sunucu
	CurrentInterface string // CurrentServer'ın arayüzü ("" = genel)
	Servers          []DNSServer
	FallbackServers  []string // resolved'ın hiçbir sunucu tanımlı değilken kullandıkları
	SearchDomains    []string
	RoutingDomains   []string // yalnızca yönlendirme alanları (resolved "~alan"): kısa adlara eklenmez
	DNSSEC           string   // yes | no | allow-downgrade (resolved)
	DNSOverTLS       string   // yes | no | opportunistic (resolved)
	Options          []string // resolv.conf "options" (resolved'sız makinede)
}

// DNSServer, tanımlı bir DNS sunucusudur; Interface "" = genel (arayüze bağlı değil).
type DNSServer struct {
	Address   string
	Interface string
}

// dnsListLimit, raporlanan listelerin (sunucu, alan, seçenek) üst sınırıdır.
const dnsListLimit = 16

const resolvedStub = "127.0.0.53"

// DNSCollector, DNS yapılandırmasını toplar (5 dakikada bir, arka planda).
type DNSCollector struct {
	root string // sahte kök (test); "" = gerçek /
	run  func(ctx context.Context, name string, args ...string) (string, error)
}

func NewDNSCollector() *DNSCollector { return &DNSCollector{run: runCommand} }

// Collect, resolv.conf da resolved da yoksa nil, nil ("bilinmiyor": alan gönderilmez) döndürür.
func (c *DNSCollector) Collect(ctx context.Context) (*DNSInfo, error) {
	conf, confErr := c.readResolvConf()
	if info := c.resolved(ctx, conf); info != nil {
		return info, nil
	}
	if confErr != nil {
		return nil, nil
	}
	info := &DNSInfo{Resolver: "resolv.conf", SearchDomains: dnsLimit(conf.search), Options: dnsLimit(conf.options)}
	for _, ns := range dnsLimit(conf.nameservers) {
		info.Servers = append(info.Servers, DNSServer{Address: ns})
	}
	return info, nil
}

type resolvConf struct {
	nameservers, search, options []string
	target                       string // /etc/resolv.conf bağlantıysa hedefi
}

func (c *DNSCollector) readResolvConf() (resolvConf, error) {
	var conf resolvConf
	path := rootPath(c.root, "/etc/resolv.conf")
	if link, err := os.Readlink(path); err == nil {
		conf.target = link // çoğunlukla göreli: ../run/systemd/resolve/stub-resolv.conf
	}
	f, err := os.Open(path)
	if err != nil {
		return conf, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") || strings.HasPrefix(fields[0], ";") {
			continue
		}
		switch fields[0] {
		case "nameserver":
			if ip := net.ParseIP(strings.SplitN(fields[1], "%", 2)[0]); ip != nil {
				conf.nameservers = append(conf.nameservers, fields[1])
			}
		case "search", "domain": // son gelen geçerlidir (resolv.conf(5))
			conf.search = nil
			for _, d := range fields[1:] {
				if d != "." {
					conf.search = append(conf.search, d)
				}
			}
		case "options":
			conf.options = append(conf.options, fields[1:]...)
		}
	}
	return conf, sc.Err()
}

// resolvedProps, resolved Manager'ından okunan özelliklerdir (systemd 246+). Daha eski sürümde "Ex"siz eşdeğerleri okunur.
var (
	resolvedProps       = []string{"DNSEx", "FallbackDNSEx", "Domains", "DNSSEC", "DNSOverTLS", "ResolvConfMode", "CurrentDNSServerEx"}
	resolvedLegacyProps = []string{"DNS", "FallbackDNS", "Domains", "DNSSEC", "DNSOverTLS", "", "CurrentDNSServer"}
)

// resolved, systemd-resolved'ın yapılandırmasını okur. resolved yoksa ya da resolv.conf'u yönetmiyorsa (foreign) nil
// döndürür: o zaman uygulamalar resolv.conf'u doğrudan kullanır.
func (c *DNSCollector) resolved(ctx context.Context, conf resolvConf) *DNSInfo {
	data := c.busctlProps(ctx, "/org/freedesktop/resolve1", "org.freedesktop.resolve1.Manager", resolvedProps)
	if data == nil {
		data = c.busctlProps(ctx, "/org/freedesktop/resolve1", "org.freedesktop.resolve1.Manager", resolvedLegacyProps)
		if data == nil {
			return nil
		}
	}
	var mode string
	json.Unmarshal(data["ResolvConfMode"], &mode)
	if mode == "" { // ResolvConfMode'dan eski systemd: resolv.conf'un hedefinden ya da içeriğinden çıkarılır
		mode = guessResolvConfMode(conf)
	}
	if mode == "foreign" || mode == "missing" {
		return nil
	}

	info := &DNSInfo{Resolver: "resolved", Mode: mode}
	names := c.interfaceNames()
	ifname := func(index int) string {
		if index == 0 {
			return ""
		}
		if n, ok := names[index]; ok {
			return n
		}
		return "#" + strconv.Itoa(index)
	}

	servers := decodeServerList(data["DNSEx"])
	if servers == nil {
		servers = decodeServerList(data["DNS"])
	}
	links := map[int]bool{}
	for _, s := range servers {
		if len(info.Servers) < dnsListLimit {
			info.Servers = append(info.Servers, DNSServer{Address: s.addr, Interface: ifname(s.ifindex)})
		}
		if s.ifindex != 0 {
			links[s.ifindex] = true
		}
	}
	fallback := decodeServerList(data["FallbackDNSEx"])
	if fallback == nil {
		fallback = decodeServerList(data["FallbackDNS"])
	}
	for _, s := range fallback {
		info.FallbackServers = append(info.FallbackServers, s.addr)
	}
	info.FallbackServers = dnsLimit(info.FallbackServers)

	var domains [][]json.RawMessage // a(isb): arayüz, alan, yalnızca yönlendirme
	json.Unmarshal(data["Domains"], &domains)
	for _, d := range domains {
		var name string
		var routeOnly bool
		if len(d) != 3 || json.Unmarshal(d[1], &name) != nil || name == "" || name == "." {
			continue
		}
		json.Unmarshal(d[2], &routeOnly)
		if routeOnly {
			info.RoutingDomains = addUnique(info.RoutingDomains, name)
		} else {
			info.SearchDomains = addUnique(info.SearchDomains, name)
		}
	}
	info.SearchDomains, info.RoutingDomains = dnsLimit(info.SearchDomains), dnsLimit(info.RoutingDomains)
	json.Unmarshal(data["DNSSEC"], &info.DNSSEC)
	json.Unmarshal(data["DNSOverTLS"], &info.DNSOverTLS)

	// Kullanılan sunucu: önce genel olan, yoksa sunucusu olan arayüzlerinki (sıra: arayüz numarası).
	if cur := decodeServer(data["CurrentDNSServerEx"], true); cur.addr != "" {
		info.CurrentServer, info.CurrentInterface = cur.addr, ifname(cur.ifindex)
	} else if cur := decodeServer(data["CurrentDNSServer"], true); cur.addr != "" {
		info.CurrentServer, info.CurrentInterface = cur.addr, ifname(cur.ifindex)
	} else {
		indexes := make([]int, 0, len(links))
		for i := range links {
			indexes = append(indexes, i)
		}
		sort.Ints(indexes)
		for _, i := range indexes {
			if addr := c.linkCurrentServer(ctx, i); addr != "" {
				info.CurrentServer, info.CurrentInterface = addr, ifname(i)
				break
			}
		}
	}
	return info
}

// linkCurrentServer, bir arayüzün o an kullanılan DNS sunucusudur ("" = yok ya da okunamadı).
func (c *DNSCollector) linkCurrentServer(ctx context.Context, ifindex int) string {
	out, err := c.run(ctx, "busctl", "--json=short", "call", "org.freedesktop.resolve1", "/org/freedesktop/resolve1",
		"org.freedesktop.resolve1.Manager", "GetLink", "i", strconv.Itoa(ifindex))
	if err != nil {
		return ""
	}
	var reply struct {
		Data []string `json:"data"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil || len(reply.Data) != 1 {
		return ""
	}
	for _, props := range [][]string{{"CurrentDNSServerEx"}, {"CurrentDNSServer"}} {
		if data := c.busctlProps(ctx, reply.Data[0], "org.freedesktop.resolve1.Link", props); data != nil {
			return decodeServer(data[props[0]], false).addr
		}
	}
	return ""
}

// busctlProps, bir D-Bus nesnesinin özelliklerini tek busctl çağrısıyla okur; boş ad o özelliği atlar. Biri bile yoksa
// busctl hata verir ve nil döner.
func (c *DNSCollector) busctlProps(ctx context.Context, path, iface string, props []string) map[string]json.RawMessage {
	var asked []string
	for _, p := range props {
		if p != "" {
			asked = append(asked, p)
		}
	}
	out, err := c.run(ctx, "busctl", append([]string{"--json=short", "get-property", "org.freedesktop.resolve1", path, iface}, asked...)...)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(asked) {
		return nil
	}
	data := map[string]json.RawMessage{}
	for i, line := range lines {
		var v struct { // her satıra yeni değişken (bkz. timesyncd)
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal([]byte(line), &v) == nil {
			data[asked[i]] = v.Data
		}
	}
	return data
}

type dnsServer struct {
	ifindex int
	addr    string
}

// decodeServerList, a(iiay…) biçimindeki sunucu listesini (arayüz, aile, adres baytları[, port, ad]) çözer.
func decodeServerList(raw json.RawMessage) []dnsServer {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := []dnsServer{}
	for _, item := range list {
		if s := decodeServer(item, true); s.addr != "" {
			out = append(out, s)
		}
	}
	return out
}

// decodeServer, tek sunucuyu çözer: Manager'da (iiay…) (arayüz başta), Link'te (iay…). Adres boşsa (sunucu yok) addr "".
func decodeServer(raw json.RawMessage, withIndex bool) dnsServer {
	var tuple []json.RawMessage
	if json.Unmarshal(raw, &tuple) != nil {
		return dnsServer{}
	}
	var s dnsServer
	if withIndex {
		if len(tuple) < 3 || json.Unmarshal(tuple[0], &s.ifindex) != nil {
			return dnsServer{}
		}
		tuple = tuple[1:]
	}
	if len(tuple) < 2 {
		return dnsServer{}
	}
	var ints []int
	if json.Unmarshal(tuple[1], &ints) != nil || (len(ints) != 4 && len(ints) != 16) {
		return dnsServer{}
	}
	b := make([]byte, len(ints))
	for i, n := range ints {
		b[i] = byte(n)
	}
	s.addr = net.IP(b).String()
	if len(tuple) >= 4 { // Ex: port ve DNS-over-TLS sunucu adı
		var port int
		var name string
		json.Unmarshal(tuple[2], &port)
		json.Unmarshal(tuple[3], &name)
		if port != 0 && port != 53 {
			s.addr = net.JoinHostPort(s.addr, strconv.Itoa(port))
		}
		if name != "" {
			s.addr += "#" + name
		}
	}
	return s
}

// guessResolvConfMode, ResolvConfMode özelliği olmayan resolved'da resolv.conf'un kimin olduğunu çıkarır.
func guessResolvConfMode(conf resolvConf) string {
	switch {
	case strings.HasSuffix(conf.target, "/run/systemd/resolve/stub-resolv.conf"):
		return "stub"
	case strings.HasSuffix(conf.target, "/run/systemd/resolve/resolv.conf"):
		return "uplink"
	case strings.HasSuffix(conf.target, "/lib/systemd/resolv.conf"):
		return "static"
	case len(conf.nameservers) == 1 && conf.nameservers[0] == resolvedStub:
		return "stub"
	}
	return "foreign"
}

// interfaceNames, arayüz numarası → adı eşlemesidir (/sys/class/net/*/ifindex; net.Interfaces netlink ister).
func (c *DNSCollector) interfaceNames() map[int]string {
	out := map[int]string{}
	dir := rootPath(c.root, "/sys/class/net")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "ifindex"))
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			out[n] = e.Name()
		}
	}
	return out
}

func dnsLimit(list []string) []string {
	if len(list) > dnsListLimit {
		return list[:dnsListLimit]
	}
	return list
}

func addUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
