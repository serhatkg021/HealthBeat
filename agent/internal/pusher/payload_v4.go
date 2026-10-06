package pusher

// Protokol 4'ün alanları (bkz. docs/COMPATIBILITY.md §2). Hepsi isteğe bağlıdır: toplanamayan ya da henüz bilinmeyen
// alan hiç gönderilmez ("bilinmiyor"); bir nesne gönderildiyse içindeki sayılar ölçülmüştür, bu yüzden yalnızca kendi
// başına bilinmeyebilecek alanlar işaretçidir. Oranlar ve "saniyede" değerleri iki örnek arasındaki farktır: agent'ın
// ilk raporunda ve sayaç geri gittiğinde (yeniden açılış) gönderilmez. Kümülatif sayaçları (oom_kills, restarts) olduğu
// gibi gönderir; farkı server hesaplar.

// CPUDetail, CPU zamanının dağılımıdır (/proc/stat).
type CPUDetail struct {
	IOWaitPct    *float64 `json:"iowait_pct,omitempty"`    // diske bekleyen CPU
	StealPct     *float64 `json:"steal_pct,omitempty"`     // sanal makinede hipervizörün başkasına verdiği CPU
	ProcsBlocked *int     `json:"procs_blocked,omitempty"` // G/Ç'de takılı (D durumunda) süreç sayısı
}

// MemoryDetail, RAM yüzdesinin ötesindeki bellek durumudur (/proc/meminfo, /proc/vmstat).
type MemoryDetail struct {
	AvailableMB *int64   `json:"available_mb,omitempty"`
	CachedMB    *int64   `json:"cached_mb,omitempty"`
	SwapInPerS  *float64 `json:"swap_in_per_s,omitempty"`  // swap'tan okunan sayfa/sn
	SwapOutPerS *float64 `json:"swap_out_per_s,omitempty"` // swap'a yazılan sayfa/sn
	OOMKills    *uint64  `json:"oom_kills,omitempty"`      // açılıştan beri çekirdeğin bellek yetmediği için öldürdüğü süreç
}

// Pressure, PSI'dır (/proc/pressure): süreçlerin kaynak beklerken geçirdiği zamanın yüzdesi, 10 ve 60 sn ortalaması.
// "some": en az bir süreç bekliyor; "full": hiçbir süreç ilerleyemiyor (CPU'da eski çekirdekler vermez).
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

// DiskIO, bir fiziksel diskin G/Ç'sidir (/proc/diskstats); diskler physical_disks keşfindekilerdir.
type DiskIO struct {
	Name       string  `json:"name"`
	ReadIOPS   float64 `json:"read_iops"`
	WriteIOPS  float64 `json:"write_iops"`
	ReadBps    float64 `json:"read_bps"`
	WriteBps   float64 `json:"write_bps"`
	UtilPct    float64 `json:"util_pct"`    // diskin meşgul geçirdiği zaman; SSD/NVMe'de yanıltıcıdır
	AwaitMs    float64 `json:"await_ms"`    // bir işlemin ortalama süresi
	QueueDepth float64 `json:"queue_depth"` // ortalama bekleyen işlem
}

// NetIO, bir ağ arayüzünün trafiğidir (/proc/net/dev); hata ve düşen paket sayıları aralıktaki farktır.
type NetIO struct {
	Interface string  `json:"interface"`
	RxBps     float64 `json:"rx_bps"` // bit/sn
	TxBps     float64 `json:"tx_bps"`
	RxErrors  uint64  `json:"rx_errors"`
	TxErrors  uint64  `json:"tx_errors"`
	RxDrops   uint64  `json:"rx_drops"`
	TxDrops   uint64  `json:"tx_drops"`
}

// TCP, makine geneli TCP durumudur (/proc/net/snmp, /proc/net/sockstat).
type TCP struct {
	RetransPct  *float64 `json:"retrans_pct,omitempty"` // aralıkta yeniden gönderilen segmentlerin oranı
	Established *int     `json:"established,omitempty"`
	TimeWait    *int     `json:"time_wait,omitempty"`
}

// Temperature, bir sıcaklık sensörüdür (/sys/class/hwmon); yalnızca sanal olmayan makinelerde gönderilir.
type Temperature struct {
	Sensor  string   `json:"sensor"`
	Kind    string   `json:"kind,omitempty"` // cpu | disk | other
	Celsius float64  `json:"celsius"`
	Max     *float64 `json:"max,omitempty"`  // donanımın bildirdiği üst sınır
	Crit    *float64 `json:"crit,omitempty"` // donanımın bildirdiği kritik sınır
}

// RAID, bir yazılım RAID dizisidir (/proc/mdstat).
type RAID struct {
	Name    string   `json:"name"`
	Level   string   `json:"level,omitempty"`
	State   string   `json:"state"` // clean | degraded | recovering | resyncing | failed …
	Devices int      `json:"devices"`
	Active  int      `json:"active"`
	SyncPct *float64 `json:"sync_pct,omitempty"` // yeniden kurulum/eşitleme sürüyorsa
}

// Capacity, çekirdeğin sınırlı tablolarının doluluğudur.
type Capacity struct {
	FileHandles    *int64 `json:"file_handles,omitempty"`
	FileHandlesMax *int64 `json:"file_handles_max,omitempty"`
	Conntrack      *int64 `json:"conntrack,omitempty"` // bağlantı izleme modülü yüklü değilse yok
	ConntrackMax   *int64 `json:"conntrack_max,omitempty"`
	Tasks          *int64 `json:"tasks,omitempty"` // süreç + iş parçacığı: pid_max sınırı bunlara uygulanır
	PIDMax         *int64 `json:"pid_max,omitempty"`
}

// Processes, süreç özetidir; CPU yüzdesi makinenin toplam kapasitesine göredir (cpu_usage_pct ile aynı ölçek), RSS
// grubun süreçlerinin toplamıdır. Yalnızca süreç adı (comm) gönderilir: komut satırı ve kullanıcı gönderilmez (komut
// satırında parola gibi sırlar olabilir).
type Processes struct {
	Total  int            `json:"total"`
	Zombie int            `json:"zombie"`
	TopCPU []ProcessGroup `json:"top_cpu,omitempty"`
	TopRAM []ProcessGroup `json:"top_ram,omitempty"`
}

// ProcessGroup, aynı adlı süreçlerin toplamıdır (ör. postgres ×23).
type ProcessGroup struct {
	Name   string  `json:"name"`
	Count  int     `json:"count"`
	CPUPct float64 `json:"cpu_pct"`
	RSSMB  float64 `json:"rss_mb"`
}

// Updates, bekleyen paket güncellemeleridir (yalnızca apt ailesi).
type Updates struct {
	Pending  int `json:"pending"`
	Security int `json:"security"`
	// ListsUpdatedAt, paket listelerinin en son güncellendiği andır (RFC 3339): agent listeleri kendisi güncellemez,
	// listeler eskiyse sayılar da eskidir.
	ListsUpdatedAt string `json:"lists_updated_at,omitempty"`
}

// Services, systemd servisleridir. Full=true ise liste tamdır (server kendi listesini bununla değiştirir); false ise
// yalnızca çalışmayan, yeniden başlayan ya da son rapordan beri durumu değişen servisleri içerir.
type Services struct {
	Full  bool      `json:"full"`
	Items []Service `json:"items"`
}

type Service struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Active      string `json:"active"`             // active | inactive | failed | activating | deactivating | reloading
	Sub         string `json:"sub,omitempty"`      // running | exited | dead | failed | auto-restart …
	Since       string `json:"since,omitempty"`    // bu duruma geçtiği an (RFC 3339)
	Restarts    *int   `json:"restarts,omitempty"` // systemd'nin otomatik yeniden başlatma sayacı (NRestarts)
	Enabled     string `json:"enabled,omitempty"`  // enabled | disabled | static …
}

// TimeSync, saat senkronunun ayrıntısıdır (timedatectl / systemd-timesyncd, chrony, ntpd).
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
	LastSync          string       `json:"last_sync,omitempty"` // RFC 3339
	Ignored           *bool        `json:"ignored,omitempty"`   // son yanıt geçersiz sayıldı (timesyncd)
	Sources           []TimeSource `json:"sources,omitempty"`
}

// TimeSource, bir NTP kaynağıdır.
type TimeSource struct {
	Name     string   `json:"name"`
	State    string   `json:"state"` // selected | candidate | falseticker | unreachable | unusable
	Reach    *int     `json:"reach,omitempty"`
	OffsetMs *float64 `json:"offset_ms,omitempty"`
}
