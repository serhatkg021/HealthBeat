package settings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func ptr[T any](v T) *T { return &v }

func newService(t *testing.T) (*Service, *store.Settings, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)
	st := store.NewSettings(pool)
	s, err := New(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return s, st, testdb.User(t, pool, "admin@x.test", "super_admin", "pw")
}

func TestFieldsMatchStoreColumns(t *testing.T) {
	var fields []string
	for _, f := range Fields {
		fields = append(fields, string(f))
	}
	if !slices.Equal(fields, store.SettingsColumns) {
		t.Fatalf("settings.Fields = %v\nstore.SettingsColumns = %v", fields, store.SettingsColumns)
	}
	for _, f := range Fields {
		if _, isInt := intRanges[f]; !isInt && !slices.Contains([]Field{FieldLatestAgentVersion, FieldMinSupportedAgentVersion,
			FieldPanelBaseURL, FieldLogLevel, FieldTimezone}, f) {
			t.Errorf("%s has no validation", f)
		}
	}
}

func TestUpdateWritesAndNotifies(t *testing.T) {
	ctx := context.Background()
	s, st, admin := newService(t)
	if len(s.Changed()) != 0 {
		t.Fatalf("fresh install: changed = %v, want none", s.Changed())
	}

	var calls []string
	s.OnChange(func(old, upd model.AppSettings) {
		calls = append(calls, fmt.Sprintf("%s->%s %v->%v", old.LogLevel, upd.LogLevel, old.AccessTokenTTL, upd.AccessTokenTTL))
	})

	changes, err := s.Update(ctx, &admin, Patch{
		LogLevel:              ptr(" DEBUG "),
		AccessTokenTTLSeconds: ptr(1800),
		PanelBaseURL:          ptr("HTTPS://Panel.Example.com/"),
		MetricsRetentionDays:  ptr(30), // güncel değerle aynı: değişiklik sayılmaz
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Changes{
		FieldLogLevel:              {Old: "info", New: "debug"},
		FieldAccessTokenTTLSeconds: {Old: 900, New: 1800},
		FieldPanelBaseURL:          {Old: "", New: "https://panel.example.com"},
	}
	if fmt.Sprint(changes) != fmt.Sprint(want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	cur := s.Current()
	if cur.LogLevel != "debug" || cur.AccessTokenTTL != 30*time.Minute || cur.PanelBaseURL != "https://panel.example.com" {
		t.Fatalf("current = %+v", cur)
	}
	if db, _ := st.Get(ctx); db.LogLevel != "debug" || db.UpdatedBy == nil || *db.UpdatedBy != admin {
		t.Fatalf("database = %+v", db)
	}
	if !slices.Equal(calls, []string{"info->debug 15m0s->30m0s"}) {
		t.Fatalf("subscriber calls = %v", calls)
	}
	if got := s.Changed(); !slices.Equal(got, []Field{FieldAccessTokenTTLSeconds, FieldPanelBaseURL, FieldLogLevel}) {
		t.Fatalf("changed = %v", got)
	}

	// Aynı değerleri yeniden göndermek hiçbir şey yazmaz ve abonelere haber vermez.
	before, _ := st.Get(ctx)
	if changes, err := s.Update(ctx, &admin, Patch{LogLevel: ptr("debug")}); err != nil || len(changes) != 0 {
		t.Fatalf("no-op update: changes=%v err=%v", changes, err)
	}
	if after, _ := st.Get(ctx); !after.UpdatedAt.Equal(before.UpdatedAt) || len(calls) != 1 {
		t.Fatalf("a no-op update was written (updated_at %v -> %v, %d calls)", before.UpdatedAt, after.UpdatedAt, len(calls))
	}
	if _, err := s.Update(ctx, &admin, Patch{}); !errors.Is(err, ErrNothingToChange) {
		t.Fatalf("empty patch: err=%v", err)
	}

	// Tanımsız sürüm: "" yazılır, NULL olarak saklanır.
	if changes, err := s.Update(ctx, &admin, Patch{LatestAgentVersion: ptr("")}); err != nil ||
		changes[FieldLatestAgentVersion] != (Change{Old: "1.0.0", New: ""}) {
		t.Fatalf("clearing latest: changes=%v err=%v", changes, err)
	}
}

func TestUpdateRejectsInvalidValues(t *testing.T) {
	ctx := context.Background()
	s, st, admin := newService(t)
	if _, err := s.Update(ctx, &admin, Patch{MinSupportedAgentVersion: ptr("1.0.0")}); err != nil {
		t.Fatal(err)
	}
	notified := false
	s.OnChange(func(_, _ model.AppSettings) { notified = true })
	before := s.Current()

	for name, tc := range map[string]struct {
		p     Patch
		field Field
	}{
		"ttl too short":          {Patch{AccessTokenTTLSeconds: ptr(59)}, FieldAccessTokenTTLSeconds},
		"negative retention":     {Patch{MetricsRetentionDays: ptr(-1)}, FieldMetricsRetentionDays},
		"body too large":         {Patch{LogErrorBodyBytes: ptr(1<<20 + 1)}, FieldLogErrorBodyBytes},
		"zero log days":          {Patch{LogFileMaxAgeDays: ptr(0)}, FieldLogFileMaxAgeDays},
		"bad level":              {Patch{LogLevel: ptr("trace")}, FieldLogLevel},
		"bad semver":             {Patch{LatestAgentVersion: ptr("v1.2")}, FieldLatestAgentVersion},
		"bad url":                {Patch{PanelBaseURL: ptr("panel.example.com/x")}, FieldPanelBaseURL},
		"refresh not longer":     {Patch{AccessTokenTTLSeconds: ptr(7200), RefreshTokenTTLSeconds: ptr(3600)}, FieldRefreshTokenTTLSeconds},
		"min above latest":       {Patch{LatestAgentVersion: ptr("1.0.0-rc.1")}, FieldMinSupportedAgentVersion},
		"one bad among good":     {Patch{LogLevel: ptr("debug"), RateLimitIngestPerMinute: ptr(-5)}, FieldRateLimitIngestPerMinute},
		"min above given latest": {Patch{LatestAgentVersion: ptr("1.1.0"), MinSupportedAgentVersion: ptr("1.2.0")}, FieldMinSupportedAgentVersion},
	} {
		_, err := s.Update(ctx, &admin, tc.p)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != tc.field || fe.Message == "" {
			t.Errorf("%s: err=%v, want a FieldError on %s", name, err, tc.field)
		}
	}
	if fmt.Sprint(s.Current()) != fmt.Sprint(before) || notified {
		t.Fatalf("a rejected update changed the settings or notified: %+v", s.Current())
	}
	if db, _ := st.Get(ctx); db.LogLevel != "info" || !db.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("a rejected update reached the database: %+v", db)
	}
}

func TestReset(t *testing.T) {
	ctx := context.Background()
	s, _, admin := newService(t)
	if _, err := s.Update(ctx, &admin, Patch{LogLevel: ptr("warn"), MetricsRetentionDays: ptr(90),
		LatestAgentVersion: ptr("1.3.0"), MinSupportedAgentVersion: ptr("1.2.0")}); err != nil {
		t.Fatal(err)
	}

	changes, err := s.Reset(ctx, &admin, []Field{FieldLogLevel, FieldAuditRetentionDays})
	if err != nil {
		t.Fatal(err)
	}
	// audit_retention_days zaten varsayılanındaydı: değişiklik sayılmaz.
	if fmt.Sprint(changes) != fmt.Sprint(Changes{FieldLogLevel: {Old: "warn", New: "info"}}) {
		t.Fatalf("changes = %v", changes)
	}
	if cur := s.Current(); cur.LogLevel != "info" || cur.MetricsRetentionDays != 90 {
		t.Fatalf("after reset: %+v", cur)
	}

	// En güncel sürümü varsayılana (1.0.0) döndürmek min (1.2.0) ile çelişir: alanlar arası kural sıfırlamada da geçerli.
	var fe *FieldError
	if _, err := s.Reset(ctx, &admin, []Field{FieldLatestAgentVersion}); !errors.As(err, &fe) || fe.Field != FieldMinSupportedAgentVersion {
		t.Fatalf("reset latest below min: err=%v", err)
	}
	if _, err := s.Reset(ctx, &admin, []Field{FieldLatestAgentVersion, FieldMinSupportedAgentVersion}); err != nil {
		t.Fatalf("resetting both: %v", err)
	}
	if _, err := s.Reset(ctx, &admin, []Field{"updated_by"}); !errors.As(err, &fe) || fe.Field != "updated_by" {
		t.Fatalf("unknown field: err=%v", err)
	}
	if _, err := s.Reset(ctx, &admin, nil); !errors.Is(err, ErrNothingToChange) {
		t.Fatalf("empty reset: err=%v", err)
	}
	if got := s.Changed(); !slices.Equal(got, []Field{FieldMetricsRetentionDays}) {
		t.Fatalf("changed = %v", got)
	}
}

// Servisin doğrulaması ile migration'daki CHECK kısıtları aynı kararı vermeli: sınırın hemen içi ve dışı iki katmana da
// verilir. Biri değişip diğeri unutulursa bu test kırılır.
func TestLimitsMatchDatabase(t *testing.T) {
	ctx := context.Background()
	_, st, _ := newService(t)
	check := func(f Field, v any) {
		t.Helper()
		_, svcErr := normalize(map[Field]any{f: v})
		_, _, dbErr := st.Apply(ctx, nil, map[string]any{string(f): v}, nil)
		if (svcErr == nil) != (dbErr == nil) {
			t.Errorf("%s = %v: service err=%v, database err=%v", f, v, svcErr, dbErr)
		}
		if dbErr == nil {
			if _, _, err := st.Apply(ctx, nil, nil, []string{string(f)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for f, r := range intRanges {
		check(f, r.min-1)
		check(f, r.min)
		check(f, r.max)
		if r.max != noMax {
			check(f, r.max+1)
		}
	}
	for _, v := range []string{"debug", "info", "warn", "error", "trace", "warning", ""} {
		check(FieldLogLevel, v)
	}
	for _, v := range []string{"1.0.0", "10.20.30", "1.2.3-rc.1", "1.2.3+build.5", "1.2", "v1.0.0", "1.0.0.0", "1.a.0"} {
		check(FieldLatestAgentVersion, v)
		check(FieldMinSupportedAgentVersion, v)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	s, _, admin := newService(t)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := range 5 {
				if _, err := s.Update(ctx, &admin, Patch{MetricsRetentionDays: ptr(i*10 + j + 1)}); err != nil {
					t.Error(err)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				if s.Current().LogLevel == "" {
					t.Error("read a partial snapshot")
				}
			}
		}()
	}
	wg.Wait()
}

func TestParsePanelBaseURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"https://panel.example.com", "https://panel.example.com"},
		{"  https://Panel.Example.com/  ", "https://panel.example.com"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://panel.example.com:8443///", "https://panel.example.com:8443"},
	} {
		got, err := ParsePanelBaseURL(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParsePanelBaseURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"panel.example.com",           // şema yok
		"ftp://panel.example.com",     // http/https dışı
		"https://panel.example.com/x", // yol
		"https://panel.example.com?a=1",
		"https://panel.example.com/#f",
		"https://user:pw@panel.example.com", // kimlik bilgisi
		"https://",                          // ana makine yok
		"javascript:alert(1)",
	} {
		if got, err := ParsePanelBaseURL(bad); err == nil {
			t.Errorf("ParsePanelBaseURL(%q) = %q, want an error", bad, got)
		}
	}
}

// Saat dilimi: geçersiz ad reddedilir; geçerli ad Location'ı hemen değiştirir; varsayılana dönünce TZ'ye ya da UTC'ye
// düşülür.
func TestTimezoneSetting(t *testing.T) {
	ctx := context.Background()
	t.Setenv("TZ", "")
	s, _, admin := newService(t)
	if got := s.Location(); got != time.UTC {
		t.Fatalf("fresh install: Location = %s, want UTC", got)
	}
	for _, bad := range []string{"Mars/Olympus", "Local", "europe/istanbul"} {
		var fe *FieldError
		if _, err := s.Update(ctx, &admin, Patch{Timezone: &bad}); !errors.As(err, &fe) || fe.Field != FieldTimezone {
			t.Errorf("timezone %q: err = %v, want a timezone field error", bad, err)
		}
	}
	ist := " Europe/Istanbul "
	if _, err := s.Update(ctx, &admin, Patch{Timezone: &ist}); err != nil {
		t.Fatal(err)
	}
	if got := s.Current().Timezone; got != "Europe/Istanbul" {
		t.Errorf("stored timezone = %q, want it trimmed", got)
	}
	if got := s.Location().String(); got != "Europe/Istanbul" {
		t.Errorf("Location = %s", got)
	}
	if !slices.Contains(s.Changed(), FieldTimezone) {
		t.Errorf("changed = %v, want timezone", s.Changed())
	}
	t.Setenv("TZ", "Europe/Berlin")
	if _, err := s.Reset(ctx, &admin, []Field{FieldTimezone}); err != nil {
		t.Fatal(err)
	}
	if got := s.Location().String(); got != "Europe/Berlin" {
		t.Errorf("after reset with TZ set: Location = %s, want Europe/Berlin", got)
	}
}
