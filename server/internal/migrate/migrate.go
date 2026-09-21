// Package migrate, gömülü SQL migration'larını PostgreSQL'e uygular.
//
// Neyi garanti eder:
//   - her migration kendi transaction'ında, geçmiş satırıyla birlikte çalışır; böylece
//     bir hata ne yarım uygulanmış bir şema ne de "bitti" kaydı bırakır;
//   - eşzamanlı çalıştırıcılar (birlikte açılan birkaç server kopyası) bir PostgreSQL
//     advisory lock ile sıraya dizilir; böylece her migration tam bir kez çalışır;
//   - uygulandıktan sonra düzenlenmiş bir migration (checksum) ya da veritabanında
//     zaten olandan eski olan bir migration sessizce atlanmak yerine reddedilir ve
//     binary'den yeni bir veritabanı da reddedilir;
//   - tabloları olan ama migration geçmişi olmayan bir veritabanı (eski elle çalıştırılan
//     `psql -f` yöntemiyle oluşturulmuş) asla boş sanılmaz: operatör hangi sürümde olduğunu
//     Baseline ile belirtmelidir.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey migration çalıştırmalarını sıraya dizer ("healthbe" bir int64 olarak).
const advisoryLockKey int64 = 0x6865616c74686265

const historyTable = "healthbeat_migrations"

var (
	ErrLegacyDatabase   = errors.New("the database already has tables but no migration history")
	ErrChecksumMismatch = errors.New("an applied migration was modified after it was applied")
	ErrOutOfOrder       = errors.New("a pending migration is older than one already applied")
	ErrDatabaseNewer    = errors.New("the database has migrations this binary does not know (it was migrated by a newer version)")
	ErrPending          = errors.New("the database has pending migrations")
)

type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

// Status, bir migration'ın durumudur.
type Status struct {
	Migration
	Applied   bool
	AppliedAt time.Time
}

var upFile = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.up\.sql$`)

// Load, her NNNNNN_ad.up.sql dosyasını fsys'ten sürüme göre sıralı okur. Sürümler benzersiz
// olmalı; migration'a benzeyen ama adlandırma düzenine uymayan her şey sessizce yok sayılmak
// yerine hatadır.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var migs []Migration
	seen := map[int64]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		m := upFile.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match NNNNNN_name.up.sql", name)
		}
		version, _ := strconv.ParseInt(m[1], 10, 64)
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migration version %d is used by both %q and %q", version, prev, name)
		}
		seen[version] = name

		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		migs = append(migs, Migration{Version: version, Name: m[2], SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	return migs, nil
}

type Runner struct {
	pool *pgxpool.Pool
	migs []Migration
	// Logf ilerleme mesajlarını alır; nil bunları atar.
	Logf func(format string, args ...any)
}

func New(pool *pgxpool.Pool, fsys fs.FS) (*Runner, error) {
	migs, err := Load(fsys)
	if err != nil {
		return nil, err
	}
	return &Runner{pool: pool, migs: migs}, nil
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// Migrations, bilinen migration'ları sırayla döndürür.
func (r *Runner) Migrations() []Migration { return append([]Migration(nil), r.migs...) }

type applied struct {
	version   int64
	name      string
	checksum  string
	appliedAt time.Time
}

// session, advisory lock'u tutan ayrılmış bir bağlantıdır.
type session struct {
	r    *Runner
	conn *pgxpool.Conn
}

func (r *Runner) lock(ctx context.Context) (*session, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("take the migration lock: %w", err)
	}
	return &session{r: r, conn: conn}, nil
}

func (s *session) release() {
	// Kilit bağlantı başınadır: açıkça bırak, çünkü bağlantı kapanmak yerine havuza geri
	// döner.
	_, _ = s.conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	s.conn.Release()
}

func (s *session) tableExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	// Tüm search_path yerine current_schema(): yedek bir şemadaki (ör. public) aynı adlı tablo
	// bu veritabanının kendi tablosu sayılmamalı.
	err := s.conn.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, name).Scan(&exists)
	return exists, err
}

// appliedRows geçmişi döndürür ya da geçmiş tablosu yoksa (nil, nil). Eski bir veritabanını
// da algılar ve reddeder.
func (s *session) appliedRows(ctx context.Context) ([]applied, error) {
	hasHistory, err := s.tableExists(ctx, historyTable)
	if err != nil {
		return nil, err
	}
	if !hasHistory {
		legacy, err := s.tableExists(ctx, "organizations")
		if err != nil {
			return nil, err
		}
		if legacy {
			return nil, ErrLegacyDatabase
		}
		return nil, nil
	}

	rows, err := s.conn.Query(ctx, `SELECT version, name, checksum, applied_at FROM `+historyTable+` ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []applied
	for rows.Next() {
		var a applied
		if err := rows.Scan(&a.version, &a.name, &a.checksum, &a.appliedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *session) ensureHistoryTable(ctx context.Context) error {
	_, err := s.conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+historyTable+` (
		version    BIGINT PRIMARY KEY,
		name       TEXT NOT NULL,
		checksum   TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	return err
}

// plan, geçmişi bilinen migration'larla karşılaştırır ve bekleyenleri döndürür; ikisi devam
// etmenin güvensiz olduğu biçimde uyuşmuyorsa hata verir.
func (r *Runner) plan(history []applied) ([]Migration, error) {
	known := make(map[int64]Migration, len(r.migs))
	for _, m := range r.migs {
		known[m.Version] = m
	}
	appliedSet := make(map[int64]bool, len(history))
	var maxApplied int64
	for _, a := range history {
		m, ok := known[a.version]
		if !ok {
			return nil, fmt.Errorf("%w: version %d (%s)", ErrDatabaseNewer, a.version, a.name)
		}
		if m.Checksum != a.checksum {
			return nil, fmt.Errorf("%w: %06d_%s (recorded checksum %.12s…, file has %.12s…) — never edit an applied migration, add a new one",
				ErrChecksumMismatch, a.version, a.name, a.checksum, m.Checksum)
		}
		appliedSet[a.version] = true
		if a.version > maxApplied {
			maxApplied = a.version
		}
	}

	var pending []Migration
	for _, m := range r.migs {
		if appliedSet[m.Version] {
			continue
		}
		if m.Version < maxApplied {
			return nil, fmt.Errorf("%w: %06d_%s is pending but version %d is already applied (renumber it after the latest)",
				ErrOutOfOrder, m.Version, m.Name, maxApplied)
		}
		pending = append(pending, m)
	}
	return pending, nil
}

// Up, bekleyen her migration'ı uygular ve uyguladıklarını döndürür.
func (r *Runner) Up(ctx context.Context) ([]Migration, error) {
	s, err := r.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer s.release()

	history, err := s.appliedRows(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := r.plan(history)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	if err := s.ensureHistoryTable(ctx); err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range pending {
		r.logf("migrate: applying %06d_%s", m.Version, m.Name)
		if err := s.apply(ctx, m); err != nil {
			return done, fmt.Errorf("migration %06d_%s failed (nothing from it was kept): %w", m.Version, m.Name, err)
		}
		done = append(done, m)
	}
	return done, nil
}

func (s *session) apply(ctx context.Context, m Migration) error {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // commit edildikten sonra etkisiz

	// Argüman yok => basit protokol; çok ifadeli bir dosyayı kabul eder.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO `+historyTable+` (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequireUpToDate, bilinen her migration uygulanmadıysa başarısız olur. Otomatik migration
// kapalıyken server'ın çalıştırdığı budur: bir binary yazıldığı şemanın dışındaki bir şemaya
// karşı çalışmamalı.
func (r *Runner) RequireUpToDate(ctx context.Context) error {
	s, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer s.release()

	history, err := s.appliedRows(ctx)
	if err != nil {
		return err
	}
	pending, err := r.plan(history)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("%w: %d not applied yet (next: %06d_%s); run `healthbeat-server migrate up` or set AUTO_MIGRATE=true",
			ErrPending, len(pending), pending[0].Version, pending[0].Name)
	}
	return nil
}

// Status, bilinen her migration'ı ve uygulanıp uygulanmadığını listeler.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	s, err := r.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer s.release()

	history, err := s.appliedRows(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := r.plan(history); err != nil { // checksum/daha yeni/sıra dışı sorunları burada da yüzeye çıkar
		return nil, err
	}
	at := make(map[int64]time.Time, len(history))
	for _, a := range history {
		at[a.version] = a.appliedAt
	}
	out := make([]Status, len(r.migs))
	for i, m := range r.migs {
		t, ok := at[m.Version]
		out[i] = Status{Migration: m, Applied: ok, AppliedAt: t}
	}
	return out, nil
}

// Baseline, elle migrate edilmiş bir veritabanını benimser: sürüme kadar (sürüm dahil) bilinen
// her migration'ı HİÇBİRİNİ ÇALIŞTIRMADAN zaten uygulanmış olarak kaydeder. Yalnızca geçmiş
// yokken izinlidir. Sürümü yanlış verirsen server sahip olmadığı bir şemaya inanır; bu yüzden
// çalıştırmadan önce onu kontrol et (değişiklikleri gerçekten mevcut olan son migration).
func (r *Runner) Baseline(ctx context.Context, version int64) ([]Migration, error) {
	s, err := r.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer s.release()

	found := false
	for _, m := range r.migs {
		if m.Version == version {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("version %d is not a known migration", version)
	}

	hasHistory, err := s.tableExists(ctx, historyTable)
	if err != nil {
		return nil, err
	}
	if hasHistory {
		history, err := s.appliedRows(ctx)
		if err != nil {
			return nil, err
		}
		if len(history) > 0 {
			return nil, errors.New("the database already has migration history; baseline only adopts a database that has none")
		}
	}
	if err := s.ensureHistoryTable(ctx); err != nil {
		return nil, err
	}

	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var marked []Migration
	for _, m := range r.migs {
		if m.Version > version {
			break
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+historyTable+` (version, name, checksum) VALUES ($1, $2, $3)`,
			m.Version, m.Name, m.Checksum); err != nil {
			return nil, err
		}
		marked = append(marked, m)
	}
	return marked, tx.Commit(ctx)
}
