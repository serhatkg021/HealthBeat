package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const payloadDir = "../../testdata/payloads"

// Her agent sürümünün gerçek gövdesi (testdata/payloads) bu server tarafından kabul edilmeli:
// yeni bir alan eklerken geriye dönük kırılma burada yakalanır (bkz. docs/COMPATIBILITY.md).
func TestParseMetricsIngestAcceptsEveryGoldenPayload(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(payloadDir, "*.json"))
	if err != nil || len(files) < 3 {
		t.Fatalf("golden payloads not found in %s: %v (%d files)", payloadDir, err, len(files))
	}
	wantUnknown := map[string][]string{
		"v1_legacy.json":    nil,
		"v2_hardware.json":  nil,
		"v3_inventory.json": nil,
		"v99_future.json":   {"agent_notes", "gpu", "load_average"},
	}
	for _, f := range files {
		name := filepath.Base(f)
		data, _ := os.ReadFile(f)
		req, unknown, err := ParseMetricsIngest(data)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err := req.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", name, err)
		}
		if req.CPUUsagePct == 0 || len(req.Disk) == 0 {
			t.Errorf("%s: core fields not parsed: %+v", name, req)
		}
		want, known := wantUnknown[name]
		if !known {
			t.Errorf("%s: add it to wantUnknown", name)
		}
		if !reflect.DeepEqual(unknown, want) {
			t.Errorf("%s: unknown = %v, want %v", name, unknown, want)
		}
	}
}

func TestParseMetricsIngestLegacyHasNoHardware(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join(payloadDir, "v1_legacy.json"))
	req, _, err := ParseMetricsIngest(data)
	if err != nil {
		t.Fatal(err)
	}
	hw := req.Hardware()
	if hw.CPUCores != 0 || hw.RAMTotalMB != 0 || hw.PhysicalDisks != nil {
		t.Errorf("legacy hardware = %+v, want zero value (unknown)", hw)
	}
}

func TestParseMetricsIngestRejectsNonObjectsAndBadCoreTypes(t *testing.T) {
	for name, body := range map[string]string{
		"null":            `null`,
		"array":           `[]`,
		"string":          `"x"`,
		"not json":        `{`,
		"empty":           ``,
		"core wrong type": `{"cpu_usage_pct":"high","ram_usage_pct":1}`,
	} {
		if _, _, err := ParseMetricsIngest([]byte(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestUnknownIngestFieldsAreSortedCappedAndCleaned(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"cpu_usage_pct":1,"ram_usage_pct":2,"disk":[],"docker_containers":[],"zz":1,"a\u0000b":2`)
	for i := 0; i < maxUnknownFields+5; i++ {
		b.WriteString(`,"x` + strings.Repeat("y", i) + `":1`)
	}
	b.WriteString(`,"` + strings.Repeat("n", 200) + `":1}`)
	_, unknown, err := ParseMetricsIngest([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != maxUnknownFields {
		t.Errorf("%d unknown names kept, cap is %d", len(unknown), maxUnknownFields)
	}
	for i, n := range unknown {
		if len(n) > maxUnknownFieldName || strings.ContainsRune(n, 0) {
			t.Errorf("unknown[%d] not cleaned: %q", i, n)
		}
		if i > 0 && unknown[i-1] > n {
			t.Errorf("not sorted: %v", unknown)
		}
	}
}

// Bilinen alan kümesi struct'tan türetilir; yeni alan eklenince "bilinmeyen" sanılmamalı.
func TestKnownIngestFieldsCoverEveryStructField(t *testing.T) {
	for _, f := range []string{"cpu_usage_pct", "ram_usage_pct", "disk", "docker_containers", "cpu_cores", "ram_total_mb", "physical_disks"} {
		if _, ok := knownIngestFields[f]; !ok {
			t.Errorf("%q missing from known fields", f)
		}
	}
	if len(knownIngestFields) != reflect.TypeOf(MetricsIngestRequest{}).NumField() {
		t.Errorf("known fields = %d, struct has %d", len(knownIngestFields), reflect.TypeOf(MetricsIngestRequest{}).NumField())
	}
}
