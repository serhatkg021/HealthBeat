package httpapi_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// GET /hosts/:id/metrics/latest, panelin "Genel" sekmesinin tek ihtiyacı olan en son ham örneği döndürür: tüm aralığı
// indirmeden, /metrics dizisinin son elemanıyla aynı biçimde.
func TestLatestMetricEndpoint(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "o")
	c := a.createPushHost(root, org, "c")
	path := "/api/v1/hosts/" + c.ID.String() + "/metrics/latest"

	// Henüz hiç rapor yok.
	a.expect(204, "GET", path, root, nil, nil)

	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO metrics (host_id, recorded_at, cpu_usage_pct, ram_usage_pct, disk_json) VALUES
		 ($1, now() - interval '3 seconds', 10, 11, '[]'::jsonb),
		 ($1, now() - interval '2 seconds', 20, 21, '[]'::jsonb),
		 ($1, now() - interval '1 second',  97.5, 31, '[{"mount":"/","used_pct":55,"total":100,"free":45}]'::jsonb)`, c.ID); err != nil {
		t.Fatal(err)
	}

	var latest map[string]any
	a.expect(200, "GET", path, root, nil, &latest)
	if latest["cpu_usage_pct"] != 97.5 || latest["ram_usage_pct"] != 31.0 {
		t.Fatalf("latest = %v, want the newest sample (cpu 97.5, ram 31)", latest)
	}
	disks, _ := latest["disk"].([]any)
	if len(disks) != 1 || disks[0].(map[string]any)["mount"] != "/" {
		t.Fatalf("latest disk = %v, want the newest sample's mount list", latest["disk"])
	}

	var points []map[string]any
	a.expect(200, "GET", "/api/v1/hosts/"+c.ID.String()+"/metrics", root, nil, &points)
	if len(points) != 3 || !reflect.DeepEqual(points[len(points)-1], latest) {
		t.Fatalf("latest %v differs from the last /metrics element %v", latest, points[len(points)-1])
	}
}

func TestLatestMetricEndpointAccess(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	opTok, _ := a.login("op@x.test", "operator")
	org := a.createOrg(root, "o")
	c := a.createPushHost(root, org, "c")

	a.expect(403, "GET", "/api/v1/hosts/"+c.ID.String()+"/metrics/latest", opTok, nil, nil) // atanmamış sunucu
	a.expect(404, "GET", "/api/v1/hosts/"+uuid.NewString()+"/metrics/latest", root, nil, nil)
	a.expect(400, "GET", "/api/v1/hosts/not-a-uuid/metrics/latest", root, nil, nil)
	a.expect(401, "GET", "/api/v1/hosts/"+c.ID.String()+"/metrics/latest", "", nil, nil)
}
