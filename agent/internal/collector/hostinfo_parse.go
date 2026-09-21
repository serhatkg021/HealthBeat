package collector

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Bu dosyadaki her şey saf ayrıştırıcıdır: girdiyi io.Reader/string olarak alır, dosya sistemine
// dokunmaz; gerçek dosyaları hostinfo.go okur. Agent'ın systemd sandbox'ı syscall'ları ve ağ ailelerini
// kısıtlar (bkz. healthbeat-agent.service): bu yüzden adjtimex/uname/netlink yerine yalnızca /proc,
// /sys ve /etc okunur.

// HostInfo, agent'ın bildirdiği makine envanteri ve anlık durumudur (protokol 3). Yalnızca YETKİSİZ
// okunabilen bilgileri içerir; alan adları ve anlamları server'daki model.HostInfo ile aynıdır.
type HostInfo struct {
	Hostname       string              `json:"hostname,omitempty"`
	OS             *OSInfo             `json:"os,omitempty"`
	Kernel         *KernelInfo         `json:"kernel,omitempty"`
	CPUModel       string              `json:"cpu_model,omitempty"`
	Virtualization *VirtualizationInfo `json:"virtualization,omitempty"`
	Machine        *MachineInfo        `json:"machine,omitempty"`
	MachineIDHash  string              `json:"machine_id_hash,omitempty"`
	Timezone       string              `json:"timezone,omitempty"`
	Init           string              `json:"init,omitempty"`
	SecurityModule string              `json:"security_module,omitempty"`
	BootTime       string              `json:"boot_time,omitempty"`
	UptimeSeconds  int64               `json:"uptime_seconds,omitempty"`
	LoadAvg        []float64           `json:"load_avg,omitempty"`
	Swap           *SwapInfo           `json:"swap,omitempty"`
	Addresses      []NetworkAddress    `json:"addresses,omitempty"`
	RebootRequired *bool               `json:"reboot_required,omitempty"`
	TimeSynced     *bool               `json:"time_synced,omitempty"`
	FailedUnits    *int                `json:"failed_units,omitempty"`
	DockerVersion  string              `json:"docker_version,omitempty"`
}

type OSInfo struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	PrettyName string `json:"pretty_name,omitempty"`
	VersionID  string `json:"version_id,omitempty"`
	IDLike     string `json:"id_like,omitempty"`
}

type KernelInfo struct {
	Release string `json:"release,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

type VirtualizationInfo struct {
	Kind   string `json:"kind,omitempty"` // physical | vm | container | unknown
	Vendor string `json:"vendor,omitempty"`
}

type MachineInfo struct {
	Vendor string `json:"vendor,omitempty"`
	Model  string `json:"model,omitempty"`
}

type SwapInfo struct {
	TotalMB int64 `json:"total_mb"`
	UsedMB  int64 `json:"used_mb"`
}

type NetworkAddress struct {
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address"` // CIDR: 192.168.1.10/24
}

// parseOSRelease, /etc/os-release (KEY=DEĞER, tırnaklı olabilir) içeriğinden işletim sistemi bilgisini çıkarır.
func parseOSRelease(r io.Reader) OSInfo {
	vals := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[k] = unquoteOSRelease(v)
	}
	return OSInfo{ID: vals["ID"], Name: vals["NAME"], PrettyName: vals["PRETTY_NAME"], VersionID: vals["VERSION_ID"], IDLike: vals["ID_LIKE"]}
}

func unquoteOSRelease(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	// os-release kabuk kaçışlarına izin verir: \" \\ \$ \`
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`").Replace(v)
}

// parseCPUModel, /proc/cpuinfo'dan CPU modelini bulur. x86'da "model name"; bazı ARM/RISC-V/PowerPC
// çekirdeklerinde farklı anahtarlar kullanılır. Bulunamazsa "".
func parseCPUModel(r io.Reader) string {
	priority := []string{"model name", "Model Name", "Model", "Hardware", "cpu model", "cpu", "Processor"}
	found := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if v != "" {
			if _, seen := found[k]; !seen {
				found[k] = v
			}
		}
	}
	for _, k := range priority {
		if v := found[k]; v != "" {
			return v
		}
	}
	return ""
}

// cpuHasHypervisorFlag, /proc/cpuinfo'da "hypervisor" bayrağı varsa true (sanal makine göstergesi).
func cpuHasHypervisorFlag(r io.Reader) bool {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024) // bayrak satırları uzundur
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "flags") {
			continue
		}
		_, v, _ := strings.Cut(line, ":")
		for _, f := range strings.Fields(v) {
			if f == "hypervisor" {
				return true
			}
		}
	}
	return false
}

// parseUptime, /proc/uptime'ın ilk alanını (saniye) döndürür.
func parseUptime(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) < 1 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// parseLoadAvg, /proc/loadavg'ın ilk üç alanını (1, 5, 15 dakika) döndürür.
func parseLoadAvg(s string) ([]float64, bool) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return nil, false
	}
	out := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil || v < 0 {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// parseSwap, /proc/meminfo'dan SwapTotal/SwapFree'yi MB olarak döndürür. Swap yoksa ok=false.
func parseSwap(r io.Reader) (SwapInfo, bool) {
	var total, free uint64
	haveTotal := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "SwapTotal:"):
			total, haveTotal = parseMeminfoKB(line), true
		case strings.HasPrefix(line, "SwapFree:"):
			free = parseMeminfoKB(line)
		}
	}
	if !haveTotal || total == 0 {
		return SwapInfo{}, false
	}
	if free > total {
		free = total
	}
	return SwapInfo{TotalMB: int64(total / 1024), UsedMB: int64((total - free) / 1024)}, true
}

// machineIDHash, /etc/machine-id'nin UYGULAMAYA ÖZGÜ özetidir (HMAC-SHA256, anahtar = machine-id, mesaj =
// "healthbeat"; systemd'nin sd_id128_get_machine_app_specific'i gibi): ham machine-id gizli sayılır ve
// asla gönderilmez, ama aynı makinenin iki kez eklenmesini yakalamaya yeter. 32 hex karakter.
func machineIDHash(machineID string) string {
	id := strings.TrimSpace(machineID)
	if id == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(id))
	mac.Write([]byte("healthbeat"))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// --- ağ adresleri -------------------------------------------------------------------------------
// net.Interfaces netlink (AF_NETLINK) ister; agent'ın sandbox'ı bu adres ailesini kapatır. Adresler
// bunun yerine /proc/net/fib_trie (IPv4 yerel adresler), /proc/net/route (IPv4 arayüz + ön ek) ve
// /proc/net/if_inet6 (IPv6) dosyalarından kurulur.

type route4 struct {
	iface string
	dest  uint32 // ağ düzeni (host byte order)
	mask  uint32
}

// parseFibTrieLocal, /proc/net/fib_trie'nin "Local:" bölümünden yerel IPv4 adreslerini ("/32 host LOCAL") çıkarır.
func parseFibTrieLocal(r io.Reader) []net.IP {
	var out []net.IP
	inLocal := false
	var last net.IP
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "Local:":
			inLocal = true
			continue
		case line == "Main:":
			inLocal = false
			continue
		}
		if !inLocal {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "|-- "); ok {
			last = net.ParseIP(strings.TrimSpace(rest)).To4()
			continue
		}
		if strings.HasPrefix(line, "/32 host LOCAL") && last != nil && !seen[last.String()] {
			seen[last.String()] = true
			out = append(out, last)
		}
	}
	return out
}

// parseRoutes, /proc/net/route'u ayrıştırır (Destination ve Mask sekizli düzende hex, little-endian).
// Varsayılan rota (maske 0) dışarıda bırakılır: bir adresi arayüze bağlamak için ağa özgü rota gerekir.
func parseRoutes(r io.Reader) []route4 {
	var out []route4
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // başlık satırı
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 8 {
			continue
		}
		dest, err1 := strconv.ParseUint(f[1], 16, 32)
		flags, err2 := strconv.ParseUint(f[3], 16, 32)
		mask, err3 := strconv.ParseUint(f[7], 16, 32)
		if err1 != nil || err2 != nil || err3 != nil || flags&1 == 0 /* RTF_UP */ || mask == 0 {
			continue
		}
		out = append(out, route4{iface: f[0], dest: leToHost(uint32(dest)), mask: leToHost(uint32(mask))})
	}
	return out
}

func leToHost(v uint32) uint32 { return v<<24 | (v&0xff00)<<8 | (v>>8)&0xff00 | v>>24 }

func ipToUint32(ip net.IP) (uint32, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3]), true
}

func popcount(m uint32) int {
	n := 0
	for ; m != 0; m &= m - 1 {
		n++
	}
	return n
}

// parseIfInet6, /proc/net/if_inet6'dan (arayüz adı, adres/ön ek) çıkarır. Loopback ve link-local atlanır.
func parseIfInet6(r io.Reader) []NetworkAddress {
	var out []NetworkAddress
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || len(f[0]) != 32 {
			continue
		}
		raw, err := hex.DecodeString(f[0])
		if err != nil {
			continue
		}
		prefix, err1 := strconv.ParseUint(f[2], 16, 8)
		scope, err2 := strconv.ParseUint(f[3], 16, 8)
		if err1 != nil || err2 != nil || prefix > 128 {
			continue
		}
		if scope == 0x10 /* host (loopback) */ || scope == 0x20 /* link-local */ {
			continue
		}
		out = append(out, NetworkAddress{Interface: f[5], Address: fmt.Sprintf("%s/%d", net.IP(raw).String(), prefix)})
	}
	return out
}

// virtualInterfacePrefixes, konteyner/sanal ağ arayüzleridir: bunların adresleri makinenin kimliği
// değildir ve envanteri şişirir (her container bir veth üretir).
var virtualInterfacePrefixes = []string{"lo", "docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "cilium", "weave", "kube", "lxc", "lxd", "podman", "vmnet", "vboxnet", "nerdctl"}

func isVirtualInterface(name string) bool {
	for _, p := range virtualInterfacePrefixes {
		if name == p || (p != "lo" && strings.HasPrefix(name, p)) {
			return true
		}
	}
	return false
}

const maxReportedAddresses = 32

// buildAddresses, yerel IPv4 adreslerini rotalarla arayüze/ön eke bağlar, IPv6'yı ekler, sanal
// arayüzleri ve loopback/link-local'i eler, sıralı ve sınırlı döndürür.
func buildAddresses(local []net.IP, routes []route4, v6 []NetworkAddress) []NetworkAddress {
	var out []NetworkAddress
	for _, ip := range local {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		v, ok := ipToUint32(ip)
		if !ok {
			continue
		}
		var best *route4
		for i := range routes {
			r := &routes[i]
			if v&r.mask == r.dest && (best == nil || popcount(r.mask) > popcount(best.mask)) {
				best = r
			}
		}
		if best == nil { // ağa özgü rota yok (ör. noktadan noktaya): arayüzsüz, öneksiz
			out = append(out, NetworkAddress{Address: ip.String()})
			continue
		}
		if isVirtualInterface(best.iface) {
			continue
		}
		out = append(out, NetworkAddress{Interface: best.iface, Address: fmt.Sprintf("%s/%d", ip.String(), popcount(best.mask))})
	}
	for _, a := range v6 {
		if !isVirtualInterface(a.Interface) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Interface != out[j].Interface {
			return out[i].Interface < out[j].Interface
		}
		return out[i].Address < out[j].Address
	})
	if len(out) > maxReportedAddresses {
		out = out[:maxReportedAddresses]
	}
	return out
}
