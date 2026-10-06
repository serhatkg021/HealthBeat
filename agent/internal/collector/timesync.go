package collector

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Saat senkronunun ayrıntısı. Hepsi yetkisiz: timedatectl ve busctl systemd'ye D-Bus ile sorar, chronyc ve ntpq yerel
// UDP ile daemon'un komut portuna bağlanır (sandbox'a uygun). Hata METİNLERİ (ör. "zaman aşımı") journal'dadır ve
// okunmaz; agent'a tüm loglara erişim verilmez. Sorunlar durumdan türetilir (bkz. TimeSource.State).
//
// Saat farkı (OffsetMs) işareti NTP'nin tanımıdır: sunucu saati − yerel saat. Pozitif = yerel saat geride.

// TimeSyncInfo, saat senkronunun durumudur; nil alan = bilinmiyor.
type TimeSyncInfo struct {
	Enabled, Synchronized, LocalRTC *bool
	Daemon                          string // timesyncd | chrony | ntpd | none
	Server, ServerAddress           string
	ConfiguredServers               []string
	Stratum                         *int // kullanılan sunucunun stratumu
	Leap                            string
	OffsetMs, DelayMs, JitterMs     *float64
	RootDistanceMs                  *float64
	PollS                           *int
	LastSync                        time.Time // sıfır = bilinmiyor
	Ignored                         *bool     // son yanıt geçersiz sayıldı (timesyncd)
	Sources                         []TimeSource
}

// TimeSource, bir NTP kaynağıdır. State: selected | candidate | falseticker | unreachable | unusable.
type TimeSource struct {
	Name     string
	State    string
	Reach    *int // son 8 denemenin bit maskesi (255 = hepsi başarılı)
	OffsetMs *float64
}

// timeDaemons, aranan servisler ve daemon adı; sıra önceliktir.
var timeDaemons = []struct{ unit, daemon string }{
	{"systemd-timesyncd.service", "timesyncd"}, {"chronyd.service", "chrony"}, {"chrony.service", "chrony"},
	{"ntpd.service", "ntpd"}, {"ntpsec.service", "ntpd"}, {"ntp.service", "ntpd"},
}

// TimeSyncCollector, saat senkronunu toplar (5 dakikada bir, arka planda).
type TimeSyncCollector struct {
	root string // sahte kök (test); "" = gerçek /
	run  func(ctx context.Context, name string, args ...string) (string, error)
}

func NewTimeSyncCollector() *TimeSyncCollector { return &TimeSyncCollector{run: runCommand} }

// Collect, systemd olmayan makinede nil, nil ("bilinmiyor": alan gönderilmez) döndürür.
func (c *TimeSyncCollector) Collect(ctx context.Context) (*TimeSyncInfo, error) {
	if _, err := os.Stat(rootPath(c.root, "/run/systemd/system")); err != nil {
		return nil, nil
	}
	info := &TimeSyncInfo{Daemon: "none"}
	if out, err := c.run(ctx, "timedatectl", "show"); err == nil {
		p := parseKeyValues(out)
		info.Enabled, info.Synchronized, info.LocalRTC = yesNo(p["NTP"]), yesNo(p["NTPSynchronized"]), yesNo(p["LocalRTC"])
	}

	units := make([]string, len(timeDaemons))
	for i, d := range timeDaemons {
		units[i] = d.unit
	}
	if out, err := c.run(ctx, "systemctl", append([]string{"show", "--property=Id,ActiveState"}, units...)...); err == nil {
		active := map[string]bool{}
		for _, p := range parseShow(out) {
			active[p["Id"]] = p["ActiveState"] == "active"
		}
		for _, d := range timeDaemons {
			if active[d.unit] {
				info.Daemon = d.daemon
				break
			}
		}
	}

	switch info.Daemon {
	case "timesyncd":
		c.timesyncd(ctx, info)
	case "chrony":
		c.chrony(ctx, info)
	case "ntpd":
		c.ntpd(ctx, info)
	}
	return info, nil
}

// ---- systemd-timesyncd -------------------------------------------------------------------------------------------

// timesyncd, timesyncd'nin D-Bus özelliklerini busctl'in JSON çıktısıyla okur (systemd 240+; daha eskisinde yalnızca
// timedatectl'in temel alanları kalır).
func (c *TimeSyncCollector) timesyncd(ctx context.Context, info *TimeSyncInfo) {
	props := []string{"ServerName", "ServerAddress", "PollIntervalUSec", "NTPMessage", "SystemNTPServers", "LinkNTPServers", "FallbackNTPServers"}
	out, err := c.run(ctx, "busctl", append([]string{"--json=short", "get-property", "org.freedesktop.timesync1",
		"/org/freedesktop/timesync1", "org.freedesktop.timesync1.Manager"}, props...)...)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(props) {
		return
	}
	data := map[string]json.RawMessage{}
	for i, line := range lines {
		var v struct { // her satıra yeni değişken: RawMessage çözülürken önceki satırın belleğini yeniden kullanırdı
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal([]byte(line), &v) == nil {
			data[props[i]] = v.Data
		}
	}

	json.Unmarshal(data["ServerName"], &info.Server)
	info.ServerAddress = decodeTimesyncAddress(data["ServerAddress"])
	var poll uint64
	if json.Unmarshal(data["PollIntervalUSec"], &poll) == nil && poll > 0 {
		s := int(poll / 1e6)
		info.PollS = &s
	}
	var system, link, fallback []string
	json.Unmarshal(data["SystemNTPServers"], &system)
	json.Unmarshal(data["LinkNTPServers"], &link)
	json.Unmarshal(data["FallbackNTPServers"], &fallback)
	info.ConfiguredServers = append(system, link...)
	if len(info.ConfiguredServers) == 0 {
		info.ConfiguredServers = fallback // timesyncd yalnızca ikisi de boşsa yedek sunuculara gider
	}
	applyNTPMessage(data["NTPMessage"], info)
}

// decodeTimesyncAddress, (iay) biçimindeki adresi (adres ailesi, baytlar) metne çevirir; boş = henüz bağlanmadı.
func decodeTimesyncAddress(raw json.RawMessage) string {
	var tuple []json.RawMessage
	if json.Unmarshal(raw, &tuple) != nil || len(tuple) != 2 {
		return ""
	}
	var bytes []byte
	var ints []int
	if json.Unmarshal(tuple[1], &ints) != nil || (len(ints) != 4 && len(ints) != 16) {
		return ""
	}
	for _, n := range ints {
		bytes = append(bytes, byte(n))
	}
	return net.IP(bytes).String()
}

// applyNTPMessage, timesyncd'nin son NTP yanıtını (uuuuittayttttbtt) okur: leap, sürüm, mod, stratum, kesinlik, kök
// gecikmesi (µs), kök dağılımı (µs), referans, T1–T4 zaman damgaları (µs), geçersiz mi, paket sayısı, jitter (µs). Saat
// farkı ve gecikme NTP'nin formülleriyle T1–T4'ten hesaplanır (`timedatectl timesync-status` ile aynı sonuç).
func applyNTPMessage(raw json.RawMessage, info *TimeSyncInfo) {
	var m []json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != 15 {
		return
	}
	num := func(i int) (float64, bool) {
		var f float64
		return f, json.Unmarshal(m[i], &f) == nil
	}
	if packets, ok := num(13); !ok || packets == 0 {
		return // henüz hiç yanıt yok
	}
	if leap, ok := num(0); ok {
		info.Leap = leapName(int(leap))
	}
	if st, ok := num(3); ok {
		s := int(st)
		info.Stratum = &s
	}
	delay, ok1 := num(5)
	disp, ok2 := num(6)
	if ok1 && ok2 {
		rd := delay/2/1000 + disp/1000
		info.RootDistanceMs = &rd
	}
	t1, ok1 := num(8)
	t2, ok2 := num(9)
	t3, ok3 := num(10)
	t4, ok4 := num(11)
	if ok1 && ok2 && ok3 && ok4 && t1 > 0 && t4 > 0 {
		offset := ((t2 - t1) + (t3 - t4)) / 2 / 1000
		rtt := ((t4 - t1) - (t3 - t2)) / 1000
		info.OffsetMs, info.DelayMs = &offset, &rtt
		info.LastSync = time.UnixMicro(int64(t4)).UTC()
	}
	var ignored bool
	if json.Unmarshal(m[12], &ignored) == nil {
		info.Ignored = &ignored
	}
	if j, ok := num(14); ok {
		jit := j / 1000
		info.JitterMs = &jit
	}
	if info.ServerAddress != "" {
		state := "selected"
		if ignored || info.Leap == "alarm" {
			state = "unusable"
		}
		info.Sources = []TimeSource{{Name: info.ServerAddress, State: state, OffsetMs: info.OffsetMs}}
	}
}

func leapName(code int) string {
	switch code {
	case 0:
		return "normal"
	case 1:
		return "insert"
	case 2:
		return "delete"
	case 3:
		return "alarm"
	}
	return ""
}

// ---- chrony ------------------------------------------------------------------------------------------------------

func (c *TimeSyncCollector) chrony(ctx context.Context, info *TimeSyncInfo) {
	if out, err := c.run(ctx, "chronyc", "-n", "-c", "sources"); err == nil {
		applyChronySources(out, info)
	}
	if out, err := c.run(ctx, "chronyc", "-n", "-c", "tracking"); err == nil {
		applyChronyTracking(out, info)
	}
}

// applyChronyTracking, `chronyc -c tracking` satırını okur: RefID, ad/IP, stratum (makinenin kendisi), referans zamanı,
// sistem saati düzeltmesi (s; NTP işaretiyle aynı: negatif = yerel saat ileride), son, RMS, frekans, artık frekans,
// sapma, kök gecikmesi, kök dağılımı, güncelleme aralığı, leap durumu.
func applyChronyTracking(text string, info *TimeSyncInfo) {
	f := strings.Split(strings.TrimSpace(text), ",")
	if len(f) < 14 {
		return
	}
	if f[1] != "" && f[1] != "0.0.0.0" { // henüz kaynak seçilmedi
		info.Server, info.ServerAddress = f[1], f[1]
	}
	if ref, err := strconv.ParseFloat(f[3], 64); err == nil && ref > 0 {
		sec, frac := math.Modf(ref)
		info.LastSync = time.Unix(int64(sec), int64(frac*1e9)).UTC().Truncate(time.Millisecond)
	}
	if off, err := strconv.ParseFloat(f[4], 64); err == nil {
		ms := off * 1000
		info.OffsetMs = &ms
	}
	rootDelay, e1 := strconv.ParseFloat(f[10], 64)
	rootDisp, e2 := strconv.ParseFloat(f[11], 64)
	if e1 == nil && e2 == nil {
		rd := (rootDelay/2 + rootDisp) * 1000
		info.RootDistanceMs = &rd
	}
	if upd, err := strconv.ParseFloat(f[12], 64); err == nil && upd > 0 {
		s := int(upd + 0.5)
		info.PollS = &s
	}
	info.Leap = map[string]string{"Normal": "normal", "Insert second": "insert", "Delete second": "delete",
		"Not synchronised": "alarm"}[strings.TrimSpace(f[13])]
	if info.Stratum == nil { // seçili kaynak yoksa makinenin kendi stratumundan bir eksik
		if st, err := strconv.Atoi(f[2]); err == nil && st > 0 && st < 16 {
			s := st - 1
			info.Stratum = &s
		}
	}
}

// applyChronySources, `chronyc -c sources` satırlarını okur: mod, durum, ad/IP, stratum, poll (log2 sn), reach (sekizlik),
// son alım, son örnek farkı (s), ölçülen fark, hata payı. chrony belgelerine göre kaynak farkı pozitifse yerel saat
// kaynaktan ileridedir ("Positive offsets indicate that the local clock is ahead of the source"): NTP işaretine çevrilir.
func applyChronySources(text string, info *TimeSyncInfo) {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 10 || f[0] == "#" { // # = yerel referans saati (GPS/PPS), NTP kaynağı değil
			continue
		}
		src := TimeSource{Name: f[2]}
		if r, err := strconv.ParseInt(f[5], 8, 64); err == nil {
			reach := int(r)
			src.Reach = &reach
		}
		if off, err := strconv.ParseFloat(f[7], 64); err == nil {
			ms := -off * 1000
			src.OffsetMs = &ms
		}
		switch f[1] {
		case "*":
			src.State = "selected"
			if st, err := strconv.Atoi(f[3]); err == nil {
				info.Stratum = &st
			}
		case "+", "-", "=":
			src.State = "candidate"
		case "x":
			src.State = "falseticker"
		default: // "?" kullanılamaz, "~" çok değişken
			src.State = "unusable"
		}
		if src.Reach != nil && *src.Reach == 0 {
			src.State = "unreachable"
		}
		info.Sources = append(info.Sources, src)
		info.ConfiguredServers = append(info.ConfiguredServers, src.Name)
	}
}

// ---- ntpd / ntpsec -----------------------------------------------------------------------------------------------

func (c *TimeSyncCollector) ntpd(ctx context.Context, info *TimeSyncInfo) {
	if out, err := c.run(ctx, "ntpq", "-pn"); err == nil {
		applyNtpq(out, info)
	}
}

// applyNtpq, `ntpq -pn` tablosunu okur: ilk karakter durum (* seçili, + - # aday, x yanlış zaman veriyor, o PPS),
// remote, refid, st, t, when, poll, reach (sekizlik), delay, offset (ms; NTP işareti), jitter. Havuz satırları (.POOL.)
// kaynak değil, yapılandırma girdisidir.
func applyNtpq(text string, info *TimeSyncInfo) {
	for _, line := range strings.Split(text, "\n") {
		if len(line) < 2 || strings.HasPrefix(line, "=") || strings.Contains(line, "remote") {
			continue
		}
		tally, rest := line[0], line[1:]
		f := strings.Fields(rest)
		if len(f) < 10 {
			continue
		}
		if f[1] == ".POOL." {
			info.ConfiguredServers = append(info.ConfiguredServers, f[0])
			continue
		}
		src := TimeSource{Name: f[0]}
		if r, err := strconv.ParseInt(f[6], 8, 64); err == nil {
			reach := int(r)
			src.Reach = &reach
		}
		offset, e1 := strconv.ParseFloat(f[8], 64)
		if e1 == nil {
			src.OffsetMs = &offset
		}
		switch tally {
		case '*', 'o':
			src.State = "selected"
			info.Server, info.ServerAddress = f[0], f[0]
			if st, err := strconv.Atoi(f[2]); err == nil {
				info.Stratum = &st
			}
			if p, err := strconv.Atoi(f[5]); err == nil {
				info.PollS = &p
			}
			if e1 == nil {
				info.OffsetMs = &offset
			}
			if d, err := strconv.ParseFloat(f[7], 64); err == nil {
				info.DelayMs = &d
			}
			if j, err := strconv.ParseFloat(f[9], 64); err == nil {
				info.JitterMs = &j
			}
		case '+', '-', '#':
			src.State = "candidate"
		case 'x':
			src.State = "falseticker"
		default:
			src.State = "unusable"
		}
		if src.Reach != nil && *src.Reach == 0 {
			src.State = "unreachable"
		}
		info.Sources = append(info.Sources, src)
		info.ConfiguredServers = append(info.ConfiguredServers, src.Name)
	}
}

// ---- yardımcılar -------------------------------------------------------------------------------------------------

func parseKeyValues(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

func yesNo(s string) *bool {
	switch s {
	case "yes":
		v := true
		return &v
	case "no":
		v := false
		return &v
	}
	return nil
}
