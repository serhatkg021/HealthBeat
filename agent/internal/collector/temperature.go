package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxTemperatures, raporlanan sıcaklık sensörü sayısının üst sınırıdır.
const maxTemperatures = 64

// TemperatureReading, bir sıcaklık sensörünün okumasıdır (°C).
type TemperatureReading struct {
	Sensor    string // "<sürücü>[/<aygıt>]/<etiket>", ör. "coretemp/Package id 0", "nvme/nvme0/Composite"
	Kind      string // cpu | disk | other
	Celsius   float64
	Max, Crit *float64 // donanımın bildirdiği sınırlar; nil = bildirmiyor
}

// cpuDrivers, CPU sıcaklığı veren hwmon sürücüleridir. Bunların çekirdek sensörleri ("Core N") tek bir "en sıcak çekirdek"
// okumasında toplanır: 64 çekirdekli bir sunucu 64 satır göndermesin.
var cpuDrivers = map[string]bool{"coretemp": true, "k10temp": true, "zenpower": true, "cpu_thermal": true}

// diskDrivers, disk sıcaklığı veren hwmon sürücüleridir (drivetemp: SATA, yalnızca modül yüklüyse).
var diskDrivers = map[string]bool{"nvme": true, "drivetemp": true}

// ReadTemperatures, /sys/class/hwmon'daki sıcaklık sensörlerini okur; okunamayan (ör. "veri yok" dönen) ya da akla
// yatkın olmayan okuma atlanır. Sensörü olmayan makinede (çoğu sanal makine) nil.
func ReadTemperatures(root string) []TemperatureReading {
	dirs, _ := filepath.Glob(rootPath(root, "/sys/class/hwmon/hwmon*"))
	sort.Strings(dirs)
	var out []TemperatureReading
	seen := map[string]bool{}
	for _, dir := range dirs {
		driver := strings.TrimSpace(readText(filepath.Join(dir, "name")))
		if driver == "" {
			continue
		}
		prefix := driver
		if diskDrivers[driver] {
			// Hangi disk olduğu sürücü adından anlaşılmaz: hwmon'un bağlı olduğu aygıtın adı eklenir (nvme0, 0:0:0:0).
			if target, err := os.Readlink(filepath.Join(dir, "device")); err == nil {
				prefix += "/" + filepath.Base(target)
			}
		}

		var hottestCore *TemperatureReading
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(inputs)
		for _, input := range inputs {
			base := strings.TrimSuffix(input, "_input")
			c, ok := milliCelsius(readText(input))
			if !ok {
				continue
			}
			label := strings.TrimSpace(readText(base + "_label"))
			if label == "" {
				label = filepath.Base(base) // temp1
			}
			r := TemperatureReading{Sensor: prefix + "/" + label, Kind: kindOf(driver), Celsius: c}
			if v, ok := milliCelsius(readText(base + "_max")); ok {
				r.Max = &v
			}
			if v, ok := milliCelsius(readText(base + "_crit")); ok {
				r.Crit = &v
			}
			if cpuDrivers[driver] && strings.HasPrefix(label, "Core ") {
				if hottestCore == nil || r.Celsius > hottestCore.Celsius {
					r.Sensor = prefix + "/hottest core"
					hottestCore = &r
				}
				continue
			}
			out = appendUnique(out, r, seen, dir)
		}
		if hottestCore != nil {
			out = appendUnique(out, *hottestCore, seen, dir)
		}
	}
	if len(out) > maxTemperatures {
		out = out[:maxTemperatures]
	}
	return out
}

// appendUnique, aynı adı taşıyan iki sensöre (aynı sürücüden iki hwmon aygıtı) hwmon numarasını ekler.
func appendUnique(out []TemperatureReading, r TemperatureReading, seen map[string]bool, dir string) []TemperatureReading {
	if seen[r.Sensor] {
		r.Sensor = fmt.Sprintf("%s (%s)", r.Sensor, filepath.Base(dir))
	}
	seen[r.Sensor] = true
	return append(out, r)
}

func kindOf(driver string) string {
	switch {
	case cpuDrivers[driver]:
		return "cpu"
	case diskDrivers[driver]:
		return "disk"
	}
	return "other"
}

// milliCelsius, hwmon'un milidereceli değerini °C'ye çevirir; -50…200 dışını (sensör hatası) kabul etmez.
func milliCelsius(s string) (float64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	c := float64(n) / 1000
	if c < -50 || c > 200 {
		return 0, false
	}
	return c, true
}

func readText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
