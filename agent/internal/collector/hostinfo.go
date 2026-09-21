package collector

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Toplama sıklığı: kimlik alanları neredeyse hiç değişmez; yavaş olanlar bir alt süreç ya da Docker
// çağrısı gerektirir; hızlı olanlar yalnızca /proc okur.
const (
	hostStaticTTL = time.Hour
	hostSlowTTL   = 5 * time.Minute
	execTimeout   = 3 * time.Second
)

// HostInfoCollector, makine envanterini ve anlık durumunu toplar. Yalnızca YETKİSİZ okunabilen bilgiler:
// hiçbir alan root gerektirmez ve agent'ın systemd sandbox'ı gevşetilmez (bkz. docs/AGENT.md, "Envanter").
type HostInfoCollector struct {
	root string           // sahte kök (test); "" = gerçek /
	now  func() time.Time // test için
	run  func(ctx context.Context, name string, args ...string) (string, error)
	// dockerVersion, Docker daemon sürümünü verir ("" = yok/erişilemiyor); nil = Docker izlenmiyor.
	dockerVersion func(ctx context.Context) string

	mu       sync.Mutex
	staticAt time.Time
	static   HostInfo
	slowAt   time.Time
	slow     HostInfo
}

// NewHostInfoCollector, gerçek makine için bir toplayıcı kurar. docker nil olabilir.
func NewHostInfoCollector(docker *DockerCollector) *HostInfoCollector {
	c := &HostInfoCollector{now: time.Now, run: runCommand}
	if docker != nil {
		c.dockerVersion = docker.Version
	}
	return c
}

func (c *HostInfoCollector) path(p string) string {
	if c.root == "" {
		return p
	}
	return filepath.Join(c.root, p)
}

func (c *HostInfoCollector) readFile(p string) string {
	b, err := os.ReadFile(c.path(p))
	if err != nil {
		return ""
	}
	return string(b)
}

func (c *HostInfoCollector) exists(p string) bool {
	_, err := os.Stat(c.path(p))
	return err == nil
}

// Collect, envanteri döndürür. Hiçbir okuma başarısız olsa bile panik etmez; okunamayan alan boş kalır.
// Eşzamanlı çağrılabilir (pull modunda istekler paralel gelir).
func (c *HostInfoCollector) Collect(ctx context.Context) *HostInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.staticAt.IsZero() || now.Sub(c.staticAt) >= hostStaticTTL {
		c.static = c.collectStatic()
		c.staticAt = now
	}
	if c.slowAt.IsZero() || now.Sub(c.slowAt) >= hostSlowTTL {
		c.slow = c.collectSlow(ctx)
		c.slowAt = now
	}

	info := c.static // değer kopyası: önbellek alanlarına işaretçi paylaşımı yok (aşağıda derin kopyalanır)
	info.OS = copyPtr(c.static.OS)
	info.Kernel = copyPtr(c.static.Kernel)
	info.Virtualization = copyPtr(c.static.Virtualization)
	info.Machine = copyPtr(c.static.Machine)

	info.RebootRequired = copyPtr(c.slow.RebootRequired)
	info.TimeSynced = copyPtr(c.slow.TimeSynced)
	info.FailedUnits = copyPtr(c.slow.FailedUnits)
	info.DockerVersion = c.slow.DockerVersion

	c.collectFast(&info, now)
	return &info
}

func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// collectStatic, saatte bir okunan kimlik alanlarıdır.
func (c *HostInfoCollector) collectStatic() HostInfo {
	var h HostInfo
	if name, err := os.Hostname(); err == nil {
		h.Hostname = name
	}

	osText := c.readFile("/etc/os-release")
	if osText == "" {
		osText = c.readFile("/usr/lib/os-release")
	}
	if osText != "" {
		if o := parseOSRelease(strings.NewReader(osText)); o != (OSInfo{}) {
			h.OS = &o
		}
	}

	// uname yerine /proc/sys/kernel/osrelease (syscall yok: sandbox uyumu). Mimari, agent binary'sinin
	// çalıştığı mimaridir (64 bit çekirdekte 32 bit userland çok nadirdir).
	kernel := KernelInfo{Release: strings.TrimSpace(c.readFile("/proc/sys/kernel/osrelease")), Arch: unameArch(runtime.GOARCH)}
	if kernel.Release != "" || kernel.Arch != "" {
		h.Kernel = &kernel
	}

	cpuinfo := c.readFile("/proc/cpuinfo")
	h.CPUModel = parseCPUModel(strings.NewReader(cpuinfo))
	dmi := c.readDMI()
	h.Virtualization = c.detectVirtualization(cpuHasHypervisorFlag(strings.NewReader(cpuinfo)), dmi)
	if dmi.vendor != "" || dmi.product != "" {
		h.Machine = &MachineInfo{Vendor: dmi.vendor, Model: dmi.product}
	}

	h.MachineIDHash = machineIDHash(c.readFile("/etc/machine-id"))
	h.Timezone = c.timezone()
	if c.exists("/run/systemd/system") {
		h.Init = "systemd"
	}
	h.SecurityModule = c.securityModule()
	return h
}

// unameArch, Go mimari adını `uname -m` karşılığına çevirir.
func unameArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	case "arm":
		return "armv7l"
	}
	return goarch
}

func (c *HostInfoCollector) timezone() string {
	if tz := strings.TrimSpace(c.readFile("/etc/timezone")); tz != "" {
		return tz
	}
	if target, err := os.Readlink(c.path("/etc/localtime")); err == nil {
		if _, after, ok := strings.Cut(target, "zoneinfo/"); ok {
			return after
		}
	}
	return ""
}

// securityModule: SELinux (zorlayıcı/izin verici), AppArmor ya da none.
func (c *HostInfoCollector) securityModule() string {
	switch strings.TrimSpace(c.readFile("/sys/fs/selinux/enforce")) {
	case "1":
		return "selinux-enforcing"
	case "0":
		return "selinux-permissive"
	}
	if strings.TrimSpace(c.readFile("/sys/module/apparmor/parameters/enabled")) == "Y" {
		return "apparmor"
	}
	return "none"
}

type dmiInfo struct{ vendor, product, boardVendor, biosVendor string }

// dmiNoise, üreticilerin doldurmadığı yer tutucu metinlerdir; bilgi taşımaz.
var dmiNoise = map[string]bool{
	"to be filled by o.e.m.": true, "default string": true, "system manufacturer": true, "system product name": true,
	"not specified": true, "not applicable": true, "none": true, "unknown": true, "o.e.m.": true, "type1productconfigid": true,
}

func (c *HostInfoCollector) dmiField(name string) string {
	v := strings.TrimSpace(c.readFile("/sys/class/dmi/id/" + name))
	if dmiNoise[strings.ToLower(v)] {
		return ""
	}
	return v
}

// readDMI yalnızca dünya-okunabilir DMI alanlarını okur; product_serial ve product_uuid root ister ve
// BİLEREK okunmaz (karar: yetki artışı yok).
func (c *HostInfoCollector) readDMI() dmiInfo {
	return dmiInfo{vendor: c.dmiField("sys_vendor"), product: c.dmiField("product_name"), boardVendor: c.dmiField("board_vendor"), biosVendor: c.dmiField("bios_vendor")}
}

// hypervisorSignatures, DMI metinlerinde aranan (küçük harf) imzalar ve gösterilen ad. Sıra önemlidir.
var hypervisorSignatures = []struct{ needle, vendor string }{
	{"amazon ec2", "Amazon EC2"}, {"google compute engine", "Google Cloud"}, {"openstack", "OpenStack"},
	{"alibaba cloud", "Alibaba Cloud"}, {"digitalocean", "DigitalOcean"}, {"vmware", "VMware"},
	{"virtualbox", "VirtualBox"}, {"innotek", "VirtualBox"}, {"parallels", "Parallels"},
	{"microsoft corporation", "Hyper-V"}, // yalnızca product "virtual machine" ise; aşağıda ayrıca denetlenir
	{"xen", "Xen"}, {"kvm", "KVM"}, {"qemu", "QEMU"}, {"bochs", "Bochs"},
}

func (c *HostInfoCollector) detectVirtualization(hypervisorFlag bool, dmi dmiInfo) *VirtualizationInfo {
	// Konteyner: agent'ın kendisi bir konteyner içinde çalışıyorsa.
	if v := strings.TrimSpace(c.readFile("/run/systemd/container")); v != "" {
		return &VirtualizationInfo{Kind: "container", Vendor: v}
	}
	if c.exists("/run/.containerenv") {
		return &VirtualizationInfo{Kind: "container", Vendor: "podman"}
	}
	if c.exists("/.dockerenv") {
		return &VirtualizationInfo{Kind: "container", Vendor: "docker"}
	}

	haystack := strings.ToLower(strings.Join([]string{dmi.vendor, dmi.product, dmi.boardVendor, dmi.biosVendor}, " | "))
	for _, sig := range hypervisorSignatures {
		if !strings.Contains(haystack, sig.needle) {
			continue
		}
		if sig.vendor == "Hyper-V" && !strings.Contains(haystack, "virtual machine") {
			continue // "Microsoft Corporation" tek başına Surface gibi fiziksel makineleri de kapsar
		}
		return &VirtualizationInfo{Kind: "vm", Vendor: sig.vendor}
	}
	if strings.TrimSpace(c.readFile("/sys/hypervisor/type")) != "" || hypervisorFlag {
		return &VirtualizationInfo{Kind: "vm"}
	}
	if dmi.vendor != "" || dmi.product != "" {
		return &VirtualizationInfo{Kind: "physical"}
	}
	return &VirtualizationInfo{Kind: "unknown"}
}

// collectSlow, bir alt süreç ya da Docker çağrısı gerektiren alanlardır (5 dakikada bir).
func (c *HostInfoCollector) collectSlow(ctx context.Context) HostInfo {
	var h HostInfo

	// "Yeniden başlatma gerekiyor": Debian/Ubuntu ailesinde /run/reboot-required işaret dosyası.
	// Diğer ailelerde yetkisiz güvenilir bir kaynak yoktur: bilinmiyor (nil) bırakılır.
	if o := parseOSRelease(strings.NewReader(c.readFile("/etc/os-release"))); isDebianFamily(o) {
		v := c.exists("/run/reboot-required") || c.exists("/var/run/reboot-required")
		h.RebootRequired = &v
	}

	if c.exists("/run/systemd/system") {
		// NTPSynchronized, çekirdeğin saat durumudur; chrony/ntpd/timesyncd hepsinde çalışır. adjtimex'i
		// agent'ın kendisi ÇAĞIRMAZ: sandbox'ta (ProtectClock) süreci öldürürdü; timedatectl systemd'ye
		// D-Bus ile sorar.
		if out, err := c.run(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value"); err == nil {
			switch strings.TrimSpace(out) {
			case "yes":
				v := true
				h.TimeSynced = &v
			case "no":
				v := false
				h.TimeSynced = &v
			}
		}
		if out, err := c.run(ctx, "systemctl", "--failed", "--plain", "--no-legend", "--no-pager"); err == nil {
			n := countNonEmptyLines(out)
			h.FailedUnits = &n
		}
	}

	if c.dockerVersion != nil {
		h.DockerVersion = c.dockerVersion(ctx)
	}
	return h
}

func isDebianFamily(o OSInfo) bool {
	for _, id := range append(strings.Fields(o.IDLike), o.ID) {
		if id == "debian" || id == "ubuntu" {
			return true
		}
	}
	return false
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// collectFast, her raporda yeniden okunan (yalnızca /proc) anlık durumdur.
func (c *HostInfoCollector) collectFast(h *HostInfo, now time.Time) {
	if up, ok := parseUptime(c.readFile("/proc/uptime")); ok {
		h.UptimeSeconds = int64(up)
		h.BootTime = now.Add(-time.Duration(up * float64(time.Second))).UTC().Format(time.RFC3339)
	}
	if la, ok := parseLoadAvg(c.readFile("/proc/loadavg")); ok {
		h.LoadAvg = la
	}
	if sw, ok := parseSwap(strings.NewReader(c.readFile("/proc/meminfo"))); ok {
		h.Swap = &sw
	}
	h.Addresses = buildAddresses(
		parseFibTrieLocal(strings.NewReader(c.readFile("/proc/net/fib_trie"))),
		parseRoutes(strings.NewReader(c.readFile("/proc/net/route"))),
		parseIfInet6(strings.NewReader(c.readFile("/proc/net/if_inet6"))),
	)
}

// runCommand, dış bir komutu kısa bir zaman aşımıyla ve asgari bir ortamla çalıştırır. Komut yoksa ya da
// hata verirse hata döner; çağıran alanı "bilinmiyor" bırakır.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}
