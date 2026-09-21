// Package version, agent'ın sürümünü ve iletişim protokolü sürümünü tutar. Server bunları her
// istekte başlıktan okuyup panelde "hangi sunucuda hangi agent var" görünümünü besler
// (bkz. docs/COMPATIBILITY.md).
package version

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strconv"
)

// Version agent sürümüdür (SemVer). Elle artırılır; derleme sırasında
// -ldflags "-X healthbeat-agent/internal/version.Version=1.0.1" ile de ezilebilir.
var Version = "1.0.0"

// Protocol, agent'ın konuştuğu ingest protokolünün sürümüdür: "hangi alan kümesini gönderiyorum".
// Uyumluluk kararları sürüm metnine değil buna bakar. Yeni bir alan kümesi eklendiğinde artırılır.
//
//	1  sürüm bildirmeyen eski agent'lar (başlık hiç gönderilmez; server bunu 1 sayar)
//	2  donanım özeti (cpu_cores, ram_total_mb, physical_disks) + sürüm/protokol başlıkları
//	3  makine envanteri (host_info) + disk girdilerinde inodes_used_pct
const Protocol = 3

// Başlık adları server ile paylaşılan sözleşmenin parçasıdır.
const (
	HeaderProtocol     = "X-HealthBeat-Protocol"
	HeaderAgentVersion = "X-HealthBeat-Agent-Version"

	// Server'ın ingest yanıtlarında bildirdiği başlıklar.
	HeaderServerVersion = "X-HealthBeat-Server-Version"
	HeaderLatestAgent   = "X-HealthBeat-Latest-Agent"
)

// UserAgent push isteklerinde kullanılır: healthbeat-agent/1.1.0
func UserAgent() string { return "healthbeat-agent/" + Version }

// Commit, derleme sırasında gömülen VCS revizyonunun kısaltmasını döndürür (yoksa "").
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}

// String, `healthbeat-agent --version` çıktısıdır.
func String() string {
	if c := Commit(); c != "" {
		return fmt.Sprintf("healthbeat-agent %s (protocol %d, commit %s)", Version, Protocol, c)
	}
	return fmt.Sprintf("healthbeat-agent %s (protocol %d)", Version, Protocol)
}

var semverPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

// Compare a < b ise -1, a > b ise 1, eşitse 0 döndürür. Geçersiz bir sürüm karşılaştırılamaz
// (0 döner): bilinmeyen bir sürüm sebepsiz yere "daha eski/yeni" sayılmasın. Önsürüm, aynı
// çekirdeğin sürümünden küçüktür; derleme eki (+...) yok sayılır. Panelin karşılaştırmasıyla
// aynı kuraldır (server/panel/src/pages/agentStatus.ts).
func Compare(a, b string) int {
	ma, mb := semverPattern.FindStringSubmatch(a), semverPattern.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return 0
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	pa, pb := ma[4], mb[4]
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	case pa < pb:
		return -1
	default:
		return 1
	}
}
