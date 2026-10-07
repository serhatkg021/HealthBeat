package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// Protokol 4'ün alanları: sistem sağlığı ve performans (bkz. docs/COMPATIBILITY.md §2). Hepsi isteğe bağlıdır ve
// agent'ın gönderdiği biçimi birebir yansıtır; alan yoksa "bilinmiyor". Oranlar ve "saniyede" değerleri agent'ta iki
// örnek arasındaki farktır; kümülatif sayaçlar (oom_kills, restarts) olduğu gibi gelir, farkı server hesaplar.
//
// Değerler ölçüldüğü gibi (yuvarlanmadan) saklanır ve API'de öyle döner; hassasiyet bir gösterim kararıdır ve panelde
// verilir. Böylece hassasiyet değiştirmek ne agent'ı ne saklanan veriyi etkiler.
//
// Kabul kuralı: bu bölümlerden biri bozuksa (yanlış tip, aralık dışı sayı) yalnızca o bölüm, liste girdisi ya da alan
// atılır; rapor reddedilmez. Bilgi alanı yüzünden CPU/RAM/disk kaybolmamalı.

// CPUDetail, CPU zamanının dağılımıdır.
type CPUDetail struct {
	IOWaitPct    *float64 `json:"iowait_pct,omitempty"`
	StealPct     *float64 `json:"steal_pct,omitempty"`
	ProcsBlocked *int     `json:"procs_blocked,omitempty"` // G/Ç'de takılı (D durumunda) süreç sayısı
}

// MemoryDetail, RAM yüzdesinin ötesindeki bellek durumudur.
type MemoryDetail struct {
	AvailableMB *int64   `json:"available_mb,omitempty"`
	CachedMB    *int64   `json:"cached_mb,omitempty"`
	SwapInPerS  *float64 `json:"swap_in_per_s,omitempty"`  // swap'tan okunan sayfa/sn
	SwapOutPerS *float64 `json:"swap_out_per_s,omitempty"` // swap'a yazılan sayfa/sn
	OOMKills    *uint64  `json:"oom_kills,omitempty"`      // açılıştan beri bellek yetmediği için öldürülen süreç
}

// Pressure, PSI'dır: süreçlerin kaynak beklerken geçirdiği zamanın yüzdesi, 10 ve 60 sn ortalaması.
type Pressure struct {
	CPU    *PressureStall `json:"cpu,omitempty"`
	Memory *PressureStall `json:"memory,omitempty"`
	IO     *PressureStall `json:"io,omitempty"`
}

type PressureStall struct {
	Some10 float64  `json:"some10"`
	Some60 float64  `json:"some60"`
	Full10 *float64 `json:"full10,omitempty"`
	Full60 *float64 `json:"full60,omitempty"`
}

// DiskIO, bir fiziksel diskin G/Ç'sidir.
type DiskIO struct {
	Name       string  `json:"name"`
	ReadIOPS   float64 `json:"read_iops"`
	WriteIOPS  float64 `json:"write_iops"`
	ReadBps    float64 `json:"read_bps"` // bayt/sn
	WriteBps   float64 `json:"write_bps"`
	UtilPct    float64 `json:"util_pct"`
	AwaitMs    float64 `json:"await_ms"`    // bir işlemin ortalama süresi
	QueueDepth float64 `json:"queue_depth"` // ortalama bekleyen işlem
}

// NetIO, bir ağ arayüzünün trafiğidir; hata ve düşen paket sayıları aralıktaki farktır.
type NetIO struct {
	Interface string  `json:"interface"`
	RxBps     float64 `json:"rx_bps"` // bit/sn
	TxBps     float64 `json:"tx_bps"`
	RxErrors  uint64  `json:"rx_errors"`
	TxErrors  uint64  `json:"tx_errors"`
	RxDrops   uint64  `json:"rx_drops"`
	TxDrops   uint64  `json:"tx_drops"`
}

// TCP, makine geneli TCP durumudur.
type TCP struct {
	RetransPct  *float64 `json:"retrans_pct,omitempty"`
	Established *int     `json:"established,omitempty"`
	TimeWait    *int     `json:"time_wait,omitempty"`
}

// Temperature, bir sıcaklık sensörüdür; Max/Crit donanımın bildirdiği sınırlardır.
type Temperature struct {
	Sensor  string   `json:"sensor"`
	Kind    string   `json:"kind,omitempty"` // cpu | disk | other
	Celsius float64  `json:"celsius"`
	Max     *float64 `json:"max,omitempty"`
	Crit    *float64 `json:"crit,omitempty"`
}

// RAID, bir yazılım RAID dizisidir.
type RAID struct {
	Name    string   `json:"name"`
	Level   string   `json:"level,omitempty"`
	State   string   `json:"state"` // clean | degraded | recovering | resyncing | failed …
	Devices int      `json:"devices"`
	Active  int      `json:"active"`
	SyncPct *float64 `json:"sync_pct,omitempty"`
}

// Capacity, çekirdeğin sınırlı tablolarının doluluğudur.
type Capacity struct {
	FileHandles    *int64 `json:"file_handles,omitempty"`
	FileHandlesMax *int64 `json:"file_handles_max,omitempty"`
	Conntrack      *int64 `json:"conntrack,omitempty"`
	ConntrackMax   *int64 `json:"conntrack_max,omitempty"`
	Tasks          *int64 `json:"tasks,omitempty"` // süreç + iş parçacığı: pid_max sınırı bunlara uygulanır
	PIDMax         *int64 `json:"pid_max,omitempty"`
}

// Processes, süreç özetidir; yalnızca süreç adı gelir (komut satırı ve kullanıcı gelmez).
type Processes struct {
	Total  int            `json:"total"`
	Zombie int            `json:"zombie"`
	TopCPU []ProcessGroup `json:"top_cpu,omitempty"`
	TopRAM []ProcessGroup `json:"top_ram,omitempty"`
}

// ProcessGroup, aynı adlı süreçlerin toplamıdır; CPU yüzdesi makinenin toplam kapasitesine göredir.
type ProcessGroup struct {
	Name   string  `json:"name"`
	Count  int     `json:"count"`
	CPUPct float64 `json:"cpu_pct"`
	RSSMB  float64 `json:"rss_mb"`
}

// Updates, bekleyen paket güncellemeleridir; ListsUpdatedAt paket listelerinin en son güncellendiği andır.
type Updates struct {
	Pending        int    `json:"pending"`
	Security       int    `json:"security"`
	ListsUpdatedAt string `json:"lists_updated_at,omitempty"`
}

// Services, systemd servisleridir. Full=true ise liste tamdır (saklanan liste bununla değiştirilir); false ise yalnızca
// çalışmayan, yeniden başlayan ya da son rapordan beri durumu değişen servisleri içerir.
type Services struct {
	Full  bool      `json:"full"`
	Items []Service `json:"items"`
}

type Service struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Active      string `json:"active"`
	Sub         string `json:"sub,omitempty"`
	Since       string `json:"since,omitempty"` // bu duruma geçtiği an (RFC 3339)
	Restarts    *int   `json:"restarts,omitempty"`
	Enabled     string `json:"enabled,omitempty"`
}

// TimeSync, saat senkronunun ayrıntısıdır.
type TimeSync struct {
	Enabled           *bool        `json:"enabled,omitempty"`
	Synchronized      *bool        `json:"synchronized,omitempty"`
	Daemon            string       `json:"daemon,omitempty"` // timesyncd | chrony | ntpd | none
	LocalRTC          *bool        `json:"local_rtc,omitempty"`
	Server            string       `json:"server,omitempty"`
	ServerAddress     string       `json:"server_address,omitempty"`
	ConfiguredServers []string     `json:"configured_servers,omitempty"`
	Stratum           *int         `json:"stratum,omitempty"`
	Leap              string       `json:"leap,omitempty"` // normal | insert | delete | alarm
	OffsetMs          *float64     `json:"offset_ms,omitempty"`
	DelayMs           *float64     `json:"delay_ms,omitempty"`
	JitterMs          *float64     `json:"jitter_ms,omitempty"`
	RootDistanceMs    *float64     `json:"root_distance_ms,omitempty"`
	PollS             *int         `json:"poll_s,omitempty"`
	LastSync          string       `json:"last_sync,omitempty"`
	Ignored           *bool        `json:"ignored,omitempty"` // son yanıt geçersiz sayıldı
	Sources           []TimeSource `json:"sources,omitempty"`
}

type TimeSource struct {
	Name     string   `json:"name"`
	State    string   `json:"state"` // selected | candidate | falseticker | unreachable | unusable
	Reach    *int     `json:"reach,omitempty"`
	OffsetMs *float64 `json:"offset_ms,omitempty"`
}

// ---------------------------------------------------------------- çözme

// ingestWire, raporun çözüldüğü biçimdir: protokol 4 bölümleri gömülü struct'takileri gölgeler (sığ alan kazanır) ve
// önce ham alınır; her biri ayrı çözülür, böylece yanlış tipteki bir bölüm yalnızca kendisini düşürür.
type ingestWire struct {
	MetricsIngestRequest
	CPUDetail    json.RawMessage `json:"cpu_detail"`
	MemoryDetail json.RawMessage `json:"memory_detail"`
	Pressure     json.RawMessage `json:"pressure"`
	DiskIO       json.RawMessage `json:"disk_io"`
	NetIO        json.RawMessage `json:"net_io"`
	TCP          json.RawMessage `json:"tcp"`
	Temperatures json.RawMessage `json:"temperatures"`
	RAID         json.RawMessage `json:"raid"`
	Capacity     json.RawMessage `json:"capacity"`
	Processes    json.RawMessage `json:"processes"`
	Updates      json.RawMessage `json:"updates"`
	Services     json.RawMessage `json:"services"`
	TimeSync     json.RawMessage `json:"time_sync"`
}

// request, çekirdek alanları ve çözülebilen protokol 4 bölümlerini tek istekte toplar (temizlenmemiş).
func (w ingestWire) request() MetricsIngestRequest {
	r := w.MetricsIngestRequest
	decode := func(raw json.RawMessage, dst any) bool {
		return len(raw) > 0 && json.Unmarshal(raw, dst) == nil
	}
	var (
		cpu  CPUDetail
		mem  MemoryDetail
		psi  Pressure
		tcp  TCP
		capa Capacity
		proc Processes
		upd  Updates
		svc  Services
		ts   TimeSync
	)
	if decode(w.CPUDetail, &cpu) {
		r.CPUDetail = &cpu
	}
	if decode(w.MemoryDetail, &mem) {
		r.MemoryDetail = &mem
	}
	if decode(w.Pressure, &psi) {
		r.Pressure = &psi
	}
	if decode(w.TCP, &tcp) {
		r.TCP = &tcp
	}
	if decode(w.Capacity, &capa) {
		r.Capacity = &capa
	}
	if decode(w.Processes, &proc) {
		r.Processes = &proc
	}
	if decode(w.Updates, &upd) {
		r.Updates = &upd
	}
	if decode(w.Services, &svc) {
		r.Services = &svc
	}
	if decode(w.TimeSync, &ts) {
		r.TimeSync = &ts
	}
	if !decode(w.DiskIO, &r.DiskIO) {
		r.DiskIO = nil
	}
	if !decode(w.NetIO, &r.NetIO) {
		r.NetIO = nil
	}
	if !decode(w.Temperatures, &r.Temperatures) {
		r.Temperatures = nil
	}
	if !decode(w.RAID, &r.RAID) {
		r.RAID = nil
	}
	return r
}

// ---------------------------------------------------------------- temizleme

// Kabul sınırları. Listeler JSONB olarak saklanır ve panel isteklerinde döner; hatalı ya da düşmanca bir agent'ın
// şişirebilmesi engellenir. Sayı sınırları gerçek bir makinenin asla ulaşamayacağı değerlerdir.
const (
	maxV4Entries       = 64
	maxProcessGroups   = 20
	maxServices        = 2000
	maxServiceText     = 256
	maxStateText       = 32
	maxSensorBytes     = 128
	maxNTPNameBytes    = 253
	maxNTPServers      = 16
	maxTimeSources     = 32
	maxCount           = 1e9
	maxRate            = 1e15 // bayt/sn ve bit/sn
	maxIOPS            = 1e9
	maxMillis          = 1e9
	minCelsius         = -100.0
	maxCelsius         = 500.0
	maxPackageUpdates  = 1_000_000
	maxRAIDDevices     = 100_000
	maxNTPStratum      = 16
	maxNTPPollSeconds  = 10_000_000
	maxNTPReach        = 255
	maxSwapPagesPerSec = 1e12
)

var (
	temperatureKinds = map[string]bool{"cpu": true, "disk": true, "other": true}
	ntpDaemons       = map[string]bool{"timesyncd": true, "chrony": true, "ntpd": true, "none": true}
	ntpLeaps         = map[string]bool{"normal": true, "insert": true, "delete": true, "alarm": true}
	dockerHealth     = map[string]bool{"healthy": true, "unhealthy": true, "starting": true}
)

// ValidDockerHealth, docker_containers.health üzerindeki CHECK kısıtını yansıtır ("" = healthcheck yok).
func ValidDockerHealth(h string) bool { return dockerHealth[h] }

func inRange(f, lo, hi float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= lo && f <= hi
}

func floatIn(p *float64, lo, hi float64) *float64 {
	if p == nil || !inRange(*p, lo, hi) {
		return nil
	}
	v := *p
	return &v
}

func intIn(p *int, lo, hi int) *int {
	if p == nil || *p < lo || *p > hi {
		return nil
	}
	v := *p
	return &v
}

func int64NonNeg(p *int64) *int64 {
	if p == nil || *p < 0 {
		return nil
	}
	v := *p
	return &v
}

func boolCopy(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// cleanTime, RFC 3339 bir anı UTC'ye çevirir; geçersizse "" (bilinmiyor).
func cleanTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.Year() < 1970 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// sanitizeV4, protokol 4 bölümlerini saklanabilir hâle getirir: aralık dışı alanlar ve girdiler atılır, metinler
// temizlenip kırpılır, listeler sınırlanır. Hiçbir şey kalmayan bölüm nil olur.
func sanitizeV4(r *MetricsIngestRequest) {
	r.CPUDetail = sanitizeCPUDetail(r.CPUDetail)
	r.MemoryDetail = sanitizeMemoryDetail(r.MemoryDetail)
	r.Pressure = sanitizePressure(r.Pressure)
	r.DiskIO = sanitizeDiskIO(r.DiskIO)
	r.NetIO = sanitizeNetIO(r.NetIO)
	r.TCP = sanitizeTCP(r.TCP)
	r.Temperatures = sanitizeTemperatures(r.Temperatures)
	r.RAID = sanitizeRAID(r.RAID)
	r.Capacity = sanitizeCapacity(r.Capacity)
	r.Processes = sanitizeProcesses(r.Processes)
	r.Updates = sanitizeUpdates(r.Updates)
	r.Services = sanitizeServices(r.Services)
	r.TimeSync = sanitizeTimeSync(r.TimeSync)
}

func sanitizeCPUDetail(in *CPUDetail) *CPUDetail {
	if in == nil {
		return nil
	}
	out := CPUDetail{IOWaitPct: floatIn(in.IOWaitPct, 0, 100), StealPct: floatIn(in.StealPct, 0, 100),
		ProcsBlocked: intIn(in.ProcsBlocked, 0, maxCount)}
	if out == (CPUDetail{}) {
		return nil
	}
	return &out
}

func sanitizeMemoryDetail(in *MemoryDetail) *MemoryDetail {
	if in == nil {
		return nil
	}
	mb := func(p *int64) *int64 {
		if p = int64NonNeg(p); p != nil && *p > maxSwapMB {
			return nil
		}
		return p
	}
	out := MemoryDetail{AvailableMB: mb(in.AvailableMB), CachedMB: mb(in.CachedMB),
		SwapInPerS: floatIn(in.SwapInPerS, 0, maxSwapPagesPerSec), SwapOutPerS: floatIn(in.SwapOutPerS, 0, maxSwapPagesPerSec)}
	if in.OOMKills != nil {
		v := *in.OOMKills
		out.OOMKills = &v
	}
	if out == (MemoryDetail{}) {
		return nil
	}
	return &out
}

func sanitizeStall(in *PressureStall) *PressureStall {
	if in == nil || !inRange(in.Some10, 0, 100) || !inRange(in.Some60, 0, 100) {
		return nil
	}
	return &PressureStall{Some10: in.Some10, Some60: in.Some60, Full10: floatIn(in.Full10, 0, 100), Full60: floatIn(in.Full60, 0, 100)}
}

func sanitizePressure(in *Pressure) *Pressure {
	if in == nil {
		return nil
	}
	out := Pressure{CPU: sanitizeStall(in.CPU), Memory: sanitizeStall(in.Memory), IO: sanitizeStall(in.IO)}
	if out == (Pressure{}) {
		return nil
	}
	return &out
}

// uniqueName, listedeki adı temizler ve boş ya da tekrar eden adı reddeder.
func uniqueName(seen map[string]struct{}, name string, max int) (string, bool) {
	name = cleanText(name, max)
	if _, dup := seen[name]; name == "" || dup {
		return "", false
	}
	seen[name] = struct{}{}
	return name, true
}

func sanitizeDiskIO(in []DiskIO) []DiskIO {
	var out []DiskIO
	seen := map[string]struct{}{}
	for _, d := range in {
		if len(out) == maxV4Entries {
			break
		}
		if !inRange(d.ReadIOPS, 0, maxIOPS) || !inRange(d.WriteIOPS, 0, maxIOPS) || !inRange(d.ReadBps, 0, maxRate) ||
			!inRange(d.WriteBps, 0, maxRate) || !inRange(d.UtilPct, 0, 100) || !inRange(d.AwaitMs, 0, maxMillis) ||
			!inRange(d.QueueDepth, 0, maxCount) {
			continue
		}
		name, ok := uniqueName(seen, d.Name, maxDiskNameBytes)
		if !ok {
			continue
		}
		d.Name = name
		out = append(out, d)
	}
	return out
}

func sanitizeNetIO(in []NetIO) []NetIO {
	var out []NetIO
	seen := map[string]struct{}{}
	for _, n := range in {
		if len(out) == maxV4Entries {
			break
		}
		if !inRange(n.RxBps, 0, maxRate) || !inRange(n.TxBps, 0, maxRate) {
			continue
		}
		name, ok := uniqueName(seen, n.Interface, maxIfaceNameBytes)
		if !ok {
			continue
		}
		n.Interface = name
		out = append(out, n)
	}
	return out
}

func sanitizeTCP(in *TCP) *TCP {
	if in == nil {
		return nil
	}
	out := TCP{RetransPct: floatIn(in.RetransPct, 0, 100), Established: intIn(in.Established, 0, maxCount),
		TimeWait: intIn(in.TimeWait, 0, maxCount)}
	if out == (TCP{}) {
		return nil
	}
	return &out
}

func sanitizeTemperatures(in []Temperature) []Temperature {
	var out []Temperature
	seen := map[string]struct{}{}
	for _, t := range in {
		if len(out) == maxV4Entries {
			break
		}
		if !inRange(t.Celsius, minCelsius, maxCelsius) {
			continue
		}
		name, ok := uniqueName(seen, t.Sensor, maxSensorBytes)
		if !ok {
			continue
		}
		kind := t.Kind
		if !temperatureKinds[kind] {
			kind = "other"
		}
		out = append(out, Temperature{Sensor: name, Kind: kind, Celsius: t.Celsius,
			Max: floatIn(t.Max, minCelsius, maxCelsius), Crit: floatIn(t.Crit, minCelsius, maxCelsius)})
	}
	return out
}

func sanitizeRAID(in []RAID) []RAID {
	var out []RAID
	seen := map[string]struct{}{}
	for _, a := range in {
		if len(out) == maxV4Entries {
			break
		}
		state := cleanText(a.State, maxStateText)
		if state == "" || a.Devices < 0 || a.Devices > maxRAIDDevices || a.Active < 0 || a.Active > maxRAIDDevices {
			continue
		}
		name, ok := uniqueName(seen, a.Name, maxDiskNameBytes)
		if !ok {
			continue
		}
		out = append(out, RAID{Name: name, Level: cleanText(a.Level, maxStateText), State: state, Devices: a.Devices,
			Active: a.Active, SyncPct: floatIn(a.SyncPct, 0, 100)})
	}
	return out
}

func sanitizeCapacity(in *Capacity) *Capacity {
	if in == nil {
		return nil
	}
	out := Capacity{FileHandles: int64NonNeg(in.FileHandles), FileHandlesMax: int64NonNeg(in.FileHandlesMax),
		Conntrack: int64NonNeg(in.Conntrack), ConntrackMax: int64NonNeg(in.ConntrackMax),
		Tasks: int64NonNeg(in.Tasks), PIDMax: int64NonNeg(in.PIDMax)}
	if out == (Capacity{}) {
		return nil
	}
	return &out
}

func sanitizeProcessGroups(in []ProcessGroup) []ProcessGroup {
	var out []ProcessGroup
	for _, g := range in {
		if len(out) == maxProcessGroups {
			break
		}
		name := cleanText(g.Name, maxStateText*2)
		if name == "" || g.Count < 1 || g.Count > maxCount || !inRange(g.CPUPct, 0, 100) || !inRange(g.RSSMB, 0, float64(maxSwapMB)) {
			continue
		}
		out = append(out, ProcessGroup{Name: name, Count: g.Count, CPUPct: g.CPUPct, RSSMB: g.RSSMB})
	}
	return out
}

func sanitizeProcesses(in *Processes) *Processes {
	if in == nil || in.Total < 0 || in.Total > maxCount || in.Zombie < 0 || in.Zombie > maxCount {
		return nil
	}
	return &Processes{Total: in.Total, Zombie: in.Zombie, TopCPU: sanitizeProcessGroups(in.TopCPU), TopRAM: sanitizeProcessGroups(in.TopRAM)}
}

func sanitizeUpdates(in *Updates) *Updates {
	if in == nil || in.Pending < 0 || in.Pending > maxPackageUpdates || in.Security < 0 || in.Security > maxPackageUpdates {
		return nil
	}
	return &Updates{Pending: in.Pending, Security: in.Security, ListsUpdatedAt: cleanTime(in.ListsUpdatedAt)}
}

// sanitizeServices, servis listesini temizler. Boş bir TAM liste "bilinmiyor" sayılır: saklanan listeyi silmemeli
// (systemd'li bir makinede servis listesi hiçbir zaman boş değildir; boşsa toplama bozulmuştur).
func sanitizeServices(in *Services) *Services {
	if in == nil {
		return nil
	}
	out := &Services{Full: in.Full, Items: []Service{}}
	seen := map[string]struct{}{}
	for _, s := range in.Items {
		if len(out.Items) == maxServices {
			break
		}
		active := cleanText(s.Active, maxStateText)
		if active == "" {
			continue
		}
		name, ok := uniqueName(seen, s.Name, maxServiceText)
		if !ok {
			continue
		}
		out.Items = append(out.Items, Service{Name: name, Description: cleanText(s.Description, maxServiceText),
			Active: active, Sub: cleanText(s.Sub, maxStateText), Since: cleanTime(s.Since),
			Restarts: intIn(s.Restarts, 0, maxCount), Enabled: cleanText(s.Enabled, maxStateText)})
	}
	if out.Full && len(out.Items) == 0 {
		return nil
	}
	return out
}

func sanitizeTimeSync(in *TimeSync) *TimeSync {
	if in == nil {
		return nil
	}
	out := TimeSync{
		Enabled: boolCopy(in.Enabled), Synchronized: boolCopy(in.Synchronized), LocalRTC: boolCopy(in.LocalRTC),
		Ignored: boolCopy(in.Ignored),
		Server:  cleanText(in.Server, maxNTPNameBytes), ServerAddress: cleanText(in.ServerAddress, 64),
		Stratum: intIn(in.Stratum, 0, maxNTPStratum), PollS: intIn(in.PollS, 0, maxNTPPollSeconds),
		OffsetMs: floatIn(in.OffsetMs, -maxMillis, maxMillis), DelayMs: floatIn(in.DelayMs, 0, maxMillis),
		JitterMs: floatIn(in.JitterMs, 0, maxMillis), RootDistanceMs: floatIn(in.RootDistanceMs, 0, maxMillis),
		LastSync: cleanTime(in.LastSync),
	}
	if ntpDaemons[in.Daemon] {
		out.Daemon = in.Daemon
	}
	if ntpLeaps[in.Leap] {
		out.Leap = in.Leap
	}
	for _, s := range in.ConfiguredServers {
		if len(out.ConfiguredServers) == maxNTPServers {
			break
		}
		if s = cleanText(s, maxNTPNameBytes); s != "" {
			out.ConfiguredServers = append(out.ConfiguredServers, s)
		}
	}
	for _, s := range in.Sources {
		if len(out.Sources) == maxTimeSources {
			break
		}
		name, state := cleanText(s.Name, maxNTPNameBytes), cleanText(s.State, maxStateText)
		if name == "" || state == "" {
			continue
		}
		out.Sources = append(out.Sources, TimeSource{Name: name, State: state, Reach: intIn(s.Reach, 0, maxNTPReach),
			OffsetMs: floatIn(s.OffsetMs, -maxMillis, maxMillis)})
	}
	if out.Enabled == nil && out.Synchronized == nil && out.LocalRTC == nil && out.Ignored == nil && out.Server == "" &&
		out.ServerAddress == "" && out.Stratum == nil && out.PollS == nil && out.OffsetMs == nil && out.DelayMs == nil &&
		out.JitterMs == nil && out.RootDistanceMs == nil && out.LastSync == "" && out.Daemon == "" && out.Leap == "" &&
		out.ConfiguredServers == nil && out.Sources == nil {
		return nil
	}
	return &out
}

// ---------------------------------------------------------------- kayıt

// SystemSample, metrics.system_json'a giren zaman serisidir: CPU ayrıntısı, bellek ayrıntısı (OOM sayacı hariç: o
// anlık durumdadır), PSI ve TCP.
type SystemSample struct {
	CPUDetail    *CPUDetail    `json:"cpu_detail,omitempty"`
	MemoryDetail *MemoryDetail `json:"memory_detail,omitempty"`
	Pressure     *Pressure     `json:"pressure,omitempty"`
	TCP          *TCP          `json:"tcp,omitempty"`
}

// MetricSeries, bir metrik satırının protokol 4 sütunlarıdır; boş alan NULL yazılır.
type MetricSeries struct {
	System *SystemSample
	DiskIO []DiskIO
	NetIO  []NetIO
}

// Series, isteğin metrics satırına yazılacak protokol 4 zaman serisini döndürür.
func (r MetricsIngestRequest) Series() MetricSeries {
	s := MetricSeries{DiskIO: r.DiskIO, NetIO: r.NetIO}
	sys := SystemSample{CPUDetail: r.CPUDetail, Pressure: r.Pressure, TCP: r.TCP}
	if m := r.MemoryDetail; m != nil {
		mem := *m
		mem.OOMKills = nil
		if mem != (MemoryDetail{}) {
			sys.MemoryDetail = &mem
		}
	}
	if sys != (SystemSample{}) {
		s.System = &sys
	}
	return s
}

// SystemState, host_status.system_state'tir: protokol 4'ün geçmişi tutulmayan anlık durumları. Her rapor bunu son
// bildirilenle değiştirir; raporda olmayan bölüm "bilinmiyor"dur.
type SystemState struct {
	// OOMKills, çekirdeğin açılıştan beri bellek yetmediği için öldürdüğü süreç sayısıdır; OOMLastIncreaseAt sayacın
	// en son arttığını server'ın gördüğü andır (oom_kill alert'i bununla kapanır).
	OOMKills          *uint64       `json:"oom_kills,omitempty"`
	OOMLastIncreaseAt *time.Time    `json:"oom_last_increase_at,omitempty"`
	Temperatures      []Temperature `json:"temperatures,omitempty"`
	RAID              []RAID        `json:"raid,omitempty"`
	Capacity          *Capacity     `json:"capacity,omitempty"`
	Processes         *Processes    `json:"processes,omitempty"`
	Updates           *Updates      `json:"updates,omitempty"`
	TimeSync          *TimeSync     `json:"time_sync,omitempty"`
}

func (s SystemState) empty() bool {
	return s.OOMKills == nil && s.OOMLastIncreaseAt == nil && s.Temperatures == nil && s.RAID == nil &&
		s.Capacity == nil && s.Processes == nil && s.Updates == nil && s.TimeSync == nil
}

// NextSystemState, önceki anlık durum ve yeni rapordan saklanacak durumu kurar; hiçbir şey bilinmiyorsa nil (eski
// agent). Önceki durumdan yalnızca OOM sayacının artış anı taşınır: sayaç bu raporda öncekinden büyükse artış anı now
// olur. Sayaç geri gittiyse (yeniden açılış) artış sayılmaz; önceki sayaç bilinmiyorsa ilk değer de artış sayılmaz
// (açılıştan beri birikmiş olabilir).
func NextSystemState(prev *SystemState, r MetricsIngestRequest, now time.Time) *SystemState {
	s := SystemState{Temperatures: r.Temperatures, RAID: r.RAID, Capacity: r.Capacity, Processes: r.Processes,
		Updates: r.Updates, TimeSync: r.TimeSync}
	if m := r.MemoryDetail; m != nil && m.OOMKills != nil {
		n := *m.OOMKills
		s.OOMKills = &n
		if prev != nil {
			s.OOMLastIncreaseAt = prev.OOMLastIncreaseAt
			if prev.OOMKills != nil && n > *prev.OOMKills {
				t := now.UTC()
				s.OOMLastIncreaseAt = &t
			}
		}
	}
	if s.empty() {
		return nil
	}
	return &s
}

// ---------------------------------------------------------------- okuma ve izleme seçimi

// HostService, bir sunucunun saklanan servisidir (GET /hosts/:id/services). Watched, servisin izlenen servisler
// arasında olup olmadığıdır: izlenen servis çalışmazsa alert üretir.
type HostService struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Active      string     `json:"active"`
	Sub         string     `json:"sub,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Restarts    *int       `json:"restarts,omitempty"`
	Enabled     string     `json:"enabled,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"` // satırın içeriğinin en son değiştiği an
	Watched     bool       `json:"watched"`
}

const maxWatchedServices = 256

// ValidateServiceList, izlenen servis seçimini denetler. Adlar agent'ın bildirdiği gibi birebir karşılaştırılır; bu
// yüzden normalleştirme yapılmaz. Şu an raporlanmayan bir servis de seçilebilir (kaldırılmış ya da geçici olarak
// görünmeyen servisin seçimi kaybolmasın).
func ValidateServiceList(names []string) error {
	if len(names) > maxWatchedServices {
		return fmt.Errorf("en fazla %d servis izlenebilir", maxWatchedServices)
	}
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		switch {
		case strings.TrimSpace(n) == "":
			return errors.New("servis adı boş olamaz")
		case len(n) > maxServiceText:
			return fmt.Errorf("servis adı %d bayttan uzun olamaz", maxServiceText)
		case strings.IndexFunc(n, unicode.IsControl) >= 0:
			return errors.New("servis adı kontrol karakteri içeremez")
		}
		if _, dup := seen[n]; dup {
			return fmt.Errorf("%q iki kez listelenmiş", n)
		}
		seen[n] = struct{}{}
	}
	return nil
}
