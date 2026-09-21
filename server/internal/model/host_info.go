package model

import (
	"math"
	"net"
	"strconv"
	"time"
)

// HostInfo, agent'ın bildirdiği makine envanteri ve anlık durumudur (protokol 3). Yalnızca bilgi
// içindir: alert üretmez; panelde "Sistem bilgisi" olarak gösterilir. Her alan isteğe bağlıdır
// ("bilinmiyor" = alan yok); agent yalnızca YETKİSİZ okunabilen bilgileri gönderir (bkz. docs/AGENT.md).
// hosts tablosunda son bilinen değer olarak tutulur: yeni rapor tümüyle değiştirir, eksik/boş rapor
// mevcut kaydı silmez.
type HostInfo struct {
	Hostname       string              `json:"hostname,omitempty"`
	OS             *OSInfo             `json:"os,omitempty"`
	Kernel         *KernelInfo         `json:"kernel,omitempty"`
	CPUModel       string              `json:"cpu_model,omitempty"`
	Virtualization *VirtualizationInfo `json:"virtualization,omitempty"`
	Machine        *MachineInfo        `json:"machine,omitempty"`
	// MachineIDHash, /etc/machine-id'nin uygulamaya özgü özetidir (ham kimlik asla gönderilmez, bkz.
	// systemd machine-id belgeleri): aynı makinenin iki kez eklenmesini yakalamaya yarar.
	MachineIDHash  string           `json:"machine_id_hash,omitempty"`
	Timezone       string           `json:"timezone,omitempty"`
	Init           string           `json:"init,omitempty"`            // "systemd" ya da boş
	SecurityModule string           `json:"security_module,omitempty"` // apparmor | selinux-enforcing | selinux-permissive | none
	BootTime       string           `json:"boot_time,omitempty"`       // RFC 3339, UTC
	UptimeSeconds  int64            `json:"uptime_seconds,omitempty"`
	LoadAvg        []float64        `json:"load_avg,omitempty"` // 1, 5, 15 dakika
	Swap           *SwapInfo        `json:"swap,omitempty"`
	Addresses      []NetworkAddress `json:"addresses,omitempty"`
	// RebootRequired/TimeSynced/FailedUnits işaretçidir: nil = bilinmiyor (false/0 ile karışmasın).
	RebootRequired *bool  `json:"reboot_required,omitempty"`
	TimeSynced     *bool  `json:"time_synced,omitempty"`
	FailedUnits    *int   `json:"failed_units,omitempty"`
	DockerVersion  string `json:"docker_version,omitempty"`
}

type OSInfo struct {
	ID         string `json:"id,omitempty"`          // ubuntu, rhel, rocky, amzn, debian
	Name       string `json:"name,omitempty"`        // Ubuntu
	PrettyName string `json:"pretty_name,omitempty"` // Ubuntu 24.04.5 LTS
	VersionID  string `json:"version_id,omitempty"`  // 24.04
	IDLike     string `json:"id_like,omitempty"`     // debian
}

type KernelInfo struct {
	Release string `json:"release,omitempty"` // uname -r
	Arch    string `json:"arch,omitempty"`    // uname -m: x86_64, aarch64
}

// VirtualizationInfo: Kind physical | vm | container | unknown.
type VirtualizationInfo struct {
	Kind   string `json:"kind,omitempty"`
	Vendor string `json:"vendor,omitempty"` // KVM, VMware, Hyper-V, Amazon EC2, docker ...
}

// MachineInfo, DMI'dan gelen üretici ve model'dir (fiziksel makinede donanım, VM'de hipervizör ürünü).
type MachineInfo struct {
	Vendor string `json:"vendor,omitempty"`
	Model  string `json:"model,omitempty"`
}

type SwapInfo struct {
	TotalMB int64 `json:"total_mb"`
	UsedMB  int64 `json:"used_mb"`
}

// NetworkAddress, bir arayüzün adresidir (CIDR gösterimi: 192.168.1.10/24).
type NetworkAddress struct {
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address"`
}

// Envanter için kabul sınırları: liste JSONB olarak saklanır ve her panel isteğinde döner.
const (
	maxHostnameBytes  = 253
	maxHostTextBytes  = 128
	maxAddressEntries = 32
	maxIfaceNameBytes = 32
	maxUptimeSeconds  = int64(100 * 365 * 24 * 3600) // 100 yıl: bunun üstü saat/okuma hatasıdır
	maxSwapMB         = int64(1) << 40               // 1 PiB
	maxFailedUnits    = 100000
	machineHashLen    = 32
)

var (
	virtKinds       = map[string]bool{"physical": true, "vm": true, "container": true, "unknown": true}
	securityModules = map[string]bool{"apparmor": true, "selinux-enforcing": true, "selinux-permissive": true, "none": true}
)

func nonNegFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 }

// sanitizeHostInfo, agent'ın bildirdiği envanteri saklanabilir hâle getirir: metinler temizlenip
// kırpılır (denetim karakterleri JSONB'yi bozardı), bilinmeyen sabit listeli değerler ("unknown"/atılır),
// sayılar makul aralığa çekilir, adresler doğrulanıp kanonik hâle getirilir. Bozuk bir envanter tüm
// metrik alımını reddettirmemeli; bu yüzden hata döndürmez, geçersiz parçayı atar. Hiçbir alan
// kalmadıysa nil döner ("bilinmiyor": mevcut kayıt korunur).
func sanitizeHostInfo(in *HostInfo) *HostInfo {
	if in == nil {
		return nil
	}
	out := &HostInfo{
		Hostname:      cleanText(in.Hostname, maxHostnameBytes),
		CPUModel:      cleanText(in.CPUModel, maxHostTextBytes),
		Timezone:      cleanText(in.Timezone, 64),
		DockerVersion: cleanText(in.DockerVersion, 64),
	}

	if in.OS != nil {
		o := &OSInfo{
			ID: cleanText(in.OS.ID, 64), Name: cleanText(in.OS.Name, maxHostTextBytes),
			PrettyName: cleanText(in.OS.PrettyName, maxHostTextBytes), VersionID: cleanText(in.OS.VersionID, 64),
			IDLike: cleanText(in.OS.IDLike, maxHostTextBytes),
		}
		if *o != (OSInfo{}) {
			out.OS = o
		}
	}
	if in.Kernel != nil {
		k := &KernelInfo{Release: cleanText(in.Kernel.Release, maxHostTextBytes), Arch: cleanText(in.Kernel.Arch, 32)}
		if *k != (KernelInfo{}) {
			out.Kernel = k
		}
	}
	if in.Virtualization != nil {
		kind := cleanText(in.Virtualization.Kind, 16)
		if !virtKinds[kind] {
			kind = "unknown"
		}
		v := &VirtualizationInfo{Kind: kind, Vendor: cleanText(in.Virtualization.Vendor, 64)}
		if in.Virtualization.Kind != "" || v.Vendor != "" {
			out.Virtualization = v
		}
	}
	if in.Machine != nil {
		m := &MachineInfo{Vendor: cleanText(in.Machine.Vendor, maxHostTextBytes), Model: cleanText(in.Machine.Model, maxHostTextBytes)}
		if *m != (MachineInfo{}) {
			out.Machine = m
		}
	}
	if h := cleanText(in.MachineIDHash, machineHashLen+1); len(h) == machineHashLen && isLowerHex(h) {
		out.MachineIDHash = h
	}
	if in.Init == "systemd" {
		out.Init = "systemd"
	}
	if securityModules[in.SecurityModule] {
		out.SecurityModule = in.SecurityModule
	}
	if t, err := time.Parse(time.RFC3339, in.BootTime); err == nil && t.Year() >= 1970 && t.Before(time.Now().Add(24*time.Hour)) {
		out.BootTime = t.UTC().Format(time.RFC3339)
	}
	if in.UptimeSeconds > 0 && in.UptimeSeconds <= maxUptimeSeconds {
		out.UptimeSeconds = in.UptimeSeconds
	}
	if len(in.LoadAvg) == 3 && nonNegFinite(in.LoadAvg[0]) && nonNegFinite(in.LoadAvg[1]) && nonNegFinite(in.LoadAvg[2]) &&
		in.LoadAvg[0] < 1e6 && in.LoadAvg[1] < 1e6 && in.LoadAvg[2] < 1e6 {
		out.LoadAvg = []float64{in.LoadAvg[0], in.LoadAvg[1], in.LoadAvg[2]}
	}
	if s := in.Swap; s != nil && s.TotalMB >= 0 && s.TotalMB <= maxSwapMB && s.UsedMB >= 0 && s.UsedMB <= s.TotalMB {
		out.Swap = &SwapInfo{TotalMB: s.TotalMB, UsedMB: s.UsedMB}
	}
	for _, a := range in.Addresses {
		if len(out.Addresses) == maxAddressEntries {
			break
		}
		if addr, ok := canonicalAddress(a.Address); ok {
			out.Addresses = append(out.Addresses, NetworkAddress{Interface: cleanText(a.Interface, maxIfaceNameBytes), Address: addr})
		}
	}
	if in.RebootRequired != nil {
		v := *in.RebootRequired
		out.RebootRequired = &v
	}
	if in.TimeSynced != nil {
		v := *in.TimeSynced
		out.TimeSynced = &v
	}
	if in.FailedUnits != nil && *in.FailedUnits >= 0 && *in.FailedUnits <= maxFailedUnits {
		v := *in.FailedUnits
		out.FailedUnits = &v
	}

	if isEmptyHostInfo(out) {
		return nil
	}
	return out
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// canonicalAddress, "192.168.1.10/24" ya da "192.168.1.10" biçimini doğrulayıp kanonik hâle getirir
// (CIDR verilmişse ön ek korunur). Geçersiz metin ok=false döndürür.
func canonicalAddress(s string) (string, bool) {
	if ip, ipnet, err := net.ParseCIDR(s); err == nil {
		ones, _ := ipnet.Mask.Size()
		return ip.String() + "/" + strconv.Itoa(ones), true
	}
	if ip := net.ParseIP(s); ip != nil {
		return ip.String(), true
	}
	return "", false
}

func isEmptyHostInfo(h *HostInfo) bool {
	return h.Hostname == "" && h.OS == nil && h.Kernel == nil && h.CPUModel == "" && h.Virtualization == nil &&
		h.Machine == nil && h.MachineIDHash == "" && h.Timezone == "" && h.Init == "" && h.SecurityModule == "" &&
		h.BootTime == "" && h.UptimeSeconds == 0 && h.LoadAvg == nil && h.Swap == nil && h.Addresses == nil &&
		h.RebootRequired == nil && h.TimeSynced == nil && h.FailedUnits == nil && h.DockerVersion == ""
}
