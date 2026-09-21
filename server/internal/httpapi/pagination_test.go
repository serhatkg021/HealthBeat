package httpapi_test

import (
	"strconv"
	"testing"

	"healthbeat-server/internal/testdb"
)

// Bu dosya, panelin yeni sayfalanabilir/aranabilir tablolarının çalıştığı ortak ?q=&limit=&offset=
// sözleşmesini (bkz. respond.go: parseListParams) ve X-Total-Count başlığını test eder.
// limit/offset verilmeden çağrılan uçlar zaten integration_test.go'da kapsanıyor (geriye dönük
// uyumluluk: tüm satırlar döner) — burada yalnızca yeni davranış test edilir.

func totalCount(t *testing.T, h interface{ Get(string) string }) int {
	t.Helper()
	v := h.Get("X-Total-Count")
	if v == "" {
		t.Fatal("X-Total-Count header missing")
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("X-Total-Count = %q: %v", v, err)
	}
	return n
}

func TestUsersPaginationAndSearch(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	testdb.User(t, a.pool, "alice@example.com", "operator", password)
	testdb.User(t, a.pool, "bob@example.com", "operator", password)
	testdb.User(t, a.pool, "carol@example.com", "operator", password)
	// + root@x.test = 4 kullanıcı toplam.

	var page []struct{ Email string }
	code, h := a.callHeaders("GET", "/api/v1/users?limit=2&offset=0", root, &page)
	if code != 200 || len(page) != 2 || totalCount(t, h) != 4 {
		t.Fatalf("page1 = %d items, code=%d, total=%d, want 2 items / 4 total", len(page), code, totalCount(t, h))
	}

	var page2 []struct{ Email string }
	code, h = a.callHeaders("GET", "/api/v1/users?limit=2&offset=2", root, &page2)
	if code != 200 || len(page2) != 2 || totalCount(t, h) != 4 {
		t.Fatalf("page2 = %d items, code=%d, total=%d, want 2 items / 4 total", len(page2), code, totalCount(t, h))
	}
	if page[0].Email == page2[0].Email {
		t.Fatalf("page1 and page2 overlap: both start with %q", page[0].Email)
	}

	var found []struct{ Email string }
	code, h = a.callHeaders("GET", "/api/v1/users?q=alice", root, &found)
	if code != 200 || len(found) != 1 || found[0].Email != "alice@example.com" || totalCount(t, h) != 1 {
		t.Fatalf("q=alice -> %+v (total %d), want exactly alice@example.com", found, totalCount(t, h))
	}

	a.expect(400, "GET", "/api/v1/users?limit=0", root, nil, nil)
	a.expect(400, "GET", "/api/v1/users?limit=201", root, nil, nil)
	a.expect(400, "GET", "/api/v1/users?offset=-1", root, nil, nil)
}

func TestOrganizationHostsPaginationAndSearch(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	a.createPushHost(root, org, "web-1")
	a.createPushHost(root, org, "web-2")
	a.createPushHost(root, org, "db-1")

	var page []struct{ Hostname string }
	code, h := a.callHeaders("GET", "/api/v1/organizations/"+org.String()+"/hosts?limit=2&offset=0", root, &page)
	if code != 200 || len(page) != 2 || totalCount(t, h) != 3 {
		t.Fatalf("page1 = %d items, code=%d, total=%d, want 2 items / 3 total", len(page), code, totalCount(t, h))
	}

	var found []struct{ Hostname string }
	code, h = a.callHeaders("GET", "/api/v1/organizations/"+org.String()+"/hosts?q=web", root, &found)
	if code != 200 || len(found) != 2 || totalCount(t, h) != 2 {
		t.Fatalf("q=web -> %+v (total %d), want the two web-* hosts", found, totalCount(t, h))
	}
}

func TestAlertsPaginationAndSearch(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	hostA := a.createPushHost(root, org, "alpha")
	hostB := a.createPushHost(root, org, "beta")

	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 50, "critical_level": 90}, nil)
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "ram", "warning_level": 50, "critical_level": 90}, nil)
	a.expect(204, "POST", "/api/v1/metrics", hostA.APIToken, metrics(95, 95), nil, "X-Host-ID", hostA.ID.String()) // cpu + ram
	a.expect(204, "POST", "/api/v1/metrics", hostB.APIToken, metrics(95, 10), nil, "X-Host-ID", hostB.ID.String()) // yalnızca cpu

	var page []struct{ ID string }
	code, h := a.callHeaders("GET", "/api/v1/alerts?status=open&limit=2&offset=0", root, &page)
	if code != 200 || len(page) != 2 || totalCount(t, h) != 3 {
		t.Fatalf("page1 = %d items, code=%d, total=%d, want 2 items / 3 total", len(page), code, totalCount(t, h))
	}

	// hostname'de arama: yalnızca "alpha" (cpu + ram = 2 alert).
	var byHost []struct{ ID string }
	code, h = a.callHeaders("GET", "/api/v1/alerts?status=open&q=alpha", root, &byHost)
	if code != 200 || len(byHost) != 2 || totalCount(t, h) != 2 {
		t.Fatalf("q=alpha -> %+v (total %d), want alpha'nın 2 alert'i", byHost, totalCount(t, h))
	}

	// metric_type'ta arama: cpu iki host'ta da açık (2 alert).
	var byMetric []struct{ ID string }
	code, h = a.callHeaders("GET", "/api/v1/alerts?status=open&q=cpu", root, &byMetric)
	if code != 200 || len(byMetric) != 2 || totalCount(t, h) != 2 {
		t.Fatalf("q=cpu -> %+v (total %d), want 2", byMetric, totalCount(t, h))
	}

	a.expect(400, "GET", "/api/v1/alerts?limit=0", root, nil, nil)
	a.expect(400, "GET", "/api/v1/alerts?limit=201", root, nil, nil)
}
