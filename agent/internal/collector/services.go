package collector

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// maxServices, raporlanan servis sayısının üst sınırıdır (server da aynı sınırı uygular).
const maxServices = 2000

// showChunk, tek bir `systemctl show` çağrısına verilen servis adı sayısıdır: argüman listesi ve çağrı süresi sınırlı kalsın.
const showChunk = 200

// ServiceState, bir systemd servisinin durumudur.
type ServiceState struct {
	Name        string
	Description string
	Active      string    // active | inactive | failed | activating | deactivating | reloading
	Sub         string    // running | exited | dead | failed | auto-restart …
	Enabled     string    // enabled | disabled | static … ("" = bilinmiyor)
	Since       time.Time // bu duruma geçtiği an; sıfır = bilinmiyor
	Restarts    *int      // NRestarts (systemd 235+); nil = bilinmiyor
}

// ServiceCollector, systemd servislerini yetkisiz toplar: `systemctl list-units` ve `systemctl show` systemd'ye D-Bus
// ile sorar (sandbox'a uygun; agent `systemctl --failed`'ı zaten çalıştırıyor).
type ServiceCollector struct {
	root string           // sahte kök (test); "" = gerçek /
	now  func() time.Time // test için
	run  func(ctx context.Context, name string, args ...string) (string, error)
}

func NewServiceCollector() *ServiceCollector {
	return &ServiceCollector{now: time.Now, run: runCommand}
}

// Collect, kurulu (yüklenmiş) servislerin durumunu döndürür. systemd olmayan makinede nil, nil ("bilinmiyor": alan
// gönderilmez). Liste alınamazsa hata döner; ayrıntı (show) alınamazsa servisler ayrıntısız döner.
func (c *ServiceCollector) Collect(ctx context.Context) ([]ServiceState, error) {
	if _, err := os.Stat(rootPath(c.root, "/run/systemd/system")); err != nil {
		return nil, nil
	}
	out, err := c.run(ctx, "systemctl", "list-units", "--type=service", "--all", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil, err
	}
	services := parseListUnits(out)
	if len(services) > maxServices {
		services = services[:maxServices]
	}

	boot, bootKnown := c.bootTime()
	index := make(map[string]int, len(services))
	names := make([]string, len(services))
	for i, s := range services {
		index[s.Name], names[i] = i, s.Name
	}
	for start := 0; start < len(names); start += showChunk {
		chunk := names[start:min(start+showChunk, len(names))]
		args := append([]string{"show", "--property=Id,NRestarts,StateChangeTimestampMonotonic,UnitFileState"}, chunk...)
		text, err := c.run(ctx, "systemctl", args...)
		if err != nil {
			continue // ayrıntısız servis yine de durumuyla raporlanır
		}
		for _, p := range parseShow(text) {
			i, ok := index[p["Id"]]
			if !ok {
				continue
			}
			s := &services[i]
			s.Enabled = p["UnitFileState"]
			if n, err := strconv.Atoi(p["NRestarts"]); err == nil && n >= 0 {
				s.Restarts = &n
			}
			if us, err := strconv.ParseInt(p["StateChangeTimestampMonotonic"], 10, 64); err == nil && us > 0 && bootKnown {
				s.Since = boot.Add(time.Duration(us) * time.Microsecond).UTC().Truncate(time.Second)
			}
		}
	}
	return services, nil
}

// bootTime, açılış anıdır (şimdi − /proc/uptime). Servislerin "ne zamandan beri" bilgisi systemd'den açılıştan beri
// geçen mikrosaniye olarak alınır: tarih metni "+03", "CEST" gibi saat dilimi kısaltmaları içerdiği için güvenilir
// ayrıştırılamaz.
func (c *ServiceCollector) bootTime() (time.Time, bool) {
	b, err := os.ReadFile(rootPath(c.root, "/proc/uptime"))
	if err != nil {
		return time.Time{}, false
	}
	up, ok := parseUptime(string(b))
	if !ok {
		return time.Time{}, false
	}
	return c.now().Add(-time.Duration(up * float64(time.Second))), true
}

// parseListUnits, `systemctl list-units --plain --no-legend` satırlarını okur: UNIT LOAD ACTIVE SUB DESCRIPTION.
// Yüklenmemiş servisler (not-found: başka unit'lerin andığı ama kurulu olmayan; masked: devre dışı bırakılmış) atlanır.
func parseListUnits(text string) []ServiceState {
	var out []ServiceState
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") || fields[1] != "loaded" {
			continue
		}
		s := ServiceState{Name: fields[0], Active: fields[2], Sub: fields[3]}
		if len(fields) > 4 {
			s.Description = strings.Join(fields[4:], " ")
		}
		out = append(out, s)
	}
	return out
}

// parseShow, `systemctl show` çıktısını (boş satırla ayrılmış "Ad=Değer" blokları) özellik haritalarına çevirir.
func parseShow(text string) []map[string]string {
	var out []map[string]string
	cur := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = map[string]string{}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
