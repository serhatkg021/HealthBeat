package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const listUnitsSample = `apache2.service                                       not-found inactive dead    apache2.service
apt-daily.service                                     loaded    inactive dead    Daily apt download activities
cron.service                                          loaded    active   running Regular background program processing daemon
postgresql.service                                    loaded    failed   failed  PostgreSQL RDBMS
redis-server.service                                  loaded    activating auto-restart Advanced key-value store
old.service                                           masked    inactive dead    old.service
sys-kernel.mount                                      loaded    active   mounted Kernel Debug File System
`

// fakeServices, sahte bir systemd'dir: list-units ve show çağrılarını yanıtlar ve show çağrılarını kaydeder.
type fakeServices struct {
	list      string
	show      map[string]string // servis -> show bloğu (Id hariç)
	listErr   error
	showErr   error
	showCalls [][]string
}

func (f *fakeServices) run(_ context.Context, name string, args ...string) (string, error) {
	if name != "systemctl" {
		return "", fmt.Errorf("unexpected command %s", name)
	}
	switch args[0] {
	case "list-units":
		return f.list, f.listErr
	case "show":
		names := args[2:]
		f.showCalls = append(f.showCalls, names)
		if f.showErr != nil {
			return "", f.showErr
		}
		var b strings.Builder
		for _, n := range names {
			fmt.Fprintf(&b, "Id=%s\n%s\n\n", n, f.show[n])
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("unexpected systemctl %v", args)
}

func newServiceTestCollector(t *testing.T, f *fakeServices, now time.Time) *ServiceCollector {
	t.Helper()
	root := t.TempDir()
	writeProc(t, root, "run/systemd/system/.keep", "")
	writeProc(t, root, "proc/uptime", "1000.00 2000.00\n") // açılış: now − 1000 sn
	return &ServiceCollector{root: root, now: func() time.Time { return now }, run: f.run}
}

func TestServiceCollector(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	f := &fakeServices{list: listUnitsSample, show: map[string]string{
		"apt-daily.service":    "NRestarts=0\nStateChangeTimestampMonotonic=6069466\nUnitFileState=static",
		"cron.service":         "NRestarts=0\nStateChangeTimestampMonotonic=0\nUnitFileState=enabled",
		"postgresql.service":   "NRestarts=3\nStateChangeTimestampMonotonic=760000000\nUnitFileState=enabled",
		"redis-server.service": "StateChangeTimestampMonotonic=990500000\nUnitFileState=enabled", // eski systemd: NRestarts yok
	}}
	got, err := newServiceTestCollector(t, f, now).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ServiceState{}
	for _, s := range got {
		by[s.Name] = s
	}
	if len(got) != 4 {
		t.Fatalf("got %d services %v; not-found, masked and non-service units must be skipped", len(got), got)
	}

	pg := by["postgresql.service"]
	boot := now.Add(-1000 * time.Second)
	if pg.Active != "failed" || pg.Sub != "failed" || pg.Description != "PostgreSQL RDBMS" || pg.Enabled != "enabled" ||
		pg.Restarts == nil || *pg.Restarts != 3 || !pg.Since.Equal(boot.Add(760*time.Second)) {
		t.Errorf("postgresql = %+v", pg)
	}
	if redis := by["redis-server.service"]; redis.Restarts != nil || redis.Sub != "auto-restart" || redis.Description != "Advanced key-value store" {
		t.Errorf("redis = %+v; restarts must be unknown on a systemd without NRestarts", redis)
	}
	if cron := by["cron.service"]; !cron.Since.IsZero() {
		t.Errorf("cron since = %v; a zero monotonic timestamp means unknown", cron.Since)
	}
	if apt := by["apt-daily.service"]; apt.Enabled != "static" || apt.Since.IsZero() {
		t.Errorf("apt-daily = %+v", apt)
	}
}

func TestServiceCollectorWithoutSystemdReportsNothing(t *testing.T) {
	f := &fakeServices{list: listUnitsSample}
	c := &ServiceCollector{root: t.TempDir(), now: time.Now, run: f.run}
	got, err := c.Collect(context.Background())
	if got != nil || err != nil || f.showCalls != nil {
		t.Fatalf("Collect = %v, %v (show calls %v); want nothing on a machine without systemd", got, err, f.showCalls)
	}
}

func TestServiceCollectorFailures(t *testing.T) {
	now := time.Now()
	f := &fakeServices{listErr: errors.New("timeout")}
	if _, err := newServiceTestCollector(t, f, now).Collect(context.Background()); err == nil {
		t.Fatal("a failing list-units must be an error (the previous good list stays, then goes stale)")
	}

	f = &fakeServices{list: listUnitsSample, showErr: errors.New("timeout")}
	got, err := newServiceTestCollector(t, f, now).Collect(context.Background())
	if err != nil || len(got) != 4 {
		t.Fatalf("Collect = %d services, %v; a failing show must still report the states from list-units", len(got), err)
	}
	for _, s := range got {
		if s.Restarts != nil || !s.Since.IsZero() || s.Enabled != "" {
			t.Errorf("%s has details %+v without a successful show", s.Name, s)
		}
	}
}

func TestServiceCollectorChunksShowCalls(t *testing.T) {
	var b strings.Builder
	for i := 0; i < showChunk*2+5; i++ {
		fmt.Fprintf(&b, "svc%d.service loaded active running Service %d\n", i, i)
	}
	f := &fakeServices{list: b.String(), show: map[string]string{}}
	got, err := newServiceTestCollector(t, f, time.Now()).Collect(context.Background())
	if err != nil || len(got) != showChunk*2+5 {
		t.Fatalf("Collect = %d services, %v", len(got), err)
	}
	if len(f.showCalls) != 3 || len(f.showCalls[0]) != showChunk || len(f.showCalls[2]) != 5 {
		t.Fatalf("show was called with %d chunks; want 3 (%d, %d, 5)", len(f.showCalls), showChunk, showChunk)
	}
}

func TestParseShow(t *testing.T) {
	got := parseShow("Id=a.service\nNRestarts=1\n\nId=b.service\nDescription=x=y\n")
	if len(got) != 2 || got[0]["NRestarts"] != "1" || got[1]["Description"] != "x=y" {
		t.Fatalf("parseShow = %v", got)
	}
}
