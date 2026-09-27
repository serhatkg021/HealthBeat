package httpapi_test

import "testing"

// Veritabanı ayaktayken /readyz 200 döner ve kimlik doğrulama istemez.
func TestReadyzWithTheDatabaseUp(t *testing.T) {
	a := newAPI(t)
	var body struct{ Status string }
	a.expect(200, "GET", "/readyz", "", nil, &body)
	if body.Status != "ok" {
		t.Fatalf("status = %q, want ok", body.Status)
	}
}
