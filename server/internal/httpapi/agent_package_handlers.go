package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/agentpkg"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

const (
	// maxAgentPackageBytes, tek bir paket dosyasının üst sınırıdır (release paketleri 3–9 MB).
	maxAgentPackageBytes = 50 << 20
	// maxAgentUploadBytes, bir yükleme isteğinin tamamının üst sınırıdır (bir sürümün 6 paketi ve multipart ekleri).
	maxAgentUploadBytes = 8 * maxAgentPackageBytes
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

// agentPackageUploadResult, yüklemedeki bir dosyanın sonucudur: Package ya da Error doludur.
type agentPackageUploadResult struct {
	Filename string              `json:"filename"`
	Package  *model.AgentPackage `json:"package,omitempty"`
	Error    string              `json:"error,omitempty"`
}

// POST /api/v1/agent-packages (multipart/form-data; her dosya bir parça). Her dosya ayrı değerlendirilir: biri
// reddedilse de diğerleri yüklenir. Yanıt 200 ve dosya başına sonuçtur.
func (d *Deps) handleUploadAgentPackages(w http.ResponseWriter, r *http.Request) error {
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
	var uploader *uuid.UUID
	if id, ok := userIDFromContext(r.Context()); ok {
		uploader = &id
	}
	results := []agentPackageUploadResult{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return newError(http.StatusRequestEntityTooLarge, fmt.Sprintf("istek en fazla %d MB olabilir", maxAgentUploadBytes>>20))
			}
			return badRequest("yükleme okunamadı")
		}
		name := part.FileName()
		if name == "" { // dosya olmayan form alanı
			_ = part.Close()
			continue
		}
		res := d.uploadAgentPackage(r, part, name, uploader)
		_ = part.Close()
		if res == nil { // istek gövdesi okunamadı (bağlantı koptu ya da sınır aşıldı)
			return newError(http.StatusRequestEntityTooLarge, fmt.Sprintf("istek en fazla %d MB olabilir", maxAgentUploadBytes>>20))
		}
		results = append(results, *res)
	}
	if len(results) == 0 {
		return badRequest("yüklenecek dosya yok")
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

// uploadAgentPackage, tek bir dosyayı yükler. İstek gövdesinin kendisi okunamadıysa nil döner.
func (d *Deps) uploadAgentPackage(r *http.Request, body io.Reader, name string, uploader *uuid.UUID) *agentPackageUploadResult {
	res := &agentPackageUploadResult{Filename: name}
	info, err := agentpkg.Parse(name)
	if err != nil {
		res.Error = "agent paketi değil: yalnızca release'teki .deb, .rpm ve .tar.gz paketleri yüklenebilir"
		return res
	}
	staged, err := d.agentPackageDir.Stage(body, maxAgentPackageBytes)
	switch {
	case errors.Is(err, agentpkg.ErrTooLarge):
		res.Error = fmt.Sprintf("dosya en fazla %d MB olabilir", maxAgentPackageBytes>>20)
		return res
	case errors.Is(err, agentpkg.ErrEmpty):
		res.Error = "dosya boş"
		return res
	case err != nil:
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || r.Context().Err() != nil {
			return nil
		}
		slog.ErrorContext(r.Context(), "agent package upload: stage", "filename", name, "err", err)
		res.Error = "dosya kaydedilemedi"
		return res
	}
	defer staged.Discard()

	p := model.AgentPackage{Version: info.Version, Filename: name, Arch: info.Arch, Format: info.Format,
		SizeBytes: staged.Size, SHA256: staged.SHA256, UploadedBy: uploader}
	created, err := d.agentPackages.Create(r.Context(), p, func() error { return staged.Place(info.Version, name) })
	switch {
	case errors.Is(err, store.ErrAgentPackageExists):
		res.Error = "bu dosya zaten yüklü; yeniden yüklemek için önce silin"
		return res
	case err != nil:
		slog.ErrorContext(r.Context(), "agent package upload: save", "filename", name, "err", err)
		res.Error = "dosya kaydedilemedi"
		return res
	}
	id := created.ID.String()
	d.logAudit(r, "agent_package.upload", "agent_package", &id, map[string]any{
		"filename": created.Filename, "version": created.Version, "size_bytes": created.SizeBytes, "sha256": created.SHA256,
	})
	res.Package = &created
	return res
}

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
