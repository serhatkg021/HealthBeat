package httpapi_test

import (
	"context"
	"testing"

	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/testdb"
)

// auditIP, verilen eylemin denetim kaydındaki IP'yi döndürür.
func (a *api) auditIP(action string) string {
	a.t.Helper()
	var ip string
	if err := a.pool.QueryRow(context.Background(), `SELECT host(ip) FROM audit_logs WHERE action = $1`, action).Scan(&ip); err != nil {
		a.t.Fatal(err)
	}
	return ip
}

func (a *api) trustProxies(raw string) {
	a.t.Helper()
	p, err := clientip.ParseProxies(raw)
	if err != nil {
		a.t.Fatal(err)
	}
	a.deps.SetClientIPResolver(clientip.New(p))
}

// httptest.NewRequest'in TCP eşi 192.0.2.1'dir; testler onu panelin proxy'si gibi kullanır.
const testPeer = "192.0.2.1"

func TestAuditRecordsClientIPBehindTrustedProxy(t *testing.T) {
	a := newAPI(t)
	a.trustProxies(testPeer)
	testdb.User(t, a.pool, "ali@x.test", "operator", password)

	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "ali@x.test", "password": password}, nil,
		"X-Forwarded-For", "85.1.1.1")
	if ip := a.auditIP("auth.login"); ip != "85.1.1.1" {
		t.Fatalf("audit ip = %s, want the client address from X-Forwarded-For", ip)
	}
}

func TestAuditIgnoresForwardedForFromUntrustedPeer(t *testing.T) {
	a := newAPI(t) // TRUSTED_PROXIES yok
	testdb.User(t, a.pool, "ali@x.test", "operator", password)

	a.expect(200, "POST", "/api/v1/auth/login", "", map[string]string{"email": "ali@x.test", "password": password}, nil,
		"X-Forwarded-For", "85.1.1.1")
	if ip := a.auditIP("auth.login"); ip != testPeer {
		t.Fatalf("audit ip = %s, want the TCP peer %s: the header is not trusted", ip, testPeer)
	}
}
