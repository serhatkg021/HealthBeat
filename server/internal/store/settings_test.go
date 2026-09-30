package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestSettingsGetApplyReset(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	s := store.NewSettings(pool)
	admin := testdb.User(t, pool, "admin@x.test", "super_admin", "pw")

	got, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestAgentVersion != "1.0.0" || got.MinSupportedAgentVersion != "" || got.AccessTokenTTL != 15*time.Minute ||
		got.RefreshTokenTTL != 7*24*time.Hour || got.MetricsRetentionDays != 30 || got.LogLevel != "info" || got.UpdatedBy != nil {
		t.Fatalf("initial settings = %+v, want the migration defaults", got)
	}

	// Kısmi yazma: yalnızca verilen sütunlar değişir; önceki ve sonraki hâl döner.
	old, upd, err := s.Apply(ctx, &admin, map[string]any{
		"min_supported_agent_version": "1.0.0", "access_token_ttl_seconds": 1800, "log_level": "debug",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if old.LogLevel != "info" || upd.LogLevel != "debug" || upd.MinSupportedAgentVersion != "1.0.0" ||
		upd.AccessTokenTTL != 30*time.Minute || upd.MetricsRetentionDays != 30 || upd.UpdatedBy == nil || *upd.UpdatedBy != admin {
		t.Fatalf("apply: old=%+v updated=%+v", old, upd)
	}
	if again, _ := s.Get(ctx); again.LogLevel != "debug" || !again.UpdatedAt.After(got.UpdatedAt) {
		t.Fatalf("after apply: %+v", again)
	}

	// Tanımsız sürüm NULL olarak yazılır.
	if _, upd, err = s.Apply(ctx, &admin, map[string]any{"latest_agent_version": nil}, nil); err != nil || upd.LatestAgentVersion != "" {
		t.Fatalf("clearing latest_agent_version: %+v err=%v", upd, err)
	}

	// Varsayılanlar veritabanından okunur ve satırı değiştirmez.
	def, err := s.Defaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if def.LogLevel != "info" || def.AccessTokenTTL != 15*time.Minute || def.LatestAgentVersion != "1.0.0" {
		t.Fatalf("defaults = %+v", def)
	}
	if cur, _ := s.Get(ctx); cur.LogLevel != "debug" || cur.LatestAgentVersion != "" {
		t.Fatalf("Defaults changed the row: %+v", cur)
	}

	// Sıfırlama yalnızca seçilen sütunları varsayılana döndürür; yazma ve sıfırlama birlikte yapılabilir.
	if _, upd, err = s.Apply(ctx, nil, map[string]any{"metrics_retention_days": 7},
		[]string{"log_level", "latest_agent_version"}); err != nil {
		t.Fatal(err)
	}
	if upd.LogLevel != "info" || upd.LatestAgentVersion != "1.0.0" || upd.AccessTokenTTL != 30*time.Minute ||
		upd.MetricsRetentionDays != 7 || upd.UpdatedBy != nil {
		t.Fatalf("after reset: %+v", upd)
	}

	// CHECK ihlali ErrSettingsInvalid olur, satır değişmez.
	if _, _, err := s.Apply(ctx, nil, map[string]any{"metrics_retention_days": 1, "log_level": "trace"}, nil); !errors.Is(err, store.ErrSettingsInvalid) {
		t.Fatalf("invalid value: err=%v, want ErrSettingsInvalid", err)
	}
	if cur, _ := s.Get(ctx); cur.MetricsRetentionDays != 7 {
		t.Fatalf("a rejected change was partly written: %+v", cur)
	}

	// Bilinmeyen sütun ya da boş değişiklik reddedilir (SQL'e sütun adı olarak girmez).
	for name, call := range map[string]func() error{
		"unknown set":   func() error { _, _, err := s.Apply(ctx, nil, map[string]any{"id = 2; --": 1}, nil); return err },
		"unknown reset": func() error { _, _, err := s.Apply(ctx, nil, nil, []string{"updated_by"}); return err },
		"empty":         func() error { _, _, err := s.Apply(ctx, nil, nil, nil); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
