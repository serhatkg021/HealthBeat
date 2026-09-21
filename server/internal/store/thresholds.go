package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

type Thresholds struct {
	pool *pgxpool.Pool
}

func NewThresholds(pool *pgxpool.Pool) *Thresholds {
	return &Thresholds{pool: pool}
}

// İki tablo: threshold_defaults (genel ya da organizasyon varsayılanı; organizasyon ağacında alt dallara miras kalır) ve
// host_custom_thresholds (tek bir sunucuya özel). Go tarafında ikisi de model.ThresholdConfig olarak görünür.
const (
	defaultColumns = `id, organization_id, NULL::uuid, metric_type, ''::text, warning_level, critical_level, created_at, updated_at`
	hostColumnsThr = `id, NULL::uuid, host_id, metric_type, COALESCE(subject, ''), warning_level, critical_level, created_at, updated_at`
)

func scanThreshold(row interface{ Scan(...any) error }) (model.ThresholdConfig, error) {
	var t model.ThresholdConfig
	err := row.Scan(&t.ID, &t.OrganizationID, &t.HostID, &t.MetricType, &t.Subject, &t.WarningLevel,
		&t.CriticalLevel, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// orgChainCTE, $N numaralı organizasyondan köke doğru zinciri (en yakın önce, depth 0) verir.
func orgChainCTE(param int) string {
	return fmt.Sprintf(`WITH RECURSIVE chain AS (
		SELECT id, parent_organization_id, 0 AS depth FROM organizations WHERE id = $%d
		UNION ALL
		SELECT o.id, o.parent_organization_id, c.depth + 1 FROM organizations o JOIN chain c ON o.id = c.parent_organization_id
	)`, param)
}

// CreateThresholdParams, bir varsayılan eşiktir (OrganizationID nil = genel). Sunucuya özel eşikler
// SetHostOverrides ile yazılır.
type CreateThresholdParams struct {
	OrganizationID *uuid.UUID
	MetricType     string
	WarningLevel   float64
	CriticalLevel  float64
}

func (s *Thresholds) Create(ctx context.Context, p CreateThresholdParams) (model.ThresholdConfig, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO threshold_defaults (organization_id, metric_type, warning_level, critical_level)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+defaultColumns,
		p.OrganizationID, p.MetricType, p.WarningLevel, p.CriticalLevel,
	)
	t, err := scanThreshold(row)
	if err != nil {
		switch pgErrorCode(err) {
		case pgUniqueViolation:
			return model.ThresholdConfig{}, fmt.Errorf("%w: bu kapsam ve metrik için zaten bir eşik var", ErrConflict)
		case pgForeignKeyViolation:
			return model.ThresholdConfig{}, fmt.Errorf("%w: organizasyon yok", ErrNotFound)
		case pgCheckViolation:
			return model.ThresholdConfig{}, fmt.Errorf("%w: warning_level, critical_level'dan büyük olamaz", ErrConflict)
		}
		return model.ThresholdConfig{}, err
	}
	return t, nil
}

func (s *Thresholds) GetByID(ctx context.Context, id uuid.UUID) (model.ThresholdConfig, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+defaultColumns+` FROM threshold_defaults WHERE id = $1`, id)
	t, err := scanThreshold(row)
	if err != nil {
		if isNoRows(err) {
			return model.ThresholdConfig{}, ErrNotFound
		}
		return model.ThresholdConfig{}, err
	}
	return t, nil
}

func (s *Thresholds) List(ctx context.Context) ([]model.ThresholdConfig, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+defaultColumns+` FROM threshold_defaults ORDER BY metric_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThresholds(rows)
}

// ListForOrganizations, genel varsayılanları ve orgIDs'e ait varsayılanları döndürür — bir org_admin'in görebildiği küme.
func (s *Thresholds) ListForOrganizations(ctx context.Context, orgIDs []uuid.UUID) ([]model.ThresholdConfig, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+defaultColumns+` FROM threshold_defaults
		 WHERE organization_id IS NULL OR organization_id = ANY($1)
		 ORDER BY metric_type`,
		orgIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThresholds(rows)
}

func scanThresholds(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.ThresholdConfig, error) {
	configs := []model.ThresholdConfig{}
	for rows.Next() {
		t, err := scanThreshold(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, t)
	}
	return configs, rows.Err()
}

func (s *Thresholds) Update(ctx context.Context, id uuid.UUID, warningLevel, criticalLevel *float64) (model.ThresholdConfig, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE threshold_defaults
		 SET warning_level = COALESCE($2, warning_level),
		     critical_level = COALESCE($3, critical_level),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+defaultColumns,
		id, warningLevel, criticalLevel,
	)
	t, err := scanThreshold(row)
	if err != nil {
		if isNoRows(err) {
			return model.ThresholdConfig{}, ErrNotFound
		}
		if pgErrorCode(err) == pgCheckViolation {
			return model.ThresholdConfig{}, fmt.Errorf("%w: warning_level, critical_level'dan büyük olamaz", ErrConflict)
		}
		return model.ThresholdConfig{}, err
	}
	return t, nil
}

func (s *Thresholds) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM threshold_defaults WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Resolve, bir host+metrik için uygulanabilir en özel eşiği seçer: sunucuya özel eşik > organizasyon varsayılanı >
// üst organizasyonların varsayılanı (en yakın önce) > genel varsayılan. Bu metrik için hiçbir şey
// yapılandırılmamışsa found hata değil false olur.
func (s *Thresholds) Resolve(ctx context.Context, hostID, orgID uuid.UUID, metricType string) (model.ThresholdConfig, bool, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+hostColumnsThr+` FROM host_custom_thresholds
		 WHERE host_id = $1 AND metric_type = $2 AND subject IS NULL`,
		hostID, metricType,
	)
	t, err := scanThreshold(row)
	if err == nil {
		return t, true, nil
	}
	if !isNoRows(err) {
		return model.ThresholdConfig{}, false, err
	}
	return s.resolveDefault(ctx, orgID, metricType)
}

// resolveDefault, organizasyon zincirinde (kendisi, üst şirketler) en yakın varsayılanı, yoksa genel olanı seçer.
func (s *Thresholds) resolveDefault(ctx context.Context, orgID uuid.UUID, metricType string) (model.ThresholdConfig, bool, error) {
	row := s.pool.QueryRow(ctx,
		orgChainCTE(1)+`
		 SELECT d.id, d.organization_id, NULL::uuid, d.metric_type, ''::text, d.warning_level, d.critical_level, d.created_at, d.updated_at
		 FROM threshold_defaults d LEFT JOIN chain c ON c.id = d.organization_id
		 WHERE d.metric_type = $2 AND (d.organization_id IS NULL OR c.id IS NOT NULL)
		 ORDER BY (d.organization_id IS NULL), c.depth
		 LIMIT 1`,
		orgID, metricType,
	)
	t, err := scanThreshold(row)
	if err != nil {
		if isNoRows(err) {
			return model.ThresholdConfig{}, false, nil
		}
		return model.ThresholdConfig{}, false, err
	}
	return t, true, nil
}

// HostOverrides, host'ın kendi (özel) eşiklerini metrik türüne göre döndürür.
func (s *Thresholds) HostOverrides(ctx context.Context, hostID uuid.UUID) (map[string]model.ThresholdLevels, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT metric_type, warning_level, critical_level FROM host_custom_thresholds WHERE host_id = $1 AND subject IS NULL`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ThresholdLevels{}
	for rows.Next() {
		var metricType string
		var levels model.ThresholdLevels
		if err := rows.Scan(&metricType, &levels.WarningLevel, &levels.CriticalLevel); err != nil {
			return nil, err
		}
		out[metricType] = levels
	}
	return out, rows.Err()
}

// DefaultsFor, metrik türü başına, özel eşiği olmayan bir orgID host'ının ne alacağını
// döndürür: organizasyon zincirindeki en yakın varsayılan, yoksa genel olan. İkisi de olmayan
// metrikler yoktur (onlar için alert yok).
func (s *Thresholds) DefaultsFor(ctx context.Context, orgID uuid.UUID) (map[string]model.ThresholdLevels, error) {
	rows, err := s.pool.Query(ctx,
		orgChainCTE(1)+`
		 SELECT d.metric_type, d.warning_level, d.critical_level
		 FROM threshold_defaults d LEFT JOIN chain c ON c.id = d.organization_id
		 WHERE d.organization_id IS NULL OR c.id IS NOT NULL
		 ORDER BY d.metric_type, (d.organization_id IS NULL), c.depth`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ThresholdLevels{}
	for rows.Next() {
		var metricType string
		var levels model.ThresholdLevels
		if err := rows.Scan(&metricType, &levels.WarningLevel, &levels.CriticalLevel); err != nil {
			return nil, err
		}
		if _, seen := out[metricType]; !seen { // en özel satır önce sıralanır
			out[metricType] = levels
		}
	}
	return out, rows.Err()
}

// SetHostOverrides bir host'ın özel eşiklerini tek transaction'da uygular: nil değer o
// metriğin özel eşiğini kaldırır (host varsayılana döner), bir çift onu oluşturur ya da
// değiştirir; overrides içinde olmayan metriklere dokunulmaz.
func (s *Thresholds) SetHostOverrides(ctx context.Context, hostID uuid.UUID, overrides model.ThresholdOverrides, mounts model.MountThresholds, containers model.ContainerThresholds) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // commit edildikten sonra etkisiz
	if err := applyHostOverrides(ctx, tx, hostID, overrides, mounts, containers); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applyHostOverrides host oluşturmayla paylaşılır; böylece yeni bir host ve özel
// eşikleri atomik yazılır. Anahtarlar belirlilik için sıralı uygulanır.
// mounts mount başına disk eşikleridir (subject'i mount olan disk satırları olarak saklanır).
func applyHostOverrides(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, overrides model.ThresholdOverrides, mounts model.MountThresholds, containers model.ContainerThresholds) error {
	metricTypes := make([]string, 0, len(overrides))
	for metricType := range overrides {
		metricTypes = append(metricTypes, metricType)
	}
	sort.Strings(metricTypes)
	for _, metricType := range metricTypes {
		if err := applyOne(ctx, tx, hostID, metricType, "", overrides[metricType]); err != nil {
			return err
		}
	}

	mountPaths := make([]string, 0, len(mounts))
	for mount := range mounts {
		mountPaths = append(mountPaths, mount)
	}
	sort.Strings(mountPaths)
	for _, mount := range mountPaths {
		if err := applyOne(ctx, tx, hostID, model.MetricTypeDisk, mount, mounts[mount]); err != nil {
			return err
		}
	}

	names := make([]string, 0, len(containers))
	for name := range containers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := applyOne(ctx, tx, hostID, model.MetricTypeDockerRestart, name, containers[name]); err != nil {
			return err
		}
	}
	return nil
}

// applyOne, bir host eşik satırını yazar (levels != nil) ya da kaldırır (levels == nil). subject "" = sunucu geneli (NULL).
func applyOne(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, metricType, subject string, levels *model.ThresholdLevels) error {
	var subj *string
	if subject != "" {
		subj = &subject
	}
	if levels == nil {
		_, err := tx.Exec(ctx,
			`DELETE FROM host_custom_thresholds WHERE host_id = $1 AND metric_type = $2 AND subject IS NOT DISTINCT FROM $3`, hostID, metricType, subj)
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT ON CONSTRAINT host_custom_thresholds_key
		 DO UPDATE SET warning_level = EXCLUDED.warning_level, critical_level = EXCLUDED.critical_level, updated_at = now()`,
		hostID, metricType, subj, levels.WarningLevel, levels.CriticalLevel)
	if err != nil {
		switch pgErrorCode(err) {
		case pgForeignKeyViolation:
			return fmt.Errorf("%w: sunucu yok", ErrNotFound)
		case pgCheckViolation:
			return fmt.Errorf("%w: %q için geçersiz eşik", ErrConflict, metricType)
		}
		return err
	}
	return nil
}

// HostSubjectOverrides, host'ın belirli bir metriğe ait subject'li (mount yolu / container adı)
// eşiklerini subject'e göre döndürür.
func (s *Thresholds) HostSubjectOverrides(ctx context.Context, hostID uuid.UUID, metricType string) (map[string]model.ThresholdLevels, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT subject, warning_level, critical_level FROM host_custom_thresholds
		 WHERE host_id = $1 AND metric_type = $2 AND subject IS NOT NULL`, hostID, metricType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ThresholdLevels{}
	for rows.Next() {
		var subject string
		var levels model.ThresholdLevels
		if err := rows.Scan(&subject, &levels.WarningLevel, &levels.CriticalLevel); err != nil {
			return nil, err
		}
		out[subject] = levels
	}
	return out, rows.Err()
}

// SubjectThresholds, bir host'ın bir metriği için geçerli eşiklerdir: host genelindeki eşik
// (kendi satırı, yoksa organizasyonunki, yoksa global) ve kendi eşiği olan subject'ler (disk için
// mount yolu, docker_restart için container adı).
type SubjectThresholds struct {
	Base       *model.ThresholdConfig
	PerSubject map[string]model.ThresholdConfig
}

// For, subject için geçerli eşiği ve hiç eşik olup olmadığını döndürür.
func (d SubjectThresholds) For(subject string) (model.ThresholdConfig, bool) {
	if t, ok := d.PerSubject[subject]; ok {
		return t, true
	}
	if d.Base != nil {
		return *d.Base, true
	}
	return model.ThresholdConfig{}, false
}

// ResolveSubjects, host'a uygulanabilecek tüm eşikleri yükler: sunucu geneli (kendi satırı, yoksa organizasyon
// zincirindeki en yakın varsayılan, yoksa genel) ve sunucunun subject'li eşikleri.
func (s *Thresholds) ResolveSubjects(ctx context.Context, hostID, orgID uuid.UUID, metricType string) (SubjectThresholds, error) {
	out := SubjectThresholds{PerSubject: map[string]model.ThresholdConfig{}}

	rows, err := s.pool.Query(ctx,
		`SELECT `+hostColumnsThr+` FROM host_custom_thresholds WHERE host_id = $1 AND metric_type = $2`, hostID, metricType)
	if err != nil {
		return SubjectThresholds{}, err
	}
	own, err := scanThresholds(rows)
	rows.Close()
	if err != nil {
		return SubjectThresholds{}, err
	}
	for _, t := range own {
		if t.Subject != "" {
			out.PerSubject[t.Subject] = t
			continue
		}
		t := t
		out.Base = &t
	}
	if out.Base == nil {
		def, found, err := s.resolveDefault(ctx, orgID, metricType)
		if err != nil {
			return SubjectThresholds{}, err
		}
		if found {
			out.Base = &def
		}
	}
	return out, nil
}
