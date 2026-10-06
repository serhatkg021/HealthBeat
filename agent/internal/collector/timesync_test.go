package collector

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// Gerçek çıktılar: bu makinedeki systemd-timesyncd (busctl) ve bir container'daki chrony 4.x.
const (
	busctlTimesyncd = `{"type":"s","data":"ntp.ubuntu.com"}
{"type":"(iay)","data":[2,[185,125,190,57]]}
{"type":"t","data":2048000000}
{"type":"(uuuuittayttttbtt)","data":[0,4,4,2,-25,6576,228,[29,88,99,4],1791318523671505,1791318523697338,1791318523697398,1791318523722962,false,12,5476]}
{"type":"as","data":[]}
{"type":"as","data":[]}
{"type":"as","data":["ntp.ubuntu.com"]}
`
	chronyTracking = "0502505A,5.2.80.90,2,1791319167.779099842,-0.001153898,-0.000045042,0.000045042,-5.598,-0.000,597.057,0.010523201,0.003686110,1.2,Normal\n"
	chronySources  = `^,*,5.2.80.90,1,6,17,5,-0.000154070,-0.000199081,0.005298165
^,-,192.36.143.130,1,6,17,5,-0.003275426,-0.003275426,0.033310559
^,x,89.252.135.27,4,6,377,5,0.250000000,0.250000000,0.017418651
^,?,192.0.2.1,0,7,0,4294967295,0.000000000,0.000000000,0.000000000
#,*,PPS,0,4,377,1,0.000000100,0.000000100,0.000001000
`
	ntpqPeers = `     remote           refid      st t when poll reach   delay   offset  jitter
==============================================================================
 0.ubuntu.pool.n .POOL.          16 p    -   64    0    0.000   +0.000   0.000
*192.0.2.10      .GPS.            1 u   34   64  377    0.512   -0.123   0.045
+192.0.2.11      192.0.2.1        2 u   12   64  377    1.002    0.456   0.120
x192.0.2.12      192.0.2.9        2 u   10   64  377    1.100  812.000   3.000
 192.0.2.13      .INIT.          16 u    -   64    0    0.000   +0.000   0.000
`
)

func timeSyncCollector(t *testing.T, daemon string, outputs map[string]string) *TimeSyncCollector {
	t.Helper()
	root := t.TempDir()
	writeProc(t, root, "run/systemd/system/.keep", "")
	return &TimeSyncCollector{root: root, run: func(_ context.Context, name string, args ...string) (string, error) {
		switch {
		case name == "timedatectl":
			return "Timezone=Europe/Istanbul\nLocalRTC=no\nCanNTP=yes\nNTP=yes\nNTPSynchronized=yes\n", nil
		case name == "systemctl":
			var b strings.Builder
			for _, unit := range args[2:] {
				state := "inactive"
				if unit == daemon {
					state = "active"
				}
				b.WriteString("Id=" + unit + "\nActiveState=" + state + "\n\n")
			}
			return b.String(), nil
		}
		key := name + " " + strings.Join(args, " ")
		for prefix, out := range outputs {
			if strings.HasPrefix(key, prefix) {
				return out, nil
			}
		}
		return "", errors.New("command not available")
	}}
}

func near(t *testing.T, got *float64, want float64, what string) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-3 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestTimeSyncFromTimesyncd(t *testing.T) {
	info, err := timeSyncCollector(t, "systemd-timesyncd.service", map[string]string{"busctl": busctlTimesyncd}).Collect(context.Background())
	if err != nil || info == nil {
		t.Fatalf("Collect = %v, %v", info, err)
	}
	if info.Daemon != "timesyncd" || !*info.Enabled || !*info.Synchronized || *info.LocalRTC {
		t.Errorf("basics = %+v", info)
	}
	if info.Server != "ntp.ubuntu.com" || info.ServerAddress != "185.125.190.57" || strings.Join(info.ConfiguredServers, ",") != "ntp.ubuntu.com" {
		t.Errorf("server = %q %q %v", info.Server, info.ServerAddress, info.ConfiguredServers)
	}
	// `timedatectl timesync-status` aynı yanıt için: Offset +134us, Delay 51.397ms, Jitter 5.476ms, Root distance 3.516ms.
	near(t, info.OffsetMs, 0.1345, "offset ms")
	near(t, info.DelayMs, 51.397, "delay ms")
	near(t, info.JitterMs, 5.476, "jitter ms")
	near(t, info.RootDistanceMs, 3.516, "root distance ms")
	if *info.Stratum != 2 || info.Leap != "normal" || *info.PollS != 2048 || *info.Ignored {
		t.Errorf("stratum %d leap %q poll %d ignored %v", *info.Stratum, info.Leap, *info.PollS, *info.Ignored)
	}
	if want := time.UnixMicro(1791318523722962).UTC(); !info.LastSync.Equal(want) {
		t.Errorf("last sync = %v, want %v", info.LastSync, want)
	}
	if len(info.Sources) != 1 || info.Sources[0].State != "selected" || info.Sources[0].Name != "185.125.190.57" {
		t.Errorf("sources = %+v", info.Sources)
	}
}

// timesyncd henüz hiç yanıt almadıysa (ağ yok, sunucuya ulaşılamıyor) adres boştur ve NTP mesajı sıfırdır: ayrıntı
// alanları bilinmiyor kalır, server "ulaşılamıyor" sonucunu buradan çıkarır.
func TestTimeSyncFromTimesyncdWithoutAnswer(t *testing.T) {
	noAnswer := `{"type":"s","data":"ntp.ubuntu.com"}
{"type":"(iay)","data":[0,[]]}
{"type":"t","data":32000000}
{"type":"(uuuuittayttttbtt)","data":[0,0,0,0,0,0,0,[],0,0,0,0,false,0,0]}
{"type":"as","data":["ntp.example.com"]}
{"type":"as","data":[]}
{"type":"as","data":["ntp.ubuntu.com"]}
`
	info, _ := timeSyncCollector(t, "systemd-timesyncd.service", map[string]string{"busctl": noAnswer}).Collect(context.Background())
	if info.ServerAddress != "" || info.OffsetMs != nil || info.Stratum != nil || !info.LastSync.IsZero() || info.Sources != nil {
		t.Errorf("no answer yet = %+v; want no measured fields", info)
	}
	if strings.Join(info.ConfiguredServers, ",") != "ntp.example.com" {
		t.Errorf("configured = %v; a system server hides the fallback", info.ConfiguredServers)
	}
}

func TestTimeSyncFromChrony(t *testing.T) {
	info, _ := timeSyncCollector(t, "chronyd.service", map[string]string{
		"chronyc -n -c tracking": chronyTracking, "chronyc -n -c sources": chronySources,
	}).Collect(context.Background())
	if info.Daemon != "chrony" || info.Server != "5.2.80.90" || info.Leap != "normal" {
		t.Errorf("chrony = %+v", info)
	}
	near(t, info.OffsetMs, -1.153898, "offset ms (local clock ahead: negative)")
	near(t, info.RootDistanceMs, 0.010523201/2*1000+3.68611, "root distance ms")
	if *info.Stratum != 1 {
		t.Errorf("stratum = %d; want the selected source's stratum, not the machine's own", *info.Stratum)
	}
	if *info.PollS != 1 || info.LastSync.Year() != 2026 {
		t.Errorf("poll %d, last sync %v", *info.PollS, info.LastSync)
	}
	states := map[string]string{}
	for _, s := range info.Sources {
		states[s.Name] = s.State
	}
	want := map[string]string{"5.2.80.90": "selected", "192.36.143.130": "candidate", "89.252.135.27": "falseticker", "192.0.2.1": "unreachable"}
	for name, st := range want {
		if states[name] != st {
			t.Errorf("source %s = %q, want %q", name, states[name], st)
		}
	}
	if _, ok := states["PPS"]; ok {
		t.Error("a local reference clock is not an NTP source")
	}
	for _, s := range info.Sources {
		if s.Name == "5.2.80.90" {
			if *s.Reach != 15 {
				t.Errorf("reach = %d; chrony prints it in octal (17 = 15)", *s.Reach)
			}
			near(t, s.OffsetMs, 0.15407, "source offset (chrony's sign flipped to NTP's)")
		}
	}
}

func TestTimeSyncFromNtpd(t *testing.T) {
	info, _ := timeSyncCollector(t, "ntpsec.service", map[string]string{"ntpq -pn": ntpqPeers}).Collect(context.Background())
	if info.Daemon != "ntpd" || info.Server != "192.0.2.10" || *info.Stratum != 1 || *info.PollS != 64 {
		t.Errorf("ntpd = %+v", info)
	}
	near(t, info.OffsetMs, -0.123, "offset ms")
	near(t, info.DelayMs, 0.512, "delay ms")
	near(t, info.JitterMs, 0.045, "jitter ms")
	states := map[string]string{}
	for _, s := range info.Sources {
		states[s.Name] = s.State
	}
	if states["192.0.2.11"] != "candidate" || states["192.0.2.12"] != "falseticker" || states["192.0.2.13"] != "unreachable" || len(states) != 4 {
		t.Errorf("sources = %v; the pool line is configuration, not a source", states)
	}
	if !strings.Contains(strings.Join(info.ConfiguredServers, ","), "0.ubuntu.pool.n") {
		t.Errorf("configured = %v", info.ConfiguredServers)
	}
}

func TestTimeSyncWithoutDaemonOrSystemd(t *testing.T) {
	info, _ := timeSyncCollector(t, "", nil).Collect(context.Background())
	if info.Daemon != "none" || info.Synchronized == nil || info.Sources != nil {
		t.Errorf("no daemon = %+v; want daemon none with the timedatectl basics", info)
	}
	c := &TimeSyncCollector{root: t.TempDir(), run: func(context.Context, string, ...string) (string, error) {
		t.Error("no command may run without systemd")
		return "", nil
	}}
	if info, err := c.Collect(context.Background()); info != nil || err != nil {
		t.Fatalf("no systemd = %v, %v; want unknown", info, err)
	}
	// Komut çalışmazsa (eski systemd'de busctl --json yok) temel alanlar kalır.
	info, _ = timeSyncCollector(t, "systemd-timesyncd.service", nil).Collect(context.Background())
	if info.Daemon != "timesyncd" || info.OffsetMs != nil || info.Synchronized == nil {
		t.Errorf("busctl unavailable = %+v", info)
	}
}
