package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestPasswordResetsIssueKeepsOnlyTheLatestLink(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	uid := testdb.User(t, pool, "u@x.test", "operator", "correct-horse-battery")
	resets := store.NewPasswordResets(pool)

	if err := resets.Issue(ctx, uid, "hash-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := resets.Issue(ctx, uid, "hash-2", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := resets.Complete(ctx, "hash-1", "$2a$10$x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the superseded link must not work, got %v", err)
	}
	if _, err := resets.Complete(ctx, "hash-2", "$2a$10$y"); err != nil {
		t.Fatalf("the latest link must work: %v", err)
	}
}

func TestPasswordResetsCompleteIsSingleUseAndHonoursExpiry(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	uid := testdb.User(t, pool, "u@x.test", "operator", "correct-horse-battery")
	resets := store.NewPasswordResets(pool)

	if err := resets.Issue(ctx, uid, "expired", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := resets.Complete(ctx, "expired", "$2a$10$x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an expired link must be refused, got %v", err)
	}

	if err := resets.Issue(ctx, uid, "fresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := resets.Complete(ctx, "fresh", "$2a$10$y"); err != nil {
		t.Fatal(err)
	}
	if _, err := resets.Complete(ctx, "fresh", "$2a$10$z"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a link is usable once, got %v", err)
	}
	var hash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, uid).Scan(&hash); err != nil || hash != "$2a$10$y" {
		t.Fatalf("the failed second use must not touch the password: %q, %v", hash, err)
	}
}

// Aynı bağlantıyla eşzamanlı iki tamamlama denemesinden yalnızca biri başarılı olmalı.
func TestPasswordResetsCompleteRacesHaveOneWinner(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	uid := testdb.User(t, pool, "u@x.test", "operator", "correct-horse-battery")
	resets := store.NewPasswordResets(pool)
	if err := resets.Issue(ctx, uid, "one", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := resets.Complete(ctx, "one", "$2a$10$x"); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("a single-use link must have exactly one winner, got %d", wins.Load())
	}
}

func TestPasswordResetsCompleteRevokesEveryRefreshTokenAndClearsTheFlag(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	uid := testdb.User(t, pool, "u@x.test", "operator", "correct-horse-battery")
	if _, err := pool.Exec(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	rt := store.NewRefreshTokens(pool)
	for i := 0; i < 3; i++ {
		if err := rt.Create(ctx, uuid.New(), uid, uuid.New(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	resets := store.NewPasswordResets(pool)
	if err := resets.Issue(ctx, uid, "h", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	u, err := resets.Complete(ctx, "h", "$2a$10$new")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != uid || u.MustChangePassword || u.PasswordHash != "$2a$10$new" {
		t.Fatalf("returned user = %+v", u)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, uid).Scan(&live); err != nil || live != 0 {
		t.Fatalf("live refresh tokens after reset = %d, %v; want 0", live, err)
	}
}

func TestPasswordResetsPurgeExpired(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := testdb.User(t, pool, "a@x.test", "operator", "correct-horse-battery")
	b := testdb.User(t, pool, "b@x.test", "operator", "correct-horse-battery")
	c := testdb.User(t, pool, "c@x.test", "operator", "correct-horse-battery")
	resets := store.NewPasswordResets(pool)
	_ = resets.Issue(ctx, a, "long-expired", time.Now().Add(-48*time.Hour)) // temizlenir
	_ = resets.Issue(ctx, b, "just-expired", time.Now().Add(-time.Minute))  // bir gün dolmadı: kalır
	_ = resets.Issue(ctx, c, "live", time.Now().Add(time.Hour))             // kalır
	d := testdb.User(t, pool, "d@x.test", "operator", "correct-horse-battery")
	_ = resets.Issue(ctx, d, "used-long-ago", time.Now().Add(time.Hour)) // süresi dolmadı ama iki gün önce kullanıldı: temizlenir
	if _, err := pool.Exec(ctx, `UPDATE password_reset_tokens SET used_at = now() - interval '2 days' WHERE token_hash = 'used-long-ago'`); err != nil {
		t.Fatal(err)
	}
	n, err := resets.PurgeExpired(ctx)
	if err != nil || n != 2 {
		t.Fatalf("PurgeExpired = %d, %v; want 2", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("remaining = %d, %v; want 2", left, err)
	}
}
