package collector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHost, gerçek dosya sisteminin ilgili kısmını geçici bir dizinde kurar.
type fakeHost struct {
	t    *testing.T
	root string
}

func newFakeHost(t *testing.T) *fakeHost { return &fakeHost{t: t, root: t.TempDir()} }

func (f *fakeHost) write(rel, content string) *fakeHost {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func (f *fakeHost) mkdir(rel string) *fakeHost {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.root, rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func (f *fakeHost) symlink(rel, target string) *fakeHost {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.Symlink(target, p); err != nil {
		f.t.Fatal(err)
	}
	return f
}

// collector, komut çağrılarını sayan ve yanıtlarını yöneten sahte bir çalıştırıcıyla toplayıcı kurar.
func (f *fakeHost) collector(run func(name string, args ...string) (string, error)) (*HostInfoCollector, *atomic.Int32) {
	var calls atomic.Int32
	c := &HostInfoCollector{
		root: f.root, now: time.Now,
		run: func(_ context.Context, name string, args ...string) (string, error) {
			calls.Add(1)
			if run == nil {
				return "", errors.New("no such command")
			}
			return run(name, args...)
		},
	}
	return c, &calls
}

func (f *fakeHost) ubuntu() *fakeHost {
	return f.write("etc/os-release", ubuntuOSRelease).
		write("proc/sys/kernel/osrelease", "6.8.0-45-generic\n").
		write("proc/cpuinfo", "processor : 0\nmodel name : AMD EPYC 7763\nflags : fpu vme\n").
		write("proc/uptime", "3600.00 7000.00\n").write("proc/loadavg", "0.10 0.20 0.30 1/100 5\n").
		write("proc/meminfo", "SwapTotal: 2097152 kB\nSwapFree: 2097152 kB\n").
		write("etc/machine-id", "0123456789abcdef0123456789abcdef\n").write("etc/timezone", "Europe/Istanbul\n")
}

func TestCollectFromAFakeUbuntuHost(t *testing.T) {
	f := newFakeHost(t).ubuntu().
		mkdir("run/systemd/system").
		write("sys/class/dmi/id/sys_vendor", "Dell Inc.\n").write("sys/class/dmi/id/product_name", "PowerEdge R740\n").
		write("sys/module/apparmor/parameters/enabled", "Y\n").
		write("proc/net/fib_trie", realFibTrie).write("proc/net/route", realRoute).write("proc/net/if_inet6", realIfInet6)
	c, _ := f.collector(func(name string, args ...string) (string, error) {
		switch name {
		case "timedatectl":
			return "yes\n", nil
		case "systemctl":
			return "  foo.service loaded failed failed Foo\n  bar.service loaded failed failed Bar\n", nil
		}
		return "", errors.New("unexpected " + name)
	})
	c.dockerVersion = func(context.Context) string { return "27.1.1" }

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	h := c.Collect(context.Background())

	if h.OS == nil || h.OS.PrettyName != "Ubuntu 24.04.5 LTS" || h.OS.ID != "ubuntu" {
		t.Errorf("os = %+v", h.OS)
	}
	if h.Kernel == nil || h.Kernel.Release != "6.8.0-45-generic" || h.Kernel.Arch != unameArch(runtime.GOARCH) {
		t.Errorf("kernel = %+v", h.Kernel)
	}
	if h.CPUModel != "AMD EPYC 7763" || h.Timezone != "Europe/Istanbul" || h.Init != "systemd" || h.SecurityModule != "apparmor" {
		t.Errorf("cpu/tz/init/sec = %q %q %q %q", h.CPUModel, h.Timezone, h.Init, h.SecurityModule)
	}
	if h.Virtualization == nil || h.Virtualization.Kind != "physical" || h.Machine == nil || h.Machine.Vendor != "Dell Inc." || h.Machine.Model != "PowerEdge R740" {
		t.Errorf("virt/machine = %+v %+v", h.Virtualization, h.Machine)
	}
	if h.UptimeSeconds != 3600 || h.BootTime != "2026-09-20T11:00:00Z" {
		t.Errorf("uptime/boot = %d %q", h.UptimeSeconds, h.BootTime)
	}
	if len(h.LoadAvg) != 3 || h.LoadAvg[1] != 0.20 || h.Swap == nil || h.Swap.TotalMB != 2048 {
		t.Errorf("load/swap = %v %+v", h.LoadAvg, h.Swap)
	}
	if len(h.Addresses) != 2 || h.Addresses[0].Address != "192.168.1.106/24" {
		t.Errorf("addresses = %+v", h.Addresses)
	}
	if h.RebootRequired == nil || *h.RebootRequired {
		t.Errorf("reboot_required = %v, want false on Ubuntu without the marker", h.RebootRequired)
	}
	if h.TimeSynced == nil || !*h.TimeSynced || h.FailedUnits == nil || *h.FailedUnits != 2 || h.DockerVersion != "27.1.1" {
		t.Errorf("slow fields = synced %v failed %v docker %q", h.TimeSynced, h.FailedUnits, h.DockerVersion)
	}
	if len(h.MachineIDHash) != 32 || strings.Contains(h.MachineIDHash, "0123456789abcdef") {
		t.Errorf("machine_id_hash = %q", h.MachineIDHash)
	}
}

// Yetki gerektiren hiçbir şey okunmaz ve gönderilmez: DMI seri no/UUID dosyaları OKUNMAZ.
func TestCollectNeverReadsRootOnlyDMIFiles(t *testing.T) {
	f := newFakeHost(t).ubuntu().
		write("sys/class/dmi/id/sys_vendor", "V").write("sys/class/dmi/id/product_name", "M").
		write("sys/class/dmi/id/product_serial", "SECRET-SERIAL-123").write("sys/class/dmi/id/product_uuid", "SECRET-UUID-456").
		write("sys/class/dmi/id/board_serial", "SECRET-BOARD-789")
	c, _ := f.collector(nil)
	b, _ := json.Marshal(c.Collect(context.Background()))
	for _, secret := range []string{"SECRET-SERIAL", "SECRET-UUID", "SECRET-BOARD", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("payload leaks %q: %s", secret, b)
		}
	}
	for _, key := range []string{`"serial"`, `"serial_number"`, `"uuid"`, `"product_uuid"`, `"mac"`, `"mac_address"`} {
		if strings.Contains(strings.ToLower(string(b)), key) {
			t.Errorf("payload has a %s field: %s", key, b)
		}
	}
}

func TestDetectVirtualization(t *testing.T) {
	type dmi = map[string]string
	for name, tc := range map[string]struct {
		files          dmi
		extra          func(*fakeHost)
		hypervisorFlag bool
		wantKind, want string
	}{
		"physical laptop":            {dmi{"sys_vendor": "MONSTER", "product_name": "ABRA A5"}, nil, false, "physical", ""},
		"kvm/qemu":                   {dmi{"sys_vendor": "QEMU", "product_name": "Standard PC (Q35 + ICH9, 2009)"}, nil, true, "vm", "QEMU"},
		"kvm":                        {dmi{"sys_vendor": "Red Hat", "product_name": "KVM"}, nil, true, "vm", "KVM"},
		"vmware":                     {dmi{"sys_vendor": "VMware, Inc.", "product_name": "VMware Virtual Platform"}, nil, true, "vm", "VMware"},
		"virtualbox":                 {dmi{"sys_vendor": "innotek GmbH", "product_name": "VirtualBox"}, nil, true, "vm", "VirtualBox"},
		"hyper-v":                    {dmi{"sys_vendor": "Microsoft Corporation", "product_name": "Virtual Machine"}, nil, true, "vm", "Hyper-V"},
		"surface is NOT hyper-v":     {dmi{"sys_vendor": "Microsoft Corporation", "product_name": "Surface Laptop 4"}, nil, false, "physical", ""},
		"amazon ec2":                 {dmi{"sys_vendor": "Amazon EC2", "product_name": "t3.micro"}, nil, true, "vm", "Amazon EC2"},
		"google cloud":               {dmi{"sys_vendor": "Google", "product_name": "Google Compute Engine"}, nil, true, "vm", "Google Cloud"},
		"unknown hypervisor":         {dmi{"sys_vendor": "Acme", "product_name": "Hyperion"}, nil, true, "vm", ""},
		"xen via /sys/hypervisor":    {nil, func(f *fakeHost) { f.write("sys/hypervisor/type", "xen\n") }, false, "vm", ""},
		"docker container":           {dmi{"sys_vendor": "Dell Inc."}, func(f *fakeHost) { f.write(".dockerenv", "") }, false, "container", "docker"},
		"podman container":           {nil, func(f *fakeHost) { f.write("run/.containerenv", "") }, false, "container", "podman"},
		"systemd-nspawn/lxc":         {nil, func(f *fakeHost) { f.write("run/systemd/container", "lxc\n") }, false, "container", "lxc"},
		"no dmi, no flags (arm sbc)": {nil, nil, false, "unknown", ""},
		"placeholder dmi ignored":    {dmi{"sys_vendor": "To Be Filled By O.E.M.", "product_name": "Default string"}, nil, false, "unknown", ""},
	} {
		f := newFakeHost(t)
		for k, v := range tc.files {
			f.write("sys/class/dmi/id/"+k, v+"\n")
		}
		if tc.extra != nil {
			tc.extra(f)
		}
		c, _ := f.collector(nil)
		got := c.detectVirtualization(tc.hypervisorFlag, c.readDMI())
		if got.Kind != tc.wantKind || got.Vendor != tc.want {
			t.Errorf("%s: got %+v, want kind %q vendor %q", name, got, tc.wantKind, tc.want)
		}
	}
}

func TestSecurityModuleAndTimezone(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*fakeHost)
		want  string
	}{
		"selinux enforcing":  {func(f *fakeHost) { f.write("sys/fs/selinux/enforce", "1") }, "selinux-enforcing"},
		"selinux permissive": {func(f *fakeHost) { f.write("sys/fs/selinux/enforce", "0") }, "selinux-permissive"},
		"apparmor":           {func(f *fakeHost) { f.write("sys/module/apparmor/parameters/enabled", "Y\n") }, "apparmor"},
		"apparmor disabled":  {func(f *fakeHost) { f.write("sys/module/apparmor/parameters/enabled", "N\n") }, "none"},
		"nothing":            {func(f *fakeHost) {}, "none"},
	} {
		f := newFakeHost(t)
		tc.setup(f)
		c, _ := f.collector(nil)
		if got := c.securityModule(); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}

	// saat dilimi: /etc/timezone (Debian), aksi halde /etc/localtime sembolik bağı (RHEL)
	f := newFakeHost(t).symlink("etc/localtime", "../usr/share/zoneinfo/America/New_York")
	c, _ := f.collector(nil)
	if got := c.timezone(); got != "America/New_York" {
		t.Errorf("timezone via symlink = %q", got)
	}
	f = newFakeHost(t).write("etc/timezone", "Asia/Tokyo\n").symlink("etc/localtime", "../usr/share/zoneinfo/Europe/Paris")
	c, _ = f.collector(nil)
	if got := c.timezone(); got != "Asia/Tokyo" {
		t.Errorf("/etc/timezone must win, got %q", got)
	}
	c, _ = newFakeHost(t).collector(nil)
	if got := c.timezone(); got != "" {
		t.Errorf("no timezone info = %q, want empty", got)
	}
}

// "Yeniden başlatma gerekiyor" yalnızca Debian ailesinde bilinir; diğerlerinde "bilinmiyor" (nil), false DEĞİL.
func TestRebootRequiredIsOnlyKnownOnDebianFamily(t *testing.T) {
	debianWithMarker := newFakeHost(t).ubuntu().write("run/reboot-required", "*** System restart required ***\n")
	c, _ := debianWithMarker.collector(nil)
	if h := c.Collect(context.Background()); h.RebootRequired == nil || !*h.RebootRequired {
		t.Errorf("Ubuntu with the marker: %v, want true", h.RebootRequired)
	}
	for name, osr := range map[string]string{
		"rocky":  "ID=rocky\nID_LIKE=\"rhel centos fedora\"\nNAME=Rocky\n",
		"alpine": "ID=alpine\nNAME=Alpine\n",
		"amzn":   "ID=amzn\nID_LIKE=\"centos rhel fedora\"\n",
	} {
		f := newFakeHost(t).write("etc/os-release", osr).write("run/reboot-required", "x") // dosya olsa bile bu ailede anlamsız
		c, _ := f.collector(nil)
		if h := c.Collect(context.Background()); h.RebootRequired != nil {
			t.Errorf("%s: reboot_required = %v, want unknown (nil)", name, *h.RebootRequired)
		}
	}
	debianLike := newFakeHost(t).write("etc/os-release", "ID=pop\nID_LIKE=\"ubuntu debian\"\n")
	c, _ = debianLike.collector(nil)
	if h := c.Collect(context.Background()); h.RebootRequired == nil {
		t.Error("an ID_LIKE=ubuntu derivative must be treated as Debian family")
	}
}

func TestSlowFieldsAreUnknownWhenCommandsFailOrSystemdIsAbsent(t *testing.T) {
	// systemd var ama komutlar yok/hatalı: nil ("bilinmiyor"), 0/false DEĞİL
	f := newFakeHost(t).ubuntu().mkdir("run/systemd/system")
	c, _ := f.collector(nil)
	h := c.Collect(context.Background())
	if h.TimeSynced != nil || h.FailedUnits != nil {
		t.Errorf("failed commands became values: synced %v failed %v", h.TimeSynced, h.FailedUnits)
	}

	// timedatectl anlamsız çıktı verirse de bilinmiyor
	c, _ = f.collector(func(name string, args ...string) (string, error) { return "maybe\n", nil })
	if h := c.Collect(context.Background()); h.TimeSynced != nil {
		t.Errorf("garbage timedatectl output became %v", *h.TimeSynced)
	}

	// "no" -> false (bilinmiyor değil); hiç başarısız servis -> 0
	c, _ = f.collector(func(name string, args ...string) (string, error) {
		if name == "timedatectl" {
			return "no\n", nil
		}
		return "\n", nil
	})
	h = c.Collect(context.Background())
	if h.TimeSynced == nil || *h.TimeSynced || h.FailedUnits == nil || *h.FailedUnits != 0 {
		t.Errorf("no/0 lost: synced %v failed %v", h.TimeSynced, h.FailedUnits)
	}

	// systemd yoksa (konteyner, chroot, OpenRC) komutlar HİÇ çalıştırılmaz
	c, calls := newFakeHost(t).ubuntu().collector(func(string, ...string) (string, error) { return "yes", nil })
	h = c.Collect(context.Background())
	if calls.Load() != 0 || h.TimeSynced != nil || h.FailedUnits != nil || h.Init != "" {
		t.Errorf("without systemd: %d commands ran, synced %v, init %q", calls.Load(), h.TimeSynced, h.Init)
	}
}

// Yavaş alanlar (alt süreç/Docker) 5 dakikada bir, kimlik alanları saatte bir; hızlı alanlar her seferinde.
func TestCollectCachesSlowAndStaticFields(t *testing.T) {
	f := newFakeHost(t).ubuntu().mkdir("run/systemd/system")
	c, calls := f.collector(func(name string, args ...string) (string, error) {
		if name == "timedatectl" {
			return "yes\n", nil
		}
		return "\n", nil
	})
	var dockerCalls atomic.Int32
	c.dockerVersion = func(context.Context) string { dockerCalls.Add(1); return "1.0" }
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	c.Collect(context.Background())
	first := calls.Load()
	if first != 2 || dockerCalls.Load() != 1 {
		t.Fatalf("first collect ran %d commands and %d docker calls, want 2 and 1", first, dockerCalls.Load())
	}

	// hızlı alan her çağrıda yeniden okunur
	f.write("proc/uptime", "9999.00 1.0\n")
	now = now.Add(time.Minute)
	if h := c.Collect(context.Background()); h.UptimeSeconds != 9999 {
		t.Errorf("uptime not re-read: %d", h.UptimeSeconds)
	}
	if calls.Load() != first || dockerCalls.Load() != 1 {
		t.Errorf("a re-collect within 5 minutes ran commands again (%d, docker %d)", calls.Load(), dockerCalls.Load())
	}

	// 5 dakika sonra yavaş alanlar yenilenir; kimlik alanları henüz değil
	f.write("etc/timezone", "Asia/Tokyo\n")
	now = now.Add(5 * time.Minute)
	h := c.Collect(context.Background())
	if calls.Load() != 2*first || dockerCalls.Load() != 2 {
		t.Errorf("slow fields not refreshed after 5 minutes: %d commands, %d docker", calls.Load(), dockerCalls.Load())
	}
	if h.Timezone != "Europe/Istanbul" {
		t.Errorf("static field refreshed too early: %q", h.Timezone)
	}

	// 1 saat sonra kimlik alanları da yenilenir
	now = now.Add(time.Hour)
	if h := c.Collect(context.Background()); h.Timezone != "Asia/Tokyo" {
		t.Errorf("static field not refreshed after an hour: %q", h.Timezone)
	}
}

// Dönen değer önbelleğin iç durumunu paylaşmamalı: çağıran değiştirse sonraki çağrı etkilenmemeli.
func TestCollectReturnsIndependentCopies(t *testing.T) {
	f := newFakeHost(t).ubuntu().mkdir("run/systemd/system")
	c, _ := f.collector(func(string, ...string) (string, error) { return "yes\n", nil })
	a := c.Collect(context.Background())
	a.OS.PrettyName = "TAMPERED"
	a.Kernel.Release = "TAMPERED"
	*a.TimeSynced = false
	b := c.Collect(context.Background())
	if b.OS.PrettyName == "TAMPERED" || b.Kernel.Release == "TAMPERED" || !*b.TimeSynced {
		t.Errorf("caller mutations leaked into the cache: %+v", b)
	}
}

func TestCollectIsSafeUnderConcurrency(t *testing.T) {
	f := newFakeHost(t).ubuntu().mkdir("run/systemd/system")
	c, _ := f.collector(func(string, ...string) (string, error) { return "yes\n", nil })
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if h := c.Collect(context.Background()); h == nil || h.OS == nil {
					t.Error("nil result")
				}
			}
		}()
	}
	wg.Wait()
}

// Boş bir kök (hiçbir dosya yok): panik yok, alanlar boş.
func TestCollectOnAnEmptyHostDoesNotPanic(t *testing.T) {
	c, _ := newFakeHost(t).collector(nil)
	h := c.Collect(context.Background())
	if h.OS != nil || h.CPUModel != "" || h.UptimeSeconds != 0 || h.LoadAvg != nil || h.Swap != nil || h.Addresses != nil {
		t.Errorf("an empty host produced data: %+v", h)
	}
	if h.Virtualization == nil || h.Virtualization.Kind != "unknown" {
		t.Errorf("virtualization on an empty host = %+v, want unknown", h.Virtualization)
	}
}

// Gerçek makinede: sağlıklı, tutarlı ve sızıntısız bir envanter üretir.
func TestCollectOnTheRealHost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := NewHostInfoCollector(nil).Collect(ctx)
	if h.Kernel == nil || h.Kernel.Release == "" || h.Kernel.Arch != unameArch(runtime.GOARCH) {
		t.Errorf("kernel = %+v", h.Kernel)
	}
	if h.UptimeSeconds <= 0 || len(h.LoadAvg) != 3 {
		t.Errorf("uptime %d load %v", h.UptimeSeconds, h.LoadAvg)
	}
	if h.Virtualization == nil || h.Virtualization.Kind == "" {
		t.Errorf("virtualization = %+v", h.Virtualization)
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := os.ReadFile("/etc/machine-id"); err == nil && len(strings.TrimSpace(string(id))) >= 16 {
		if strings.Contains(string(b), strings.TrimSpace(string(id))) {
			t.Error("the raw /etc/machine-id is in the payload")
		}
	}
	if len(b) > 8*1024 {
		t.Errorf("host_info is %d bytes; it is sent on every report and should stay small", len(b))
	}
}
