// Package version, server'ın sürümünü ve konuştuğu ingest protokolünü tutar
// (bkz. docs/COMPATIBILITY.md).
package version

import (
	"regexp"
	"strconv"
	"strings"
)

// Version server (ve onunla birlikte yayınlanan panel) sürümüdür (SemVer). Elle artırılır; `server/vX.Y.Z`
// etiketiyle yayınlanır. Agent'ın sürümünden BAĞIMSIZDIR (bkz. docs/DISTRIBUTION.md, iki sürüm hattı).
var Version = "1.1.0"

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

// Compare, iki SemVer sürümünü öncelik sırasına göre karşılaştırır: a < b ise -1, eşitse 0, a > b ise 1. Önsürüm
// (1.2.0-rc.1) aynı sürümün kendisinden öncedir; derleme eki (+…) yok sayılır. Geçersiz sürümler için sonuç
// tanımsızdır; çağıran önce ValidSemver ile doğrular.
func Compare(a, b string) int {
	coreA, preA := splitSemver(a)
	coreB, preB := splitSemver(b)
	for i := range 3 {
		if c := compareNumeric(coreA[i], coreB[i]); c != 0 {
			return c
		}
	}
	switch {
	case preA == "" && preB == "":
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	idsA, idsB := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < len(idsA) && i < len(idsB); i++ {
		if c := comparePrerelease(idsA[i], idsB[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(idsA), len(idsB))
}

// splitSemver, sürümü MAJOR/MINOR/PATCH ve önsürüm ekine ayırır (derleme eki atılır).
func splitSemver(v string) (core [3]string, pre string) {
	v, _, _ = strings.Cut(v, "+")
	v, pre, _ = strings.Cut(v, "-")
	parts := strings.SplitN(v, ".", 3)
	copy(core[:], parts)
	return core, pre
}

// comparePrerelease, SemVer 2.0.0 §11'e göre iki önsürüm tanımlayıcısını karşılaştırır: sayısal olanlar sayı olarak,
// diğerleri metin olarak; sayısal tanımlayıcı her zaman metin olandan öncedir.
func comparePrerelease(a, b string) int {
	_, errA := strconv.ParseUint(a, 10, 64)
	_, errB := strconv.ParseUint(b, 10, 64)
	switch {
	case errA == nil && errB == nil:
		return compareNumeric(a, b)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// compareNumeric, iki ondalık sayı metnini (baştaki sıfırlar yok sayılarak) taşma olmadan karşılaştırır.
func compareNumeric(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if c := compareInt(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
