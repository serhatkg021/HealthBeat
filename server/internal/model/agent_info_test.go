package model

import (
	"strings"
	"testing"
)

// wantInfo, ParseAgentInfo'nun başlıklardan çıkardığı iki alandır (UnsupportedFields gövdeden gelir).
type wantInfo struct {
	Version  string
	Protocol int
}

func TestParseAgentInfo(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		agentVersion, ua, protocol string
		want                       wantInfo
	}{
		{"push: user-agent + protocol", "", "healthbeat-agent/1.1.0", "2", wantInfo{"1.1.0", 2}},
		{"pull: version header wins", "1.2.3", "", "3", wantInfo{"1.2.3", 3}},
		{"header wins over user-agent", "1.2.3", "healthbeat-agent/9.9.9", "2", wantInfo{"1.2.3", 2}},
		{"user-agent with trailing comment", "", "healthbeat-agent/1.1.0 (linux)", "2", wantInfo{"1.1.0", 2}},
		{"prerelease and build metadata", "1.2.0-rc.1+abc", "", "2", wantInfo{"1.2.0-rc.1+abc", 2}},
		{"legacy agent sends nothing", "", "", "", wantInfo{"", LegacyProtocol}},
		{"legacy agent with a generic user-agent", "", "Go-http-host/2.0", "", wantInfo{"", LegacyProtocol}},
		{"protocol without version", "", "", "2", wantInfo{"", 2}},
		{"garbage protocol is legacy", "1.0.0", "", "abc", wantInfo{"1.0.0", LegacyProtocol}},
		{"zero protocol is legacy", "", "", "0", wantInfo{"", LegacyProtocol}},
		{"negative protocol is legacy", "", "", "-3", wantInfo{"", LegacyProtocol}},
		{"absurd protocol is legacy", "", "", "99999", wantInfo{"", LegacyProtocol}},
		{"hostile version dropped", "1.0; DROP TABLE", "", "2", wantInfo{"", 2}},
		{"control chars dropped", "1.0\x00.0", "", "2", wantInfo{"", 2}},
		{"overlong version dropped", strings.Repeat("1", 65), "", "2", wantInfo{"", 2}},
		{"version at the length limit kept", strings.Repeat("1", 64), "", "2", wantInfo{strings.Repeat("1", 64), 2}},
	} {
		got := ParseAgentInfo(tc.agentVersion, tc.ua, tc.protocol)
		if (wantInfo{got.Version, got.Protocol}) != tc.want || got.UnsupportedFields != nil {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
