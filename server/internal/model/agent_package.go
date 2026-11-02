package model

import (
	"time"

	"github.com/google/uuid"
)

// Agent paketinin biçimleri (agent_packages.format).
const (
	AgentPackageDeb     = "deb"
	AgentPackageRPM     = "rpm"
	AgentPackageTarball = "tar.gz"
)

// AgentPackage, server'a yüklenmiş bir agent paket dosyasıdır (bkz. migration 000010). Dosyanın kendisi diskte
// (internal/agentpkg), kaydı veritabanındadır. Filename server'ın ürettiği release adıdır (diskte ve indirme adresinde),
// OriginalFilename yüklenen dosyanın adı.
type AgentPackage struct {
	ID               uuid.UUID  `json:"id"`
	Version          string     `json:"version"`
	Filename         string     `json:"filename"`
	OriginalFilename string     `json:"original_filename"`
	Arch             string     `json:"arch"`   // amd64 | arm64
	Format           string     `json:"format"` // deb | rpm | tar.gz
	SizeBytes        int64      `json:"size_bytes"`
	SHA256           string     `json:"sha256"`
	UploadedBy       *uuid.UUID `json:"uploaded_by,omitempty"`
	UploadedByEmail  string     `json:"uploaded_by_email,omitempty"`
	UploadedAt       time.Time  `json:"uploaded_at"`
	DownloadCount    int64      `json:"download_count"`
	LastDownloadedAt *time.Time `json:"last_downloaded_at,omitempty"`
}

// AgentPackageDownload, bir paketin bir kez indirilmesidir.
type AgentPackageDownload struct {
	ClientIP     string    `json:"client_ip"`
	UserAgent    string    `json:"user_agent"`
	DownloadedAt time.Time `json:"downloaded_at"`
}
