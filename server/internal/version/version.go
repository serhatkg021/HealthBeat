// Package version, server'ın sürümünü ve konuştuğu ingest protokolünü tutar
// (bkz. docs/COMPATIBILITY.md).
package version

import "regexp"

// Version server (ve onunla birlikte yayınlanan panel) sürümüdür (SemVer). Elle artırılır; `server/vX.Y.Z`
// etiketiyle yayınlanır. Agent'ın sürümünden BAĞIMSIZDIR (bkz. docs/DISTRIBUTION.md, iki sürüm hattı).
var Version = "1.0.0"

// LatestAgent, bu server derlemesinin bildiği en güncel agent sürümüdür (SemVer): panelin "güncel / güncelleme var"
// sınıflandırmasında LATEST_AGENT_VERSION verilmezse varsayılan olarak kullanılır. Server sürümüyle karıştırma:
// server yeni sürümle çıkınca agent'lar boşuna "güncelleme var" görünmesin diye ayrı tutulur. Yeni bir agent
// yayınlanırken (`agent/vX.Y.Z`) elle güncellenir; scripts/release.sh agent bunu doğrular.
var LatestAgent = "1.0.0"

// Protocol, server'ın anladığı en yüksek ingest protokolüdür (agent'ın version.Protocol'ü ile
// aynı anlam): 1 = sürüm bildirmeyen eski agent'lar, 2 = donanım özeti + sürüm başlıkları, 3 = makine envanteri (host_info) + inode.
const Protocol = 3

// Başlık adları agent ile paylaşılan sözleşmenin parçasıdır.
const (
	HeaderProtocol      = "X-HealthBeat-Protocol"
	HeaderAgentVersion  = "X-HealthBeat-Agent-Version"
	HeaderServerVersion = "X-HealthBeat-Server-Version"
	// HeaderLatestAgent, server'ın önerdiği en güncel agent sürümüdür (ingest yanıtında).
	HeaderLatestAgent = "X-HealthBeat-Latest-Agent"
)

// UserAgent pull isteklerinde kullanılır: healthbeat-server/1.0.0
func UserAgent() string { return "healthbeat-server/" + Version }

var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)

// ValidSemver, s'nin MAJOR.MINOR.PATCH biçiminde (isteğe bağlı -önsürüm/+derleme ekiyle) olup
// olmadığını söyler.
func ValidSemver(s string) bool { return semverPattern.MatchString(s) }
