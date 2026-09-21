package model

import (
	"regexp"
	"strconv"
	"strings"
)

// LegacyProtocol, sürüm bildirmeyen (başlık göndermeyen) eski agent'ların protokolüdür.
const LegacyProtocol = 1

// AgentInfo, agent'ın her istekte başlıktan bildirdiği sürüm bilgisidir. Sürüm gövdede değil
// başlıkta taşınır: gövdedeki bilinmeyen bir alan, sözleşmeyi henüz bilmeyen eski bir server'da
// 400 üretirdi (bkz. docs/COMPATIBILITY.md).
type AgentInfo struct {
	Version  string // SemVer benzeri; "" = bildirmedi
	Protocol int    // LegacyProtocol (1) = bildirmedi/eski agent
	// UnsupportedFields, bu istekte gelen ama server'ın tanımadığı üst düzey alan adlarıdır
	// (ParseMetricsIngest). Başlıktan değil gövdeden gelir; sürüm bilgisiyle aynı "her istekte
	// yazılır" kuralına uyar: server güncellenip alanı tanıyınca kayıt temizlenir.
	UnsupportedFields []string
}

var agentVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)

const agentUserAgentPrefix = "healthbeat-agent/"

// ParseAgentInfo, agent'ın başlıklarından sürüm bilgisini çıkarır. agentVersion
// X-HealthBeat-Agent-Version (pull yanıtı), userAgent "healthbeat-agent/1.1.0" (push isteği),
// protocol X-HealthBeat-Protocol değeridir. Hiçbiri yoksa ya da geçersizse eski agent sayılır:
// başlıklar istek başına gelen bilgidir, bu yüzden "son bilinen değer" değil her seferinde yazılır.
func ParseAgentInfo(agentVersion, userAgent, protocol string) AgentInfo {
	info := AgentInfo{Protocol: LegacyProtocol}

	v := strings.TrimSpace(agentVersion)
	if v == "" {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(userAgent), agentUserAgentPrefix); ok {
			v, _, _ = strings.Cut(rest, " ")
		}
	}
	if agentVersionPattern.MatchString(v) {
		info.Version = v
	}

	if n, err := strconv.Atoi(strings.TrimSpace(protocol)); err == nil && n >= 1 && n <= 1000 {
		info.Protocol = n
	}
	return info
}
