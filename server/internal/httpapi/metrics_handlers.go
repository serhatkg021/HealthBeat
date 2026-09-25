package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/version"
)

// agentInfoFromRequest, push isteğinin başlıklarından agent sürümünü okur; unknown, gövdedeki
// server'ın tanımadığı alan adlarıdır.
func agentInfoFromRequest(r *http.Request, unknown []string) model.AgentInfo {
	info := model.ParseAgentInfo(r.Header.Get(version.HeaderAgentVersion), r.Header.Get("User-Agent"), r.Header.Get(version.HeaderProtocol))
	info.UnsupportedFields = unknown
	return info
}

// handleIngestMetrics, push modu host'ın tek endpoint'idir (bkz. docs/MIMARI.md
// bölüm 7). requirePermission ile değil requireHostAuth ile erişilir — burada hiçbir
// panel kullanıcısı ya da rol söz konusu değildir.
func (d *Deps) handleIngestMetrics(w http.ResponseWriter, r *http.Request) {
	hostID, _ := hostIDFromContext(r.Context())
	orgID, _ := hostOrgIDFromContext(r.Context())

	// Server sürümünü her yanıtta (400 dahil) bildir: agent, karşısındaki server'ın ne kadar yeni
	// olduğunu öğrenir. Eski agent'lar başlıkları yok sayar (bkz. docs/COMPATIBILITY.md).
	h := w.Header()
	h.Set(version.HeaderServerVersion, version.Version)
	h.Set(version.HeaderProtocol, strconv.Itoa(version.Protocol))
	if d.agentPolicy.Latest != "" {
		h.Set(version.HeaderLatestAgent, d.agentPolicy.Latest)
	}

	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	req, unknown, err := model.ParseMetricsIngest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := d.metrics.Insert(r.Context(), hostID, req.CPUUsagePct, req.RAMUsagePct, req.Disk); err != nil {
		slog.ErrorContext(r.Context(), "ingest metrics: insert", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler kaydedilemedi")
		return
	}

	// Hatalı biçimli bir container raporu (ör. tanınmayan bir durum değeri) tüm alımı başarısız
	// kılmamalı — CPU/RAM/disk zaten kalıcı olarak saklandı.
	if err := d.metrics.ReplaceDockerContainers(r.Context(), hostID, req.DockerContainers); err != nil {
		slog.ErrorContext(r.Context(), "ingest metrics: insert docker containers", "err", err)
	}

	if err := d.hosts.MarkOnline(r.Context(), hostID, req.Hardware(), agentInfoFromRequest(r, unknown)); err != nil {
		slog.ErrorContext(r.Context(), "ingest metrics: mark online", "err", err)
	}

	d.alertEngine.ResolveOffline(r.Context(), hostID, orgID)
	d.alertEngine.EvaluateDocker(r.Context(), hostID, orgID, req.DockerContainers)
	d.alertEngine.EvaluateMetrics(r.Context(), hostID, orgID, req.CPUUsagePct, req.RAMUsagePct, req.Disk)

	w.WriteHeader(http.StatusNoContent)
}
