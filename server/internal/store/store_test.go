package store_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func ptr[T any](v T) *T { return &v }

func TestThresholdResolvePrecedence(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)

	orgA := testdb.Org(t, pool, "A")
	orgB := testdb.Org(t, pool, "B")
	c1 := testdb.PushHost(t, pool, orgA, "c1", "h")
	c2 := testdb.PushHost(t, pool, orgA, "c2", "h")
	cB := testdb.PushHost(t, pool, orgB, "cb", "h")

	resolve := func(host, org uuid.UUID, metric string) (float64, bool) {
		t.Helper()
		got, found, err := th.Resolve(ctx, host, org, metric)
		if err != nil {
			t.Fatal(err)
		}
		return got.WarningLevel, found
	}

	if _, found := resolve(c1, orgA, "cpu"); found {
		t.Fatal("found a threshold when none is configured")
	}

	testdb.Threshold(t, pool, nil, nil, "cpu", 80, 95) // global
	if w, found := resolve(c1, orgA, "cpu"); !found || w != 80 {
		t.Fatalf("global fallback: warning=%v found=%v, want 80/true", w, found)
	}

	testdb.Threshold(t, pool, &orgA, nil, "cpu", 70, 90) // org A geçersiz kılması
	if w, _ := resolve(c1, orgA, "cpu"); w != 70 {
		t.Fatalf("org override: warning=%v, want 70", w)
	}
	if w, _ := resolve(cB, orgB, "cpu"); w != 80 {
		t.Fatalf("other org must keep global: warning=%v, want 80", w)
	}

	testdb.Threshold(t, pool, nil, &c1, "cpu", 60, 85) // host geçersiz kılması
	if w, _ := resolve(c1, orgA, "cpu"); w != 60 {
		t.Fatalf("host override: warning=%v, want 60", w)
	}
	if w, _ := resolve(c2, orgA, "cpu"); w != 70 {
		t.Fatalf("sibling host must use org override: warning=%v, want 70", w)
	}

	// Geçersiz kılmalar metrik türü başınadır.
	if _, found := resolve(c1, orgA, "ram"); found {
		t.Fatal("cpu thresholds leaked into ram")
	}
}

func TestThresholdCreateConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	org := testdb.Org(t, pool, "A")

	create := func(o *uuid.UUID, warn, crit float64) error {
		_, err := th.Create(ctx, store.CreateThresholdParams{
			OrganizationID: o, MetricType: "cpu", WarningLevel: warn, CriticalLevel: crit,
		})
		return err
	}

	if err := create(nil, 80, 95); err != nil {
		t.Fatalf("valid global create: %v", err)
	}
	if err := create(nil, 70, 90); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate global: err=%v, want ErrConflict", err)
	}
	if err := create(&org, 95, 80); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("warning > critical: err=%v, want ErrConflict", err)
	}
	missing := uuid.New()
	if err := create(&missing, 70, 90); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown organization: err=%v, want ErrNotFound", err)
	}
}

func TestAlertLifecycleAndDedupLookup(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")
	ackBy := testdb.User(t, pool, "acker@x.test", "super_admin", "pw")

	if _, err := alerts.GetOpen(ctx, host, "cpu"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetOpen with none: err=%v, want ErrNotFound", err)
	}

	a, err := alerts.Create(ctx, host, "cpu", "warning")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != model.AlertStatusOpen {
		t.Fatalf("new alert status = %q", a.Status)
	}
	if got, err := alerts.GetOpen(ctx, host, "cpu"); err != nil || got.ID != a.ID {
		t.Fatalf("GetOpen: %v %v", got.ID, err)
	}
	if _, err := alerts.GetOpen(ctx, host, "ram"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("GetOpen must be per metric type")
	}

	acked, err := alerts.Acknowledge(ctx, a.ID, ackBy)
	if err != nil || acked.Status != model.AlertStatusAcknowledged || acked.AcknowledgedAt == nil {
		t.Fatalf("Acknowledge: %+v err=%v", acked, err)
	}
	if acked.AcknowledgedBy == nil || *acked.AcknowledgedBy != ackBy {
		t.Fatalf("AcknowledgedBy = %v, want %v", acked.AcknowledgedBy, ackBy)
	}
	if _, err := alerts.Acknowledge(ctx, a.ID, ackBy); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second Acknowledge: err=%v, want ErrNotFound (not open)", err)
	}
	// Onaylanmış bir alert artık "açık" değildir: tekrar bildirimi önleme araması onu yok sayar.
	if _, err := alerts.GetOpen(ctx, host, "cpu"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("acknowledged alert still returned by GetOpen")
	}

	b, _ := alerts.Create(ctx, host, "ram", "critical")
	resolved, err := alerts.Resolve(ctx, b.ID, nil, nil)
	if err != nil || resolved.Status != model.AlertStatusResolved || resolved.ResolvedAt == nil {
		t.Fatalf("Resolve: %+v err=%v", resolved, err)
	}
	if _, err := alerts.Acknowledge(ctx, b.ID, ackBy); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("resolved alert could be acknowledged")
	}
}

// Regresyon: eşik-tabanlı bir çözülmede value/threshold verilmezse alert, seviyesinin son
// yükseltildiği andaki (hâlâ eşik üstü) eski okumayla kalırdı — bildirim e-postasında "eşiğin
// altına döndü" derken değeri hâlâ eşiğin üstünde gösterirdi. Resolve artık verilen okumayla
// bu kolonları da günceller.
func TestResolveUpdatesValueAndThresholdToTheResolvingReading(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "h", "h")

	stale, staleThreshold := 64.9, 60.0
	a, _, err := alerts.CreateIfNoneOpen(ctx, host, "ram", "", "critical", &stale, &staleThreshold)
	if err != nil {
		t.Fatal(err)
	}
	resolving, warningThreshold := 38.2, 40.0
	resolved, err := alerts.Resolve(ctx, a.ID, &resolving, &warningThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Value == nil || *resolved.Value != resolving || resolved.Threshold == nil || *resolved.Threshold != warningThreshold {
		t.Fatalf("resolved = %+v, want value=%v threshold=%v", resolved, resolving, warningThreshold)
	}

	// nil verilirse (sayısal bir okuması olmayan çözülme yolları) eski değer olduğu gibi kalır.
	b, _, err := alerts.CreateIfNoneOpen(ctx, host, "cpu", "", "warning", &stale, &staleThreshold)
	if err != nil {
		t.Fatal(err)
	}
	resolvedNoReading, err := alerts.Resolve(ctx, b.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedNoReading.Value == nil || *resolvedNoReading.Value != stale {
		t.Fatalf("resolved without a reading = %+v, want the value unchanged (%v)", resolvedNoReading, stale)
	}
}

func TestAlertListScoping(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)

	org := testdb.Org(t, pool, "A")
	c1 := testdb.PushHost(t, pool, org, "c1", "h")
	c2 := testdb.PushHost(t, pool, org, "c2", "h")
	alerts.Create(ctx, c1, "cpu", "critical")
	alerts.Create(ctx, c1, "ram", "warning")
	alerts.Create(ctx, c2, "cpu", "warning")

	all, total, _ := alerts.List(ctx, "open", store.ListParams{})
	if len(all) != 3 || total != 3 {
		t.Fatalf("List(open) = %d (total %d), want 3", len(all), total)
	}
	scoped, total, err := alerts.ListForHosts(ctx, "open", []uuid.UUID{c1}, store.ListParams{})
	if err != nil || len(scoped) != 2 || total != 2 {
		t.Fatalf("ListForHosts(c1) = %d (total %d) err=%v, want 2", len(scoped), total, err)
	}
	if none, _, _ := alerts.ListForHosts(ctx, "open", []uuid.UUID{}, store.ListParams{}); len(none) != 0 {
		t.Fatalf("empty scope returned %d alerts, want 0", len(none))
	}

	crit, warn, err := alerts.CountOpenByLevel(ctx, nil)
	if err != nil || crit != 1 || warn != 2 {
		t.Fatalf("CountOpenByLevel(nil) = %d/%d err=%v, want 1/2", crit, warn, err)
	}
	crit, warn, _ = alerts.CountOpenByLevel(ctx, []uuid.UUID{c2})
	if crit != 0 || warn != 1 {
		t.Fatalf("CountOpenByLevel(c2) = %d/%d, want 0/1", crit, warn)
	}
}

func TestHostStaleDetection(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, nil)
	org := testdb.Org(t, pool, "A")

	fresh := testdb.PushHost(t, pool, org, "fresh", "h")   // interval 10 sn
	silent := testdb.PushHost(t, pool, org, "silent", "h") // interval 10 sn
	never := testdb.PushHost(t, pool, org, "never", "h")
	_ = never // last_seen NULL ile offline kalır: raporlanmamalı

	for id, age := range map[uuid.UUID]time.Duration{fresh: 5 * time.Second, silent: 45 * time.Second} {
		if _, err := pool.Exec(ctx,
			`UPDATE host_status SET status = 'online', last_seen = now() - $2::interval WHERE host_id = $1`,
			id, age.String()); err != nil {
			t.Fatal(err)
		}
	}

	stale, err := hosts.ListStale(ctx, 3) // grace = 30 sn
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != silent {
		t.Fatalf("stale = %+v, want only the silent host", stale)
	}
}

func TestHostCreateEnforcesModeFields(t *testing.T) {
	pool := testdb.New(t)
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	org := testdb.Org(t, pool, "A")

	_, err := hosts.Create(context.Background(), store.CreateHostParams{
		OrganizationID: org, Title: "h", IP: "10.0.0.5", Mode: "pull", IntervalSeconds: 10,
		// port/endpoint/secret olmadan pull modu
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("pull host without pull fields: err=%v, want ErrConflict", err)
	}

	_, err = hosts.Create(context.Background(), store.CreateHostParams{
		OrganizationID: uuid.New(), Title: "h", IP: "10.0.0.5", Mode: "push", IntervalSeconds: 10, APITokenHash: ptr("x"),
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown organization: err=%v, want ErrNotFound", err)
	}
}

func TestRefreshTokenConsume(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	rt := store.NewRefreshTokens(pool)
	user := testdb.User(t, pool, "u@x.test", "operator", "pw")

	family := uuid.New()
	newToken := func(fam uuid.UUID, ttl time.Duration) uuid.UUID {
		t.Helper()
		jti := uuid.New()
		if err := rt.Create(ctx, jti, user, fam, time.Now().Add(ttl)); err != nil {
			t.Fatal(err)
		}
		return jti
	}
	const grace = 10 * time.Second

	if _, err := rt.Consume(ctx, uuid.New(), grace); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown jti: err=%v, want ErrNotFound", err)
	}

	a := newToken(family, time.Hour)
	got, err := rt.Consume(ctx, a, grace)
	if err != nil || got.UserID != user || got.FamilyID != family {
		t.Fatalf("first Consume = %+v err=%v", got, err)
	}

	// Tolerans penceresi içinde yeniden sunuldu (iki sekme yarışıyor): tolere edilir.
	if _, err := rt.Consume(ctx, a, grace); err != nil {
		t.Fatalf("second Consume within grace: %v", err)
	}

	// Çocuğu, aynı ailenin geçerli, bağımsız bir üyesidir.
	child := newToken(family, time.Hour)

	// Tolerans penceresinden sonra yeniden sunuldu: hırsızlık. Tüm aile ölür, kendisi hiç
	// yeniden kullanılmamış çocuk dahil.
	if _, err := pool.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = now() - interval '1 minute' WHERE jti = $1`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Consume(ctx, a, grace); !errors.Is(err, store.ErrTokenReuse) {
		t.Fatalf("reuse after grace: err=%v, want ErrTokenReuse", err)
	}
	if _, err := rt.Consume(ctx, child, grace); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("child of a compromised family: err=%v, want ErrNotFound (revoked)", err)
	}

	// Diğer aileler etkilenmez.
	other := newToken(uuid.New(), time.Hour)
	if _, err := rt.Consume(ctx, other, grace); err != nil {
		t.Fatalf("unrelated family affected by revocation: %v", err)
	}

	// Süresi dolmuş token'lar hiç kullanılmamış olsalar da reddedilir.
	expired := newToken(uuid.New(), -time.Minute)
	if _, err := rt.Consume(ctx, expired, grace); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired token: err=%v, want ErrNotFound", err)
	}
}

func TestRefreshTokenRevocation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	rt := store.NewRefreshTokens(pool)
	alice := testdb.User(t, pool, "alice@x.test", "operator", "pw")
	bob := testdb.User(t, pool, "bob@x.test", "operator", "pw")

	mk := func(user, family uuid.UUID) uuid.UUID {
		jti := uuid.New()
		if err := rt.Create(ctx, jti, user, family, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return jti
	}
	usable := func(jti uuid.UUID) bool {
		_, err := rt.Consume(ctx, jti, time.Minute)
		return err == nil
	}

	fam1, fam2 := uuid.New(), uuid.New()
	a1, a1b := mk(alice, fam1), mk(alice, fam1) // bir giriş, iki rotasyon
	a2 := mk(alice, fam2)                       // ikinci bir cihaz
	b1 := mk(bob, uuid.New())

	if err := rt.RevokeFamilyOf(ctx, a1); err != nil { // cihaz 1'de çıkış
		t.Fatal(err)
	}
	if usable(a1) || usable(a1b) {
		t.Fatal("logout left part of the family usable")
	}
	if !usable(a2) {
		t.Fatal("logout on one device ended another device's session")
	}
	if err := rt.RevokeFamilyOf(ctx, uuid.New()); err != nil {
		t.Fatalf("revoking an unknown token should be a no-op, got %v", err)
	}

	if err := rt.RevokeAllForUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if usable(a2) {
		t.Fatal("RevokeAllForUser left a session usable")
	}
	if !usable(b1) {
		t.Fatal("RevokeAllForUser affected another user")
	}
}

func TestRefreshTokenPurgeAndCascade(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	rt := store.NewRefreshTokens(pool)
	user := testdb.User(t, pool, "u@x.test", "operator", "pw")

	mk := func(ttl time.Duration) {
		if err := rt.Create(ctx, uuid.New(), user, uuid.New(), time.Now().Add(ttl)); err != nil {
			t.Fatal(err)
		}
	}
	mk(-48 * time.Hour) // çoktan ölü: temizlendi
	mk(-time.Hour)      // süresi doldu ama 1 günlük saklama içinde: tutuldu
	mk(time.Hour)       // canlı: tutuldu

	n, err := rt.PurgeExpired(ctx)
	if err != nil || n != 1 {
		t.Fatalf("PurgeExpired = %d err=%v, want 1", n, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user); err != nil {
		t.Fatal(err)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d refresh token rows survive their user's deletion", left)
	}
}

func newPullHost(t *testing.T, hosts *store.Hosts, org uuid.UUID, host, secret string) model.Host {
	t.Helper()
	c, err := hosts.Create(context.Background(), store.CreateHostParams{
		OrganizationID: org, Title: host, IP: "10.0.0.5", Mode: "pull", IntervalSeconds: 10,
		PullPort: ptr(9443), PullEndpoint: ptr("/api/v1/status"), PullSecret: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPullSecretIsEncryptedAtRest(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	box := testdb.SecretBox(t)
	hosts := store.NewHosts(pool, box)
	org := testdb.Org(t, pool, "A")

	const secret = "plaintext-shared-secret"
	c := newPullHost(t, hosts, org, "pull-1", secret)

	var raw string
	if err := pool.QueryRow(ctx, `SELECT pull_secret_enc FROM hosts WHERE id = $1`, c.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == secret || strings.Contains(raw, secret) || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("stored pull_secret = %q, want sealed ciphertext", raw)
	}

	list, err := hosts.ListPullHosts(ctx)
	if err != nil || len(list) != 1 || list[0].PullSecret != secret || list[0].ID != c.ID {
		t.Fatalf("ListPullHosts = %+v err=%v, want the decrypted secret", list, err)
	}

	// Rotasyon onu değiştirir (yine şifreli ve öncekinden farklı).
	if err := hosts.UpdatePullSecret(ctx, c.ID, "rotated-secret"); err != nil {
		t.Fatal(err)
	}
	var raw2 string
	pool.QueryRow(ctx, `SELECT pull_secret_enc FROM hosts WHERE id = $1`, c.ID).Scan(&raw2)
	if raw2 == raw || strings.Contains(raw2, "rotated-secret") {
		t.Fatalf("rotated pull_secret not re-encrypted: %q", raw2)
	}
	if list, _ = hosts.ListPullHosts(ctx); len(list) != 1 || list[0].PullSecret != "rotated-secret" {
		t.Fatalf("after rotation ListPullHosts = %+v", list)
	}
	if err := hosts.UpdatePullSecret(ctx, uuid.New(), "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rotating an unknown host: err=%v, want ErrNotFound", err)
	}
}

func TestPullSecretBoundToItsRowAndKey(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	org := testdb.Org(t, pool, "A")

	a := newPullHost(t, hosts, org, "a", "secret-a")
	b := newPullHost(t, hosts, org, "b", "secret-b")

	// DB yazma erişimi olan bir saldırgan a'nın şifreli metnini b'ye kopyalayıp öğrenmeye/tekrar
	// oynatmaya çalışır: b'nin secret'ı olarak çözülmemeli. b atlanır, a çalışmaya devam eder.
	if _, err := pool.Exec(ctx, `UPDATE hosts SET pull_secret_enc = (SELECT pull_secret_enc FROM hosts WHERE id = $1) WHERE id = $2`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	list, err := hosts.ListPullHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.ID || list[0].PullSecret != "secret-a" {
		t.Fatalf("ListPullHosts = %+v, want only host a", list)
	}

	// Farklı bir anahtar hiçbir şeyi okuyamaz.
	other, _ := secretbox.New([]byte("ffffffffffffffffffffffffffffffff"))
	if list, err := store.NewHosts(pool, other).ListPullHosts(ctx); err != nil || len(list) != 0 {
		t.Fatalf("wrong key: %+v err=%v, want no readable hosts and no error", list, err)
	}
}

func TestStoreWithoutKeyNeverWritesPlaintext(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, nil)
	org := testdb.Org(t, pool, "A")

	secret := "would-be-plaintext"
	_, err := hosts.Create(ctx, store.CreateHostParams{
		OrganizationID: org, Title: "p", IP: "10.0.0.5", Mode: "pull", IntervalSeconds: 10,
		PullPort: ptr(9443), PullEndpoint: ptr("/s"), PullSecret: &secret,
	})
	if !errors.Is(err, store.ErrNoSecretBox) {
		t.Fatalf("Create with a secret and no key: err=%v, want ErrNoSecretBox", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&n)
	if n != 0 {
		t.Fatal("a host row was written despite the missing key")
	}

	if err := hosts.UpdatePullSecret(ctx, uuid.New(), "x"); !errors.Is(err, store.ErrNoSecretBox) {
		t.Fatalf("UpdatePullSecret without key: err=%v", err)
	}
	if _, err := hosts.ListPullHosts(ctx); !errors.Is(err, store.ErrNoSecretBox) {
		t.Fatalf("ListPullHosts without key: err=%v", err)
	}

	// Push host'ların pull secret'ı yoktur, bu yüzden anahtara ihtiyaçları yoktur.
	if _, err := hosts.Create(ctx, store.CreateHostParams{
		OrganizationID: org, Title: "push", IP: "10.0.0.6", Mode: "push", IntervalSeconds: 10, APITokenHash: ptr("h"),
	}); err != nil {
		t.Fatalf("push host without key: %v", err)
	}
}

func superAdmins(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE role = 'super_admin'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLastSuperAdminCannotBeDemotedOrDeleted(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	users := store.NewUsers(pool)
	only := testdb.User(t, pool, "root@x.test", "super_admin", "pw")
	other := testdb.User(t, pool, "op@x.test", "operator", "pw")

	if _, err := users.Update(ctx, only, nil, ptr("operator")); !errors.Is(err, store.ErrLastSuperAdmin) {
		t.Fatalf("demoting the only super_admin: err=%v, want ErrLastSuperAdmin", err)
	}
	if err := users.Delete(ctx, only); !errors.Is(err, store.ErrLastSuperAdmin) {
		t.Fatalf("deleting the only super_admin: err=%v, want ErrLastSuperAdmin", err)
	}
	if !errors.Is(store.ErrLastSuperAdmin, store.ErrConflict) {
		t.Fatal("ErrLastSuperAdmin must wrap ErrConflict so it maps to 409")
	}
	if superAdmins(t, pool) != 1 {
		t.Fatal("the last super_admin was changed despite the error")
	}

	// Son super_admin'de zararsız değişikliklere hâlâ izin verilir.
	if _, err := users.Update(ctx, only, ptr("root2@x.test"), nil); err != nil {
		t.Fatalf("renaming the last super_admin: %v", err)
	}
	if _, err := users.Update(ctx, only, nil, ptr("super_admin")); err != nil {
		t.Fatalf("re-asserting super_admin: %v", err)
	}
	// Super olmayan kullanıcılar korumadan etkilenmez.
	if _, err := users.Update(ctx, other, nil, ptr("org_admin")); err != nil {
		t.Fatalf("changing a non-super user's role: %v", err)
	}
	if err := users.Delete(ctx, other); err != nil {
		t.Fatalf("deleting a non-super user: %v", err)
	}
	if err := users.Delete(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting an unknown user: err=%v, want ErrNotFound", err)
	}

	// İkinci bir super_admin varsa ikisinden biri gidebilir — ama ikisi birden değil.
	second := testdb.User(t, pool, "root3@x.test", "super_admin", "pw")
	if _, err := users.Update(ctx, only, nil, ptr("operator")); err != nil {
		t.Fatalf("demoting one of two super_admins: %v", err)
	}
	if err := users.Delete(ctx, second); !errors.Is(err, store.ErrLastSuperAdmin) {
		t.Fatalf("deleting the now-last super_admin: err=%v, want ErrLastSuperAdmin", err)
	}
}

// Aynı anda birbirine işlem yapan iki super_admin'in ikisi de kazanmamalı: her denetim
// diğerini hâlâ bir super_admin olarak okur.
func TestLastSuperAdminGuardHoldsUnderConcurrency(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	users := store.NewUsers(pool)
	a := testdb.User(t, pool, "a@x.test", "super_admin", "pw")
	b := testdb.User(t, pool, "b@x.test", "super_admin", "pw")

	for round := 0; round < 25; round++ {
		if _, err := pool.Exec(ctx, `UPDATE users SET role = 'super_admin'`); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, id := range []uuid.UUID{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if round%2 == 0 {
					_, errs[i] = users.Update(ctx, id, nil, ptr("operator"))
				} else {
					errs[i] = users.Delete(ctx, id)
				}
			}()
		}
		close(start)
		wg.Wait()

		if left := superAdmins(t, pool); left < 1 {
			t.Fatalf("round %d: %d super_admins left (errors: %v, %v)", round, left, errs[0], errs[1])
		}
		if errs[0] == nil && errs[1] == nil {
			t.Fatalf("round %d: both concurrent removals succeeded", round)
		}
		if round%2 == 1 { // bir silme bir kullanıcıyı kaldırdı; sonraki tur için onları geri koy
			pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, role) VALUES ($1, 'a@x.test', 'h', 'super_admin') ON CONFLICT DO NOTHING`, a)
			pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, role) VALUES ($1, 'b@x.test', 'h', 'super_admin') ON CONFLICT DO NOTHING`, b)
		}
	}
}

func TestCreateIfNoneOpenIsAtomicPerHostAndMetric(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	first, created, err := alerts.CreateIfNoneOpen(ctx, host, "cpu", "", "warning", nil, nil)
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	if _, created, err = alerts.CreateIfNoneOpen(ctx, host, "cpu", "", "critical", nil, nil); err != nil || created {
		t.Fatalf("second while open: created=%v err=%v, want created=false", created, err)
	}
	if _, created, _ = alerts.CreateIfNoneOpen(ctx, host, "ram", "", "warning", nil, nil); !created {
		t.Fatal("a different metric must be able to open its own alert")
	}

	// Yöntemi atlasa bile veritabanının kendisi ikinci bir açık satırı reddeder.
	if _, err := alerts.Create(ctx, host, "cpu", "warning"); err == nil {
		t.Fatal("plain INSERT of a second open alert succeeded; the unique index is missing")
	}

	// İlki artık açık olmayınca (onaylandı ya da çözüldü) yenisi açılabilir.
	ackBy := testdb.User(t, pool, "acker@x.test", "super_admin", "pw")
	if _, err := alerts.Acknowledge(ctx, first.ID, ackBy); err != nil {
		t.Fatal(err)
	}
	second, created, err := alerts.CreateIfNoneOpen(ctx, host, "cpu", "", "warning", nil, nil)
	if err != nil || !created {
		t.Fatalf("after acknowledge: created=%v err=%v", created, err)
	}
	alerts.Resolve(ctx, second.ID, nil, nil)
	if _, created, _ = alerts.CreateIfNoneOpen(ctx, host, "cpu", "", "warning", nil, nil); !created {
		t.Fatal("after resolve a new alert must be able to open")
	}

	// 25 eşzamanlı deneme: tam bir ekleme kazanır.
	other := testdb.PushHost(t, pool, testdb.Org(t, pool, "B"), "c2", "h")
	var wg sync.WaitGroup
	var wins atomic.Int32
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, created, err := alerts.CreateIfNoneOpen(ctx, other, "disk", "", "critical", nil, nil); err == nil && created {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d of 25 concurrent creators succeeded, want exactly 1", wins.Load())
	}
}

// seedMetrics, `end`de biten `n` örnek için `step` başına bir örnek ekler; cpu = cpuAt(i) ve
// sabit bir disk okumasıyla.
func seedMetrics(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, host uuid.UUID, end time.Time, n int, step time.Duration, cpuAt string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO metrics (host_id, recorded_at, cpu_usage_pct, ram_usage_pct, disk_json)
		 SELECT $1, $2::timestamptz - (g * $3::bigint || ' milliseconds')::interval, `+cpuAt+`, 40,
		        jsonb_build_array(jsonb_build_object('mount','/','used_pct', g % 100,'total',1,'free',1))
		 FROM generate_series(0, $4::int - 1) g`,
		host, end, step.Milliseconds(), n)
	if err != nil {
		t.Fatal(err)
	}
}

// ListByHostAndRange hiçbir zaman kovalamaz/ortalamaz: aralıktaki her ham satır, sıralı ve
// değiştirilmemiş, döner — kısa vadeli bir tepe noktası (ör. bir alert'i tetikleyen okuma)
// komşu örneklerle ortalanıp kaybolmasın diye.
func TestMetricsRangeReturnsEveryRawSample(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	end := time.Now().UTC().Truncate(time.Second)
	seedMetrics(t, pool, host, end, 30, time.Minute, `(10 + (29 - g) * 1)`)
	pts, err := m.ListByHostAndRange(ctx, host, end.Add(-time.Hour), end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 30 {
		t.Fatalf("%d points, want all 30 raw samples", len(pts))
	}
	for i, p := range pts {
		if want := float64(10 + i); p.CPUUsagePct != want {
			t.Fatalf("point %d cpu = %v, want %v (raw, unaveraged)", i, p.CPUUsagePct, want)
		}
	}
	for i := 1; i < len(pts); i++ {
		if !pts[i].Timestamp.After(pts[i-1].Timestamp) {
			t.Fatal("points are not strictly ascending")
		}
	}

	// Aşırı dar / dejenere aralıklar da çalışır.
	if pts, err := m.ListByHostAndRange(ctx, host, end, end); err != nil || len(pts) != 1 {
		t.Fatalf("zero-width range: %d points, err=%v", len(pts), err)
	}
	if pts, err := m.ListByHostAndRange(ctx, host, end.Add(time.Hour), end.Add(2*time.Hour)); err != nil || len(pts) != 0 {
		t.Fatalf("empty range: %d points, err=%v", len(pts), err)
	}
}

// Büyük bir örnek sayısı da (ör. 200.000 satır geniş bir aralıkta) bounded/kovalanmaz — hepsi
// olduğu gibi döner. Yanıt boyutu bilinçli olarak sınırlanmıyor.
func TestMetricsRangeWithManySamplesReturnsAllOfThem(t *testing.T) {
	pool := testdb.New(t)
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")
	end := time.Now().UTC()
	seedMetrics(t, pool, host, end, 5000, time.Second, `50`)

	pts, err := m.ListByHostAndRange(context.Background(), host, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 5000 {
		t.Fatalf("%d points, want all 5,000 raw samples (no bucketing)", len(pts))
	}
}

func dockerRows(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, host uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM docker_containers WHERE host_id = $1`, host).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDockerContainersAreLatestStateNotHistory(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")
	other := testdb.PushHost(t, pool, testdb.Org(t, pool, "B"), "c2", "h")

	report := func(restarts int, status string, names ...string) []model.DockerContainerReport {
		var out []model.DockerContainerReport
		for _, n := range names {
			out = append(out, model.DockerContainerReport{Name: n, Image: n + ":1", Status: status, CPUPct: 1.5, RAMMB: 64, RestartCount: restarts, UptimeSeconds: 10})
		}
		return out
	}

	// Regresyon: bu eskiden her push'ta sonsuza dek 3 satır ekliyordu.
	for i := 0; i < 200; i++ {
		if err := m.ReplaceDockerContainers(ctx, host, report(i, "running", "web", "db", "cache")); err != nil {
			t.Fatal(err)
		}
	}
	if n := dockerRows(t, pool, host); n != 3 {
		t.Fatalf("%d rows after 200 reports of 3 containers, want 3", n)
	}

	got, err := m.LatestDockerContainers(ctx, host)
	if err != nil || len(got) != 3 {
		t.Fatalf("LatestDockerContainers = %+v err=%v", got, err)
	}
	if got[0].Name != "cache" || got[1].Name != "db" || got[2].Name != "web" {
		t.Fatalf("not sorted by name: %+v", got)
	}
	for _, c := range got {
		if c.RestartCount != 199 {
			t.Fatalf("%s restart_count = %d, want the newest report's 199", c.Name, c.RestartCount)
		}
	}

	// Raporlarda görünmeyi bırakan container kaldırılır; görünen eklenir.
	if err := m.ReplaceDockerContainers(ctx, host, report(0, "exited", "web", "queue")); err != nil {
		t.Fatal(err)
	}
	got, _ = m.LatestDockerContainers(ctx, host)
	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "queue,web" || got[1].Status != "exited" {
		t.Fatalf("after change: %v (%+v)", names, got)
	}

	// Diğer host'lara dokunulmaz ve boş bir rapor yalnızca kendi host'ını temizler.
	if err := m.ReplaceDockerContainers(ctx, other, report(0, "running", "web")); err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceDockerContainers(ctx, host, nil); err != nil {
		t.Fatal(err)
	}
	if dockerRows(t, pool, host) != 0 || dockerRows(t, pool, other) != 1 {
		t.Fatalf("empty report: host=%d other=%d, want 0 and 1", dockerRows(t, pool, host), dockerRows(t, pool, other))
	}

	// Bir raporda bir adı tekrarlayan hatalı bir agent tüm raporu başarısız kılmamalı.
	dup := append(report(1, "running", "web"), report(2, "running", "web")...)
	if err := m.ReplaceDockerContainers(ctx, host, dup); err != nil {
		t.Fatalf("duplicate names in one report: %v", err)
	}
	if dockerRows(t, pool, host) != 1 {
		t.Fatal("duplicate names created duplicate rows")
	}
}

func TestConcurrentDockerReportsForOneHostDoNotConflict(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var cs []model.DockerContainerReport
			for _, n := range []string{"a", "b", "c", "d"} { // aynı adlar, yarışan yeniden denenmiş push'lar
				cs = append(cs, model.DockerContainerReport{Name: n, Image: "i", Status: "running", RestartCount: i})
			}
			errs <- m.ReplaceDockerContainers(ctx, host, cs)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent report failed (deadlock or unique violation): %v", err)
		}
	}
	if n := dockerRows(t, pool, host); n != 4 {
		t.Fatalf("%d rows after concurrent reports, want 4", n)
	}
}

func TestPurgeOlderThan(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	org := testdb.Org(t, pool, "A")
	c1 := testdb.PushHost(t, pool, org, "c1", "h")
	c2 := testdb.PushHost(t, pool, org, "c2", "h")

	now := time.Now().UTC()
	for _, c := range []uuid.UUID{c1, c2} {
		seedMetrics(t, pool, c, now.Add(-40*24*time.Hour), 35, time.Minute, `10`) // eski: her biri 35 satır
		seedMetrics(t, pool, c, now, 12, time.Minute, `20`)                       // yeni: her biri 12 satır
	}

	cutoff := now.Add(-30 * 24 * time.Hour)
	n, err := m.PurgeOlderThan(ctx, cutoff, 10) // 10'luk parti birkaç tur zorlar
	if err != nil {
		t.Fatal(err)
	}
	if n != 70 {
		t.Fatalf("deleted %d, want 70 (35 old rows x 2 hosts)", n)
	}
	var left, oldLeft int
	pool.QueryRow(ctx, `SELECT count(*) FROM metrics`).Scan(&left)
	pool.QueryRow(ctx, `SELECT count(*) FROM metrics WHERE recorded_at < $1`, cutoff).Scan(&oldLeft)
	if left != 24 || oldLeft != 0 {
		t.Fatalf("after purge: %d rows total, %d old; want 24 and 0", left, oldLeft)
	}
	if n, err := m.PurgeOlderThan(ctx, cutoff, 10); err != nil || n != 0 {
		t.Fatalf("second purge = %d err=%v, want 0", n, err)
	}

	// İptal edilmiş bir context uzun bir temizliği sonuna kadar çalıştırmak yerine durdurur.
	seedMetrics(t, pool, c1, now.Add(-40*24*time.Hour), 50, time.Minute, `10`)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.PurgeOlderThan(cctx, cutoff, 5); err == nil {
		t.Fatal("purge ignored a cancelled context")
	}
}

// Regresyon: birden çok çekirdek kullanan bir container (Docker, 16 çekirdeğin 12'si için
// %1200 raporlar) NUMERIC(5,2)'yi taşırıyordu ve ortaya çıkan hata TÜM raporu — her sağlıklı
// container dahil — yalnızca bir log satırıyla düşürüyordu.
func TestOneBadContainerDoesNotLoseTheWholeDockerReport(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	err := m.ReplaceDockerContainers(ctx, host, []model.DockerContainerReport{
		{Name: "busy", Image: "i", Status: "running", CPUPct: 1200, RAMMB: 10},                    // meşru olarak > 999,99
		{Name: "healthy", Image: "i", Status: "running", CPUPct: 5, RAMMB: 10},                    // hayatta kalmalı
		{Name: "absurd", Image: "i", Status: "running", CPUPct: 1e15, RAMMB: 1e15},                // kırpıldı, reddedilmedi
		{Name: "negative", Image: "i", Status: "exited", CPUPct: -3, RAMMB: -1, RestartCount: -2}, // 0'a kırpıldı
		{Name: "nan", Image: "i", Status: "running", CPUPct: math.NaN()},                          // 0'a kırpıldı
		{Name: "weird-state", Image: "i", Status: "levitating"},                                   // şemanın reddedeceği: atlandı
	})
	if err != nil {
		t.Fatalf("report with awkward containers failed as a whole: %v", err)
	}

	got, _ := m.LatestDockerContainers(ctx, host)
	byName := map[string]model.DockerContainerReport{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if len(got) != 5 || byName["weird-state"].Name != "" {
		t.Fatalf("stored %d containers %v; want the 5 storable ones and not weird-state", len(got), byName)
	}
	if byName["busy"].CPUPct != 1200 {
		t.Errorf("busy container cpu = %v, want the real 1200", byName["busy"].CPUPct)
	}
	if byName["healthy"].CPUPct != 5 {
		t.Errorf("healthy container lost or altered: %+v", byName["healthy"])
	}
	if c := byName["absurd"]; c.CPUPct != 1_000_000 || c.RAMMB != 1_000_000_000 {
		t.Errorf("absurd values not clamped: %+v", c)
	}
	if c := byName["negative"]; c.CPUPct != 0 || c.RAMMB != 0 || c.RestartCount != 0 {
		t.Errorf("negative values not clamped to 0: %+v", c)
	}
	if byName["nan"].CPUPct != 0 {
		t.Errorf("NaN cpu = %v, want 0", byName["nan"].CPUPct)
	}
}

func TestAlertSubjectsAllowOneOpenAlertPerContainer(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	a, created, err := alerts.CreateIfNoneOpen(ctx, host, "docker_restart", "web", "warning", nil, nil)
	if err != nil || !created || a.Subject != "web" {
		t.Fatalf("first: %+v created=%v err=%v", a, created, err)
	}
	if _, created, _ = alerts.CreateIfNoneOpen(ctx, host, "docker_restart", "db", "warning", nil, nil); !created {
		t.Fatal("a second container must get its own alert")
	}
	if _, created, _ = alerts.CreateIfNoneOpen(ctx, host, "docker_restart", "web", "critical", nil, nil); created {
		t.Fatal("the same container got a second open alert")
	}
	// Host geneli alert ("" subject) container alert'lerinden bağımsızdır.
	if _, created, _ = alerts.CreateIfNoneOpen(ctx, host, "docker_restart", "", "warning", nil, nil); !created {
		t.Fatal("host-level alert blocked by container alerts")
	}

	got, err := alerts.GetOpenSubject(ctx, host, "docker_restart", "web")
	if err != nil || got.ID != a.ID {
		t.Fatalf("GetOpenSubject = %+v err=%v", got, err)
	}
	if _, err := alerts.GetOpenSubject(ctx, host, "docker_restart", "cache"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown subject: err=%v, want ErrNotFound", err)
	}
	if _, err := alerts.GetOpen(ctx, host, "docker_restart"); err != nil { // "" subject
		t.Fatalf("GetOpen (empty subject): %v", err)
	}

	open, _ := alerts.ListOpen(ctx, host, "docker_restart")
	names := []string{}
	for _, o := range open {
		names = append(names, o.Subject)
	}
	if strings.Join(names, ",") != "db,web," {
		t.Fatalf("ListOpen subjects = %v", names)
	}

	// Veritabanının kendisi aynı subject için yinelenen açık alert'i reddeder.
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (host_id, alert_type, subject, level) VALUES ($1, 'docker_restart', 'web', 'warning')`, host); err == nil {
		t.Fatal("unique index does not cover the subject")
	}
}

func TestBootstrapSuperAdmin(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	users := store.NewUsers(pool)

	if n, _ := users.CountSuperAdmins(ctx); n != 0 {
		t.Fatalf("precondition: %d super_admins", n)
	}
	created, err := users.BootstrapSuperAdmin(ctx, "Root@X.test", "hash")
	if err != nil || !created {
		t.Fatalf("first bootstrap: created=%v err=%v", created, err)
	}
	u, err := users.GetByEmail(ctx, "root@x.test")
	if err != nil || u.Role != "super_admin" || !u.MustChangePassword {
		t.Fatalf("bootstrapped user = %+v err=%v; want a super_admin that must change its password", u, err)
	}

	// İdempotenttir ve — başka bir e-postayla bile — asla İKİNCİ bir super_admin eklemez.
	for _, email := range []string{"root@x.test", "another@x.test"} {
		if created, err := users.BootstrapSuperAdmin(ctx, email, "hash"); err != nil || created {
			t.Fatalf("bootstrap while a super_admin exists (%s): created=%v err=%v", email, created, err)
		}
	}
	if n, _ := users.CountSuperAdmins(ctx); n != 1 {
		t.Fatalf("%d super_admins, want 1", n)
	}

	// Aynı anda açılan birkaç kopya yine de tam bir tane verir.
	pool2 := testdb.New(t)
	users2 := store.NewUsers(pool2)
	var wg sync.WaitGroup
	var made atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if created, err := users2.BootstrapSuperAdmin(ctx, "root@x.test", "hash"); err == nil && created {
				made.Add(1)
			}
		}()
	}
	wg.Wait()
	if n, _ := users2.CountSuperAdmins(ctx); n != 1 || made.Load() != 1 {
		t.Fatalf("concurrent bootstrap: %d super_admins, %d creators; want 1 and 1", n, made.Load())
	}
}

func TestSetOwnPasswordClearsTheFlagButAdminResetDoesNot(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	users := store.NewUsers(pool)
	id := testdb.User(t, pool, "u@x.test", "operator", "old-password-123")
	if _, err := pool.Exec(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	// Yöneticinin şifreyi sıfırlaması, kullanıcının bir şifre seçmesi sayılmaz.
	if err := users.UpdatePassword(ctx, id, "admin-chosen-hash"); err != nil {
		t.Fatal(err)
	}
	if u, _ := users.GetByID(ctx, id); !u.MustChangePassword || u.PasswordHash != "admin-chosen-hash" {
		t.Fatalf("after admin reset: %+v; the flag must stay set", u)
	}

	if err := users.SetOwnPassword(ctx, id, "self-chosen-hash"); err != nil {
		t.Fatal(err)
	}
	if u, _ := users.GetByID(ctx, id); u.MustChangePassword || u.PasswordHash != "self-chosen-hash" {
		t.Fatalf("after the user's own change: %+v; the flag must be cleared", u)
	}
	if err := users.SetOwnPassword(ctx, uuid.New(), "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: err=%v, want ErrNotFound", err)
	}
}

func TestDiskAlertMountsRoundTrips(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, nil)
	org := testdb.Org(t, pool, "A")
	id := testdb.PushHost(t, pool, org, "c", "h")

	got := func() (bool, []string) {
		t.Helper()
		all, m, err := hosts.DiskAlertMounts(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return all, m
	}
	if all, m := got(); !all || len(m) != 0 {
		t.Fatalf("a new host's selection = all=%v %#v, want every reported mount and no custom list", all, m)
	}

	if err := hosts.SetDiskAlertMounts(ctx, id, false, []string{"/data", "/"}); err != nil {
		t.Fatal(err)
	}
	if all, m := got(); all || len(m) != 2 || m[0] != "/" || m[1] != "/data" {
		t.Fatalf("custom list = all=%v %#v (must be sorted)", all, m)
	}

	// Kapalı liste boşsa "hiçbiri": tümünden farklıdır.
	if err := hosts.SetDiskAlertMounts(ctx, id, false, []string{}); err != nil {
		t.Fatal(err)
	}
	if all, m := got(); all || len(m) != 0 {
		t.Fatalf("empty custom selection = all=%v %#v, want none", all, m)
	}
	h, _ := hosts.GetByID(ctx, id)
	if h.AllMountsAlert {
		t.Fatal("GetByID turned 'none' into 'all'")
	}

	if err := hosts.SetDiskAlertMounts(ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if all, _ := got(); !all {
		t.Fatal("all_mounts_alert did not switch back on")
	}

	if err := hosts.SetDiskAlertMounts(ctx, uuid.New(), true, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host: err=%v, want ErrNotFound", err)
	}
	if _, _, err := hosts.DiskAlertMounts(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host read: err=%v", err)
	}

	// Mount yolları, boşluklu ya da ASCII olmayan karakterli olanlar dahil, birebir saklanır; tekrarlar tekilleşir.
	odd := []string{"/mnt/My Disk", "/mnt/yedek-ş", "/mnt/My Disk"}
	hosts.SetDiskAlertMounts(ctx, id, false, odd)
	if _, m := got(); len(m) != 2 || m[0] != "/mnt/My Disk" || m[1] != "/mnt/yedek-ş" {
		t.Fatalf("odd paths = %#v", m)
	}
}

func TestHostCreatedWithADiskSelection(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, nil)
	org := testdb.Org(t, pool, "A")
	n := 0
	mk := func(all bool, sel []string) model.Host {
		n++
		c, err := hosts.Create(ctx, store.CreateHostParams{OrganizationID: org, Title: fmt.Sprintf("h%d", n), IP: "10.0.0.1", Mode: "push",
			IntervalSeconds: 10, APITokenHash: ptr("x"), AllMountsAlert: all, CustomAlertMounts: sel})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := mk(true, nil); !c.AllMountsAlert || len(c.CustomAlertMounts) != 0 {
		t.Fatalf("default = %v %#v", c.AllMountsAlert, c.CustomAlertMounts)
	}
	if c := mk(false, []string{"/data"}); c.AllMountsAlert || len(c.CustomAlertMounts) != 1 || c.CustomAlertMounts[0] != "/data" {
		t.Fatalf("created with a selection = %v %#v", c.AllMountsAlert, c.CustomAlertMounts)
	}
	if c := mk(false, nil); c.AllMountsAlert || len(c.CustomAlertMounts) != 0 {
		t.Fatalf("created with none = %v %#v", c.AllMountsAlert, c.CustomAlertMounts)
	}
}

func TestLatestDisksIsTheNewestReport(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	id := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	if disks, err := m.LatestDisks(ctx, id); err != nil || disks == nil || len(disks) != 0 {
		t.Fatalf("no reports yet: %#v %v, want an empty (non-nil) list", disks, err)
	}
	m.Insert(ctx, id, 1, 1, []model.DiskUsage{{Mount: "/", UsedPct: 10, Total: 100, Free: 90}})
	time.Sleep(15 * time.Millisecond)
	m.Insert(ctx, id, 1, 1, []model.DiskUsage{{Mount: "/", UsedPct: 20, Total: 100, Free: 80}, {Mount: "/data", UsedPct: 60, Total: 500, Free: 200}})

	disks, err := m.LatestDisks(ctx, id)
	if err != nil || len(disks) != 2 || disks[0].UsedPct != 20 || disks[1].Mount != "/data" || disks[1].Total != 500 {
		t.Fatalf("LatestDisks = %+v err=%v, want the newer report", disks, err)
	}
}

func TestHostCreationWithThresholdsIsAtomic(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	org := testdb.Org(t, pool, "A")
	base := store.CreateHostParams{OrganizationID: org, Title: "h", IP: "10.0.0.5", Mode: "push", IntervalSeconds: 10, APITokenHash: ptr("x")}
	count := func(table string) (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	good := base
	good.Thresholds = model.ThresholdOverrides{"cpu": {WarningLevel: 10, CriticalLevel: 20}, "ram": nil}
	c, err := hosts.Create(ctx, good)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.NewThresholds(pool).HostOverrides(ctx, c.ID)
	if err != nil || len(got) != 1 || got["cpu"] != (model.ThresholdLevels{WarningLevel: 10, CriticalLevel: 20}) {
		t.Fatalf("stored overrides = %v (err %v), want only cpu 10/20", got, err)
	}

	// İkinci eşik veritabanı tarafından reddedilir (bilinmeyen metrik): ondan hemen önce eklenen
	// host satırı ve geçerli cpu satırı onunla birlikte geri alınmalı.
	hostsBefore, thresholdsBefore := count("hosts"), count("host_custom_thresholds")
	broken := base
	broken.Thresholds = model.ThresholdOverrides{"cpu": {WarningLevel: 1, CriticalLevel: 2}, "zzz": {WarningLevel: 1, CriticalLevel: 2}}
	if _, err := hosts.Create(ctx, broken); err == nil {
		t.Fatal("creation with a rejected threshold succeeded")
	}
	if count("hosts") != hostsBefore || count("host_custom_thresholds") != thresholdsBefore {
		t.Fatalf("partial write: hosts %d->%d, thresholds %d->%d", hostsBefore, count("hosts"), thresholdsBefore, count("host_custom_thresholds"))
	}
}

func TestDefaultsForPrefersTheOrganizationOverTheGlobalThreshold(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	orgA, orgB := testdb.Org(t, pool, "A"), testdb.Org(t, pool, "B")
	testdb.Threshold(t, pool, nil, nil, "cpu", 50, 90)
	testdb.Threshold(t, pool, nil, nil, "ram", 60, 80)
	testdb.Threshold(t, pool, &orgA, nil, "cpu", 10, 20)
	host := testdb.PushHost(t, pool, orgA, "c", "h")
	testdb.Threshold(t, pool, nil, &host, "ram", 1, 2) // bir host'ın kendi değeri varsayılan değildir

	a, err := th.DefaultsFor(ctx, orgA)
	if err != nil {
		t.Fatal(err)
	}
	if a["cpu"] != (model.ThresholdLevels{WarningLevel: 10, CriticalLevel: 20}) || a["ram"] != (model.ThresholdLevels{WarningLevel: 60, CriticalLevel: 80}) || len(a) != 2 {
		t.Fatalf("defaults for org A = %v", a)
	}
	b, _ := th.DefaultsFor(ctx, orgB)
	if b["cpu"] != (model.ThresholdLevels{WarningLevel: 50, CriticalLevel: 90}) || len(b) != 2 {
		t.Fatalf("defaults for org B = %v", b)
	}
}

func TestSetHostOverridesAppliesAllOrNothing(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	org := testdb.Org(t, pool, "A")
	host := testdb.PushHost(t, pool, org, "c", "h")
	testdb.Threshold(t, pool, nil, &host, "ram", 1, 2)

	// "cpu" "zzz"den önce sıralanır; bu yüzden cpu önce yazılır ve zzz başarısız olunca geri
	// alınmalı; ram'in silinmesi (aralarında sıralanır) de geri alınmalı.
	err := th.SetHostOverrides(ctx, host, model.ThresholdOverrides{
		"cpu": {WarningLevel: 10, CriticalLevel: 20}, "ram": nil, "zzz": {WarningLevel: 1, CriticalLevel: 2},
	}, nil, nil)
	if err == nil {
		t.Fatal("a rejected threshold was accepted")
	}
	got, err := th.HostOverrides(ctx, host)
	if err != nil || len(got) != 1 || got["ram"] != (model.ThresholdLevels{WarningLevel: 1, CriticalLevel: 2}) {
		t.Fatalf("after the failed call: %v (err %v), want the untouched original ram 1/2", got, err)
	}

	if err := th.SetHostOverrides(ctx, uuid.New(), model.ThresholdOverrides{"cpu": {WarningLevel: 1, CriticalLevel: 2}}, nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host: err=%v, want ErrNotFound", err)
	}
}

func TestResolveDiskPrefersTheMountThenTheHostThenTheOrganizationThenTheGlobalThreshold(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	orgA, orgB := testdb.Org(t, pool, "A"), testdb.Org(t, pool, "B")
	a1 := testdb.PushHost(t, pool, orgA, "a1", "h")
	a2 := testdb.PushHost(t, pool, orgA, "a2", "h")
	b1 := testdb.PushHost(t, pool, orgB, "b1", "h")
	testdb.Threshold(t, pool, nil, nil, "disk", 85, 95)    // global
	testdb.Threshold(t, pool, &orgA, nil, "disk", 80, 90)  // organizasyon A
	testdb.Threshold(t, pool, nil, &a1, "disk", 60, 80)    // a1'in kendi eşiği
	testdb.MountThreshold(t, pool, a1, "/storage", 90, 95) // a1'in /storage'ı
	testdb.MountThreshold(t, pool, a2, "/other", 1, 2)     // başka bir host'ın mount'u: sızmamalı
	levels := func(c model.ThresholdConfig, ok bool) (w, cr float64, found bool) {
		return c.WarningLevel, c.CriticalLevel, ok
	}
	check := func(name string, host, org uuid.UUID, mount string, wantW, wantC float64, wantFound bool) {
		t.Helper()
		d, err := th.ResolveSubjects(ctx, host, org, model.MetricTypeDisk)
		if err != nil {
			t.Fatal(err)
		}
		w, c, ok := levels(d.For(mount))
		if ok != wantFound || w != wantW || c != wantC {
			t.Errorf("%s: %s on %v = %v/%v found=%v, want %v/%v found=%v", name, mount, host, w, c, ok, wantW, wantC, wantFound)
		}
	}
	check("mount's own", a1, orgA, "/storage", 90, 95, true)
	check("host's own for the other mounts", a1, orgA, "/", 60, 80, true)
	check("organization default", a2, orgA, "/", 80, 90, true)
	check("a2's own mount row", a2, orgA, "/other", 1, 2, true)
	check("another host's mount row is invisible to a1", a1, orgA, "/other", 60, 80, true)
	check("global default", b1, orgB, "/storage", 85, 95, true)
	if _, err := pool.Exec(ctx, `DELETE FROM threshold_defaults WHERE organization_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	check("nothing for organization B any more", b1, orgB, "/", 0, 0, false)
}

func TestSetHostOverridesWritesMountThresholdsAllOrNothing(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)
	org := testdb.Org(t, pool, "A")
	c := testdb.PushHost(t, pool, org, "c", "h")
	testdb.MountThreshold(t, pool, c, "/keep", 1, 2)
	testdb.MountThreshold(t, pool, c, "/drop", 3, 4)

	err := th.SetHostOverrides(ctx, c, model.ThresholdOverrides{"disk": {WarningLevel: 70, CriticalLevel: 80}},
		model.MountThresholds{"/new": {WarningLevel: 5, CriticalLevel: 6}, "/drop": nil, "/keep": {WarningLevel: 9, CriticalLevel: 10}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := th.HostSubjectOverrides(ctx, c, model.MetricTypeDisk)
	if len(got) != 2 || got["/new"] != (model.ThresholdLevels{WarningLevel: 5, CriticalLevel: 6}) || got["/keep"] != (model.ThresholdLevels{WarningLevel: 9, CriticalLevel: 10}) {
		t.Fatalf("mount thresholds = %v, want /new 5/6 and /keep 9/10 (/drop removed)", got)
	}
	if o, _ := th.HostOverrides(ctx, c); len(o) != 1 || o["disk"].WarningLevel != 70 {
		t.Fatalf("the host-wide disk threshold must stay separate from the per-mount ones: %v", o)
	}

	// Reddedilen bir yazma (bilinmeyen metrik mount'lardan önce sıralanır) her mount'u olduğu gibi bırakır.
	err = th.SetHostOverrides(ctx, c, model.ThresholdOverrides{"zzz": {WarningLevel: 1, CriticalLevel: 2}},
		model.MountThresholds{"/keep": nil, "/new": {WarningLevel: 50, CriticalLevel: 60}}, nil)
	if err == nil {
		t.Fatal("rejected threshold accepted")
	}
	got, _ = th.HostSubjectOverrides(ctx, c, model.MetricTypeDisk)
	if len(got) != 2 || got["/new"].WarningLevel != 5 || got["/keep"].WarningLevel != 9 {
		t.Fatalf("after the failed call: %v, want unchanged", got)
	}
}

func TestRecentReportedMountsSkipsEmptyReportsAndOrdersNewestFirst(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	org := testdb.Org(t, pool, "A")
	c := testdb.PushHost(t, pool, org, "c", "h")
	other := testdb.PushHost(t, pool, org, "other", "h")
	disks := func(mounts ...string) []model.DiskUsage {
		var out []model.DiskUsage
		for _, mnt := range mounts {
			out = append(out, model.DiskUsage{Mount: mnt, UsedPct: 1, Total: 10, Free: 9})
		}
		return out
	}
	for _, d := range [][]model.DiskUsage{disks("/", "/a"), nil, disks("/"), {}, disks("/", "/b"), disks("/c")} {
		if err := m.Insert(ctx, c, 1, 1, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Insert(ctx, other, 1, 1, disks("/other")); err != nil {
		t.Fatal(err)
	}

	got, err := m.RecentReportedMounts(ctx, c, 3)
	if err != nil {
		t.Fatal(err)
	}
	has := func(set map[string]struct{}, mounts ...string) bool {
		if len(set) != len(mounts) {
			return false
		}
		for _, mnt := range mounts {
			if _, ok := set[mnt]; !ok {
				return false
			}
		}
		return true
	}
	if len(got) != 3 || !has(got[0], "/c") || !has(got[1], "/", "/b") || !has(got[2], "/") {
		t.Fatalf("got %v, want the last three NON-EMPTY reports of this host, newest first ({/c}, {/ /b}, {/})", got)
	}
	if all, _ := m.RecentReportedMounts(ctx, c, 10); len(all) != 4 {
		t.Fatalf("%d reports, want the 4 non-empty ones (the nil and [] reports are skipped)", len(all))
	}
}

func TestOnlyDiskAndDockerRestartThresholdsMayHaveASubject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org := testdb.Org(t, pool, "A")
	c := testdb.PushHost(t, pool, org, "c", "h")

	testdb.SubjectThreshold(t, pool, c, "docker_restart", "web", 1, 2) // izinli
	testdb.SubjectThreshold(t, pool, c, "disk", "/x", 1, 2)            // izinli
	for name, sql := range map[string]string{
		"host cpu": `INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level) VALUES ('` + c.String() + `', 'cpu', 'x', 1, 2)`,
		"host ram": `INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level) VALUES ('` + c.String() + `', 'ram', 'x', 1, 2)`,
	} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Errorf("%s: a subject was accepted", name)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level) VALUES ($1, 'docker_restart', 'web', 3, 4)`, c); err == nil {
		t.Error("a second row for the same host and container was accepted")
	}

	// Subject'siz aramalar container/mount satırlarını görmez.
	if _, found, err := store.NewThresholds(pool).Resolve(ctx, c, org, "docker_restart"); err != nil || found {
		t.Fatalf("Resolve(docker_restart) = found %v (err %v): a per-container row must not act as the host's threshold", found, err)
	}
	th := store.NewThresholds(pool)
	got, _ := th.HostSubjectOverrides(ctx, c, "docker_restart")
	if len(got) != 1 || got["web"].WarningLevel != 1 {
		t.Fatalf("docker overrides = %v, want only web", got)
	}
	if disk, _ := th.HostSubjectOverrides(ctx, c, "disk"); len(disk) != 1 || disk["/x"].WarningLevel != 1 {
		t.Fatalf("disk overrides = %v, want only /x (a container must not appear as a mount)", disk)
	}
}
