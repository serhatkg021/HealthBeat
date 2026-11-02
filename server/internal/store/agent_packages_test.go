package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func agentPackage(ver, filename, arch, format string, by *uuid.UUID) model.AgentPackage {
	return model.AgentPackage{Version: ver, Filename: filename, Arch: arch, Format: format, SizeBytes: 3729808,
		SHA256: strings.Repeat("ab", 32), UploadedBy: by}
}

func TestAgentPackageCreateListDelete(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewAgentPackages(pool)
	admin := testdb.User(t, pool, "admin@example.com", model.RoleSuperAdmin, "Password-123")

	placed := 0
	place := func() error { placed++; return nil }
	for _, p := range []model.AgentPackage{
		agentPackage("1.1.0", "healthbeat-agent_1.1.0_amd64.deb", "amd64", "deb", &admin),
		agentPackage("1.10.0", "healthbeat-agent-1.10.0-1.x86_64.rpm", "amd64", "rpm", nil),
		agentPackage("1.1.0", "healthbeat-agent-1.1.0-1.aarch64.rpm", "arm64", "rpm", &admin),
		agentPackage("1.2.0-rc.1", "healthbeat-agent_1.2.0~rc.1_amd64.deb", "amd64", "deb", &admin),
	} {
		if _, err := s.Create(ctx, p, place); err != nil {
			t.Fatalf("create %s: %v", p.Filename, err)
		}
	}
	if placed != 4 {
		t.Fatalf("place called %d times, want 4", placed)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range list {
		got = append(got, p.Filename)
	}
	want := []string{
		"healthbeat-agent-1.10.0-1.x86_64.rpm",  // SemVer sırası: 1.10.0 > 1.2.0-rc.1 > 1.1.0
		"healthbeat-agent_1.2.0~rc.1_amd64.deb", //
		"healthbeat-agent-1.1.0-1.aarch64.rpm",  // aynı sürümde dosya adına göre
		"healthbeat-agent_1.1.0_amd64.deb",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("list order = %v, want %v", got, want)
	}
	first := list[3]
	if first.UploadedByEmail != "admin@example.com" || first.DownloadCount != 0 || first.LastDownloadedAt != nil ||
		first.SizeBytes != 3729808 || first.UploadedAt.IsZero() {
		t.Fatalf("package = %+v", first)
	}
	if list[0].UploadedBy != nil || list[0].UploadedByEmail != "" {
		t.Fatalf("package without uploader = %+v", list[0])
	}

	byFile, err := s.GetFile(ctx, "1.1.0", "healthbeat-agent_1.1.0_amd64.deb")
	if err != nil || byFile.ID != first.ID {
		t.Fatalf("GetFile = %+v, %v", byFile, err)
	}
	if _, err := s.GetFile(ctx, "1.2.0", "healthbeat-agent_1.1.0_amd64.deb"); !errors.Is(err, store.ErrAgentPackageMissing) {
		t.Fatalf("GetFile with the wrong version: %v, want ErrAgentPackageMissing", err)
	}

	removed := ""
	deleted, err := s.Delete(ctx, first.ID, func(p model.AgentPackage) error { removed = p.Filename; return nil })
	if err != nil || deleted.ID != first.ID || removed != first.Filename {
		t.Fatalf("delete = %+v, removed %q, %v", deleted, removed, err)
	}
	if _, err := s.Get(ctx, first.ID); !errors.Is(err, store.ErrAgentPackageMissing) {
		t.Fatalf("get after delete: %v", err)
	}
	if _, err := s.Delete(ctx, first.ID, func(model.AgentPackage) error { return nil }); !errors.Is(err, store.ErrAgentPackageMissing) {
		t.Fatalf("delete again: %v", err)
	}

	// Silinen kullanıcı yükleyen olarak boşalır; paket kalır.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, admin); err != nil {
		t.Fatal(err)
	}
	rc, err := s.GetFile(ctx, "1.2.0-rc.1", "healthbeat-agent_1.2.0~rc.1_amd64.deb")
	if err != nil || rc.UploadedBy != nil {
		t.Fatalf("package after its uploader was deleted = %+v, %v", rc, err)
	}
}

func TestAgentPackageCreateRollsBackOnFailure(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewAgentPackages(pool)
	p := agentPackage("1.1.0", "healthbeat-agent_1.1.0_amd64.deb", "amd64", "deb", nil)

	boom := errors.New("disk dolu")
	if _, err := s.Create(ctx, p, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("create with a failing place: %v", err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatalf("record left after a failed place: %+v", list)
	}

	if _, err := s.Create(ctx, p, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := s.Create(ctx, p, func() error { called = true; return nil }); !errors.Is(err, store.ErrAgentPackageExists) {
		t.Fatalf("duplicate filename: %v, want ErrAgentPackageExists", err)
	}
	if called {
		t.Fatal("place was called for a duplicate filename")
	}

	bad := agentPackage("1.1", "healthbeat-agent_1.1_amd64.deb", "amd64", "deb", nil)
	if _, err := s.Create(ctx, bad, func() error { return nil }); !errors.Is(err, store.ErrAgentPackageInvalid) {
		t.Fatalf("invalid version: %v, want ErrAgentPackageInvalid", err)
	}

	list, _ := s.List(ctx)
	if _, err := s.Delete(ctx, list[0].ID, func(model.AgentPackage) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("delete with a failing remove: %v", err)
	}
	if _, err := s.Get(ctx, list[0].ID); err != nil {
		t.Fatalf("record gone after a failed remove: %v", err)
	}
}

func TestAgentPackageDownloads(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewAgentPackages(pool)
	p, err := s.Create(ctx, agentPackage("1.1.0", "healthbeat-agent_1.1.0_amd64.deb", "amd64", "deb", nil), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("ç", 200) // 400 bayt: kısaltılırken UTF-8 bozulmamalı
	for _, ip := range []string{"10.0.0.5", "10.0.0.6", "10.0.0.5"} {
		if err := s.RecordDownload(ctx, p.ID, ip, long); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DownloadCount != 3 || got.LastDownloadedAt == nil {
		t.Fatalf("after 3 downloads: count=%d last=%v", got.DownloadCount, got.LastDownloadedAt)
	}

	recent, err := s.Downloads(ctx, p.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].ClientIP != "10.0.0.5" || recent[1].ClientIP != "10.0.0.6" {
		t.Fatalf("recent downloads = %+v, want the last two, newest first", recent)
	}
	if len(recent[0].UserAgent) > 256 || !strings.HasPrefix(recent[0].UserAgent, "ç") || strings.ContainsRune(recent[0].UserAgent, '�') {
		t.Fatalf("user agent stored as %d bytes: %q", len(recent[0].UserAgent), recent[0].UserAgent)
	}

	if err := s.RecordDownload(ctx, uuid.New(), "10.0.0.7", "curl/8"); !errors.Is(err, store.ErrAgentPackageMissing) {
		t.Fatalf("download of a missing package: %v", err)
	}

	// Saklama: eski satırlar silinir, sayaç kalır.
	if _, err := pool.Exec(ctx, `UPDATE agent_package_downloads SET downloaded_at = now() - interval '40 days'
		WHERE id = (SELECT min(id) FROM agent_package_downloads)`); err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeDownloadsOlderThan(ctx, time.Now().Add(-30*24*time.Hour), 1)
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
	if all, _ := s.Downloads(ctx, p.ID, 10); len(all) != 2 {
		t.Fatalf("downloads after purge = %d, want 2", len(all))
	}
	if got, _ := s.Get(ctx, p.ID); got.DownloadCount != 3 {
		t.Fatalf("count after purge = %d, want 3 (unchanged)", got.DownloadCount)
	}

	// Paket silinince günlüğü de silinir.
	if _, err := s.Delete(ctx, p.ID, func(model.AgentPackage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_package_downloads`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("downloads left after deleting the package: %d, %v", left, err)
	}
}
