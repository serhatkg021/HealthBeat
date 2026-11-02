package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/version"
)

// Agent paketi hataları: aynı sürüm, mimari ve biçimde paket zaten kayıtlı; alanlar migration 000010'daki kısıtlara
// uymuyor; paket yok.
var (
	ErrAgentPackageExists  = reason(ErrConflict, "agent package with this version, arch and format already exists")
	ErrAgentPackageInvalid = reason(ErrConflict, "invalid agent package")
	ErrAgentPackageMissing = reason(ErrNotFound, "agent package does not exist")
)

// maxDownloadUserAgent, indirme günlüğüne yazılan User-Agent'ın üst sınırıdır (agent_package_downloads kısıtı).
const maxDownloadUserAgent = 256

// AgentPackages, server'a yüklenen agent paketlerinin kayıtlarını ve indirme günlüğünü yönetir. Dosyaların kendisi
// internal/agentpkg'dedir; Create ve Delete dosya işlemini kaydın transaction'ı içinde çağırır.
type AgentPackages struct {
	pool *pgxpool.Pool
}

func NewAgentPackages(pool *pgxpool.Pool) *AgentPackages { return &AgentPackages{pool: pool} }

const agentPackageSelect = `SELECT p.id, p.version, p.filename, p.original_filename, p.arch, p.format, p.size_bytes, p.sha256, p.uploaded_by,
	COALESCE(u.email, ''), p.uploaded_at, p.download_count, p.last_downloaded_at
	FROM agent_packages p LEFT JOIN users u ON u.id = p.uploaded_by`

func scanAgentPackage(row interface{ Scan(...any) error }) (model.AgentPackage, error) {
	var p model.AgentPackage
	err := row.Scan(&p.ID, &p.Version, &p.Filename, &p.OriginalFilename, &p.Arch, &p.Format, &p.SizeBytes, &p.SHA256, &p.UploadedBy,
		&p.UploadedByEmail, &p.UploadedAt, &p.DownloadCount, &p.LastDownloadedAt)
	return p, err
}

// List, bütün paketleri döndürür: en yeni sürüm önce (SemVer sırası), sürüm içinde dosya adına göre.
func (s *AgentPackages) List(ctx context.Context) ([]model.AgentPackage, error) {
	rows, err := s.pool.Query(ctx, agentPackageSelect)
	if err != nil {
		return nil, err
	}
	out, err := collect(rows, scanAgentPackage)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b model.AgentPackage) int {
		if c := version.Compare(b.Version, a.Version); c != 0 {
			return c
		}
		return strings.Compare(a.Filename, b.Filename)
	})
	return out, nil
}

// Get, paketi kimliğiyle döndürür.
func (s *AgentPackages) Get(ctx context.Context, id uuid.UUID) (model.AgentPackage, error) {
	p, err := scanAgentPackage(s.pool.QueryRow(ctx, agentPackageSelect+` WHERE p.id = $1`, id))
	if isNoRows(err) {
		return p, ErrAgentPackageMissing
	}
	return p, err
}

// GetFile, paketi sürümü ve dosya adıyla döndürür (indirme ucu).
func (s *AgentPackages) GetFile(ctx context.Context, ver, filename string) (model.AgentPackage, error) {
	p, err := scanAgentPackage(s.pool.QueryRow(ctx, agentPackageSelect+` WHERE p.version = $1 AND p.filename = $2`, ver, filename))
	if isNoRows(err) {
		return p, ErrAgentPackageMissing
	}
	return p, err
}

// Create, paketin kaydını ekler ve aynı transaction içinde place'i çağırır (dosyayı yerine koyar): place başarısız
// olursa kayıt eklenmez, kayıt eklenemezse place hiç çağrılmaz. Aynı sürüm, mimari ve biçim (ya da dosya adı) zaten
// kayıtlıysa ErrAgentPackageExists.
// ID, UploadedAt ve indirme alanları yok sayılır.
func (s *AgentPackages) Create(ctx context.Context, p model.AgentPackage, place func() error) (model.AgentPackage, error) {
	var id uuid.UUID
	err := InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO agent_packages (version, filename, original_filename, arch, format, size_bytes, sha256, uploaded_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			p.Version, p.Filename, p.OriginalFilename, p.Arch, p.Format, p.SizeBytes, p.SHA256, p.UploadedBy).Scan(&id); err != nil {
			switch pgErrorCode(err) {
			case pgUniqueViolation:
				return ErrAgentPackageExists
			case pgCheckViolation:
				return ErrAgentPackageInvalid
			}
			return err
		}
		return place()
	})
	if err != nil {
		return model.AgentPackage{}, err
	}
	return s.Get(ctx, id)
}

// Delete, paketin kaydını (ve indirme günlüğünü) siler ve aynı transaction içinde remove'u çağırır (dosyayı siler):
// remove başarısız olursa kayıt kalır. Silinen paketi döndürür.
func (s *AgentPackages) Delete(ctx context.Context, id uuid.UUID, remove func(model.AgentPackage) error) (model.AgentPackage, error) {
	var p model.AgentPackage
	err := InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		p, err = scanAgentPackage(tx.QueryRow(ctx, agentPackageSelect+` WHERE p.id = $1 FOR UPDATE OF p`, id))
		if isNoRows(err) {
			return ErrAgentPackageMissing
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_packages WHERE id = $1`, id); err != nil {
			return err
		}
		return remove(p)
	})
	return p, err
}

// RecordDownload, paketin bir kez indirildiğini yazar: sayaç ve son indirme zamanı güncellenir, günlüğe satır eklenir.
// Paket bu arada silindiyse ErrAgentPackageMissing.
func (s *AgentPackages) RecordDownload(ctx context.Context, id uuid.UUID, clientIP, userAgent string) error {
	if len(userAgent) > maxDownloadUserAgent {
		userAgent = strings.ToValidUTF8(userAgent[:maxDownloadUserAgent], "")
	}
	return InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var at time.Time
		err := tx.QueryRow(ctx,
			`UPDATE agent_packages SET download_count = download_count + 1, last_downloaded_at = now()
			 WHERE id = $1 RETURNING last_downloaded_at`, id).Scan(&at)
		if isNoRows(err) {
			return ErrAgentPackageMissing
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO agent_package_downloads (package_id, client_ip, user_agent, downloaded_at) VALUES ($1, $2, $3, $4)`,
			id, clientIP, userAgent, at)
		return err
	})
}

// Downloads, paketin son limit indirmesini döndürür (en yeni önce).
func (s *AgentPackages) Downloads(ctx context.Context, id uuid.UUID, limit int) ([]model.AgentPackageDownload, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT client_ip, user_agent, downloaded_at FROM agent_package_downloads
		 WHERE package_id = $1 ORDER BY downloaded_at DESC, id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row interface{ Scan(...any) error }) (model.AgentPackageDownload, error) {
		var d model.AgentPackageDownload
		err := row.Scan(&d.ClientIP, &d.UserAgent, &d.DownloadedAt)
		return d, err
	})
}

// PurgeDownloadsOlderThan, cutoff'tan eski indirme günlüğü satırlarını parti parti siler (denetim kaydı saklama süresi;
// bkz. app_settings.audit_retention_days). Paketlerin sayaçlarına dokunmaz.
func (s *AgentPackages) PurgeDownloadsOlderThan(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	return purgeInBatches(ctx, s.pool,
		`DELETE FROM agent_package_downloads WHERE id IN (
		     SELECT id FROM agent_package_downloads WHERE downloaded_at < $1 ORDER BY downloaded_at LIMIT $2)`,
		cutoff, batchSize)
}
