package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"healthbeat-server/internal/agentpkg"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

const (
	// maxAgentPackageBytes, tek bir paket dosyasının üst sınırıdır (release paketleri 3–9 MB).
	maxAgentPackageBytes = 50 << 20
	// maxAgentUploadBytes, bir yükleme isteğinin tamamının üst sınırıdır: bir paket, form alanları ve multipart ekleri.
	maxAgentUploadBytes = maxAgentPackageBytes + 1<<20
	// maxAgentFieldBytes, yüklemedeki bir form alanının (sürüm, mimari) üst sınırıdır.
	maxAgentFieldBytes = 256
	// maxOriginalFilename, kayda yazılan yüklenen dosya adının üst sınırıdır (agent_packages_original_filename_chk).
	maxOriginalFilename = 255
	// agentUploadTimeout / agentDownloadTimeout, server'ın genel okuma/yazma sürelerinin yerine geçer: yavaş bir hatta
	// büyük bir yükleme ya da indirme onlara sığmaz.
	agentUploadTimeout   = 15 * time.Minute
	agentDownloadTimeout = 30 * time.Minute
	// AgentDownloadPath, indirme ucunun kökü: /agent/download/{sürüm}/{dosya}.
	AgentDownloadPath = "/agent/download/"

	defaultAgentDownloadsLimit = 50
	maxAgentDownloadsLimit     = 500
)

type agentPackageListResponse struct {
	// StorageAvailable, paket dizininin kullanılabilir olduğunu söyler (AGENT_PACKAGES_DIR); false ise yükleme ve indirme
	// kapalıdır, kayıtlar yine listelenir.
	StorageAvailable bool                 `json:"storage_available"`
	DownloadPath     string               `json:"download_path"`
	Packages         []model.AgentPackage `json:"packages"`
}

// GET /api/v1/agent-packages
func (d *Deps) handleListAgentPackages(w http.ResponseWriter, r *http.Request) error {
	list, err := d.agentPackages.List(r.Context())
	if err != nil {
		return serverErr("agent paketleri okunamadı", "list agent packages", err)
	}
	if list == nil {
		list = []model.AgentPackage{}
	}
	writeJSON(w, http.StatusOK, agentPackageListResponse{
		StorageAvailable: d.agentPackageDir != nil,
		DownloadPath:     AgentDownloadPath,
		Packages:         list,
	})
	return nil
}

// POST /api/v1/agent-packages (multipart/form-data): önce "version" ve "arch" alanları, sonra tek bir dosya parçası
// ("file"). Biçim dosyanın ilk baytlarından anlaşılır; dosya diskte ve indirme adresinde release adıyla durur
// (agentpkg.FileName), yüklenen ad original_filename'e yazılır. Yanıt 201 ve paket kaydıdır.
func (d *Deps) handleUploadAgentPackage(w http.ResponseWriter, r *http.Request) error {
	if d.agentPackageDir == nil {
		return newError(http.StatusServiceUnavailable, "agent paket dizini kullanılamıyor (AGENT_PACKAGES_DIR)")
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(agentUploadTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(agentUploadTimeout))
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentUploadBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		return badRequest("istek multipart/form-data olmalı")
	}
	fields := map[string]string{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return badRequest("yüklenecek dosya yok")
		}
		if err != nil {
			return uploadReadError(err)
		}
		if part.FileName() == "" { // form alanı
			v, err := io.ReadAll(io.LimitReader(part, maxAgentFieldBytes+1))
			_ = part.Close()
			if err != nil {
				return uploadReadError(err)
			}
			if len(v) > maxAgentFieldBytes {
				return fieldError400(part.FormName(), "çok uzun")
			}
			fields[part.FormName()] = strings.TrimSpace(string(v))
			continue
		}
		defer part.Close()
		return d.uploadAgentPackage(w, r, part, fields)
	}
}

// uploadAgentPackage, dosya parçasını sürüm ve mimariyle yükler.
func (d *Deps) uploadAgentPackage(w http.ResponseWriter, r *http.Request, part *multipart.Part, fields map[string]string) error {
	ver, arch := fields["version"], fields["arch"]
	switch {
	case ver == "":
		return fieldError400("version", "sürüm gerekli (dosyadan önce gönderilir)")
	case !agentpkg.ValidVersion(ver):
		return fieldError400("version", "sürüm 1.2.0 ya da 1.2.0-rc.1 biçiminde olmalı")
	case arch == "":
		return fieldError400("arch", "mimari gerekli (dosyadan önce gönderilir)")
	case !agentpkg.ValidArch(arch):
		return fieldError400("arch", "mimari amd64 ya da arm64 olmalı")
	}
	original := originalFilename(part.FileName())

	staged, err := d.agentPackageDir.Stage(part, maxAgentPackageBytes)
	switch {
	case errors.Is(err, agentpkg.ErrTooLarge):
		return newError(http.StatusRequestEntityTooLarge, fmt.Sprintf("dosya en fazla %d MB olabilir", maxAgentPackageBytes>>20))
	case errors.Is(err, agentpkg.ErrEmpty):
		return badRequest("dosya boş")
	case errors.Is(err, agentpkg.ErrUnknownFormat):
		return badRequest("agent paketi değil: dosya .deb, .rpm ya da .tar.gz olmalı")
	case err != nil:
		if r.Context().Err() != nil {
			return uploadReadError(err)
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return uploadReadError(err)
		}
		return serverErr("dosya kaydedilemedi", "agent package upload: stage", err)
	}
	defer staged.Discard()

	info := agentpkg.Info{Version: ver, Arch: arch, Format: staged.Format}
	name, err := agentpkg.FileName(info)
	if err != nil { // yukarıda denetlendi
		return badRequest("geçersiz sürüm ya da mimari")
	}
	var uploader *uuid.UUID
	if id, ok := userIDFromContext(r.Context()); ok {
		uploader = &id
	}
	p := model.AgentPackage{Version: ver, Filename: name, OriginalFilename: original, Arch: arch, Format: staged.Format,
		SizeBytes: staged.Size, SHA256: staged.SHA256, UploadedBy: uploader}
	created, err := d.agentPackages.Create(r.Context(), p, func() error { return staged.Place(ver, name) })
	switch {
	case errors.Is(err, store.ErrAgentPackageExists):
		return conflict(fmt.Sprintf("%s sürümünün %s %s paketi zaten yüklü; yerine koymak için önce silin", ver, arch, formatLabel(staged.Format)))
	case err != nil:
		return serverErr("dosya kaydedilemedi", "agent package upload: save", err)
	}
	id := created.ID.String()
	d.logAudit(r, "agent_package.upload", "agent_package", &id, map[string]any{
		"filename": created.Filename, "original_filename": created.OriginalFilename, "version": created.Version,
		"size_bytes": created.SizeBytes, "sha256": created.SHA256,
	})
	writeJSON(w, http.StatusCreated, created)
	return nil
}

// uploadReadError, istek gövdesi okunamadığında dönen hatadır (sınır aşıldı ya da bağlantı koptu).
func uploadReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return newError(http.StatusRequestEntityTooLarge, fmt.Sprintf("dosya en fazla %d MB olabilir", maxAgentPackageBytes>>20))
	}
	return badRequest("yükleme okunamadı")
}

// originalFilename, yüklenen dosyanın adını kayda uygun hale getirir: yol kısmı atılır, geçersiz UTF-8 ve denetim
// karakterleri temizlenir, en fazla maxOriginalFilename bayt.
func originalFilename(name string) string {
	name = strings.ToValidUTF8(filepath.Base(strings.ReplaceAll(name, "\\", "/")), "")
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	for len(name) > maxOriginalFilename {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" || name == "." || name == "/" {
		return "paket"
	}
	return name
}

func formatLabel(format string) string { return "." + format }

// DELETE /api/v1/agent-packages/{id}
func (d *Deps) handleDeleteAgentPackage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz paket kimliği")
	if err != nil {
		return err
	}
	deleted, err := d.agentPackages.Delete(r.Context(), id, func(p model.AgentPackage) error {
		if d.agentPackageDir == nil { // dizin yoksa dosyaya ulaşılamaz; kayıt yine silinir
			return nil
		}
		return d.agentPackageDir.Remove(p.Version, p.Filename)
	})
	switch {
	case errors.Is(err, store.ErrAgentPackageMissing):
		return notFound("paket bulunamadı")
	case err != nil:
		return serverErr("paket silinemedi", "delete agent package", err)
	}
	target := id.String()
	d.logAudit(r, "agent_package.delete", "agent_package", &target, map[string]any{
		"filename": deleted.Filename, "version": deleted.Version,
	})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// GET /api/v1/agent-packages/{id}/downloads?limit=
func (d *Deps) handleListAgentPackageDownloads(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz paket kimliği")
	if err != nil {
		return err
	}
	limit := defaultAgentDownloadsLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxAgentDownloadsLimit {
			return badRequest(fmt.Sprintf("limit 1 ile %d arasında olmalı", maxAgentDownloadsLimit))
		}
		limit = n
	}
	if _, err := d.agentPackages.Get(r.Context(), id); errors.Is(err, store.ErrAgentPackageMissing) {
		return notFound("paket bulunamadı")
	} else if err != nil {
		return serverErr("indirmeler okunamadı", "agent package downloads: get", err)
	}
	list, err := d.agentPackages.Downloads(r.Context(), id, limit)
	if err != nil {
		return serverErr("indirmeler okunamadı", "agent package downloads", err)
	}
	if list == nil {
		list = []model.AgentPackageDownload{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": list})
	return nil
}

// agentDownloadsEnabled, indirme ucunun açık olup olmadığıdır (ayar; ayarlar bağlanmamışsa varsayılan açık).
func (d *Deps) agentDownloadsEnabled() bool {
	return d.agentPackageDir != nil && (d.appSettings == nil || d.appSettings.Current().AgentDownloadEnabled)
}

// GET /agent/download/{version}/{filename} — kimlik doğrulamasız. Kapalıysa, dosya adı tanınmıyorsa ya da paket yoksa
// hep aynı 404 döner. Range desteklenir; indirme sayımı için bkz. countsAsDownload.
func (d *Deps) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	if ok, retry := d.agentDownloads.Allow(remoteIP(r)); !ok {
		writeTooManyRequests(w, retry)
		return
	}
	ver, name := r.PathValue("version"), r.PathValue("filename")
	notFound := func() { writeError(w, http.StatusNotFound, "paket bulunamadı") }
	if !d.agentDownloadsEnabled() {
		notFound()
		return
	}
	if info, err := agentpkg.Parse(name); err != nil || info.Version != ver {
		notFound()
		return
	}
	p, err := d.agentPackages.GetFile(r.Context(), ver, name)
	if errors.Is(err, store.ErrAgentPackageMissing) {
		notFound()
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "agent download: lookup", "filename", name, "err", err)
		writeError(w, http.StatusInternalServerError, "paket okunamadı")
		return
	}
	f, err := d.agentPackageDir.Open(p.Version, p.Filename)
	if err != nil {
		slog.ErrorContext(r.Context(), "agent download: package file is missing or unreadable", "filename", name, "err", err)
		notFound()
		return
	}
	defer f.Close()

	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(agentDownloadTimeout))
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, p.Filename))
	h.Set("ETag", `"`+p.SHA256+`"`)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	sw := &statusWriter{ResponseWriter: w}
	http.ServeContent(sw, r, p.Filename, p.UploadedAt, f)

	if r.Method == http.MethodGet && countsAsDownload(sw.status, r.Header.Get("Range")) {
		// İstemci bağlantıyı kapatmış olsa da kayıt yazılır.
		if err := d.agentPackages.RecordDownload(context.WithoutCancel(r.Context()), p.ID, remoteIP(r), r.UserAgent()); err != nil {
			slog.ErrorContext(r.Context(), "agent download: record", "filename", name, "err", err)
		}
	}
}

// countsAsDownload, bir yanıtın yeni bir indirme sayılıp sayılmadığıdır: tam dosya (200) ya da baştan başlayan parça
// (206, bytes=0-). Yarıda kalanı sürdüren, koşullu (304) ve hatalı istekler sayılmaz.
func countsAsDownload(status int, rangeHeader string) bool {
	switch status {
	case http.StatusOK:
		return true
	case http.StatusPartialContent:
		spec, ok := strings.CutPrefix(strings.TrimSpace(rangeHeader), "bytes=")
		return ok && strings.HasPrefix(strings.TrimSpace(spec), "0-")
	}
	return false
}

// statusWriter, yazılan durum kodunu tutar.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
