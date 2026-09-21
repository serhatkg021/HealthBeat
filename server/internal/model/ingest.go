package model

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
)

// Ingest sözleşmesi (bkz. docs/COMPATIBILITY.md): server, agent'ın gönderdiği gövdeyi
// "liberal" kabul eder. Bilinmeyen alanlar hata değildir — yeni bir agent sürümü, henüz bu
// alanı tanımayan bir server'a metrik gönderebilmeli; aksi halde tek bir yeni alan o agent'ın
// TÜM metriklerini (CPU/RAM/disk/Docker) düşürürdü.

// maxUnknownFields, tespit edilen bilinmeyen alan adlarının kaydedilecek üst sınırıdır.
const (
	maxUnknownFields    = 16
	maxUnknownFieldName = 64
)

// ParseMetricsIngest, bir metrik raporunu ayrıştırır. Bilinmeyen üst düzey alanlar hata
// vermez; adları (sıralı, sınırlı ve temizlenmiş) ikinci değer olarak döner ki panel "bu agent
// server'ın tanımadığı alanlar gönderiyor" diyebilsin. Yalnızca çekirdek alanların tipi bozuksa
// ya da gövde bir JSON nesnesi değilse hata döner.
func ParseMetricsIngest(data []byte) (MetricsIngestRequest, []string, error) {
	var req MetricsIngestRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return MetricsIngestRequest{}, nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		// "null" struct'a sessizce sıfır değerler yazardı: sahte bir %0 CPU/RAM raporu olurdu.
		return MetricsIngestRequest{}, nil, errors.New("gövde bir JSON nesnesi olmalı")
	}
	sanitizeDiskInodes(req.Disk)
	return req, unknownIngestFields(raw), nil
}

// sanitizeDiskInodes, aralık dışı ya da sayı olmayan inode yüzdesini atar (bilgi alanıdır: bozuk
// bir değer tüm raporu reddettirmemeli, yalnızca "bilinmiyor" sayılır).
func sanitizeDiskInodes(disks []DiskUsage) {
	for i := range disks {
		if p := disks[i].InodesUsedPct; p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 100) {
			disks[i].InodesUsedPct = nil
		}
	}
}

var knownIngestFields = jsonFieldNames(reflect.TypeOf(MetricsIngestRequest{}))

// jsonFieldNames, bir struct'ın json etiketli alan adlarını döndürür; bilinen alan kümesi
// struct'tan türetildiği için yeni bir alan eklendiğinde elle güncellenmesi gerekmez.
func jsonFieldNames(t reflect.Type) map[string]struct{} {
	names := map[string]struct{}{}
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" {
			name = t.Field(i).Name
		}
		if name != "-" {
			names[name] = struct{}{}
		}
	}
	return names
}

func unknownIngestFields(raw map[string]json.RawMessage) []string {
	var out []string
	for name := range raw {
		if _, known := knownIngestFields[name]; known {
			continue
		}
		if name = cleanText(name, maxUnknownFieldName); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	if len(out) > maxUnknownFields {
		out = out[:maxUnknownFields]
	}
	return out
}
