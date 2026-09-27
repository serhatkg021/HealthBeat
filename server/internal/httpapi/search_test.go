package httpapi_test

import (
	"encoding/json"
	"net/url"
	"testing"
)

// Aramada % ve _ joker değil, düz karakterdir; eşleşme olmayınca liste null değil [] döner.
func TestSearchTreatsWildcardsLiterally(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	a.createPushHost(root, org, "web_1")
	a.createPushHost(root, org, "webx1")

	hosts := func(q string) []string {
		t.Helper()
		var found []struct{ Title string }
		a.expect(200, "GET", "/api/v1/organizations/"+org.String()+"/hosts?q="+url.QueryEscape(q), root, nil, &found)
		var titles []string
		for _, h := range found {
			titles = append(titles, h.Title)
		}
		return titles
	}
	if got := hosts("web_1"); len(got) != 1 || got[0] != "web_1" {
		t.Errorf("q=web_1 -> %v, want only web_1 (_ is not a wildcard)", got)
	}
	if got := hosts("%"); len(got) != 0 {
		t.Errorf("q=%% -> %v, want nothing (%% is not a wildcard)", got)
	}

	var raw json.RawMessage
	a.expect(200, "GET", "/api/v1/users?q="+url.QueryEscape("_"), root, nil, &raw)
	if string(raw) != "[]" {
		t.Errorf("users q=_ -> %s, want []", raw)
	}
}
