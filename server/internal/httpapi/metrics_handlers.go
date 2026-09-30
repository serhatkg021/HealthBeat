package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"healthbeat-server/internal/ingest"
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
func (d *Deps) handleIngestMetrics(w http.ResponseWriter, r *http.Request) error {
	hostID, _ := hostIDFromContext(r.Context())
	orgID, _ := hostOrgIDFromContext(r.Context())

	// Server sürümünü her yanıtta (400 dahil) bildir: agent, karşısındaki server'ın ne kadar yeni
	// olduğunu öğrenir. Eski agent'lar başlıkları yok sayar (bkz. docs/COMPATIBILITY.md).
	h := w.Header()
	h.Set(version.HeaderServerVersion, version.Version)
	h.Set(version.HeaderProtocol, strconv.Itoa(version.Protocol))
	if latest := d.agentPolicy.Load().Latest; latest != "" {
		h.Set(version.HeaderLatestAgent, latest)
	}

	// bind kullanılmaz: ingest bilinmeyen alanları reddetmez, bildirir (bkz. ingest.Decode).
	body, err := readJSONBody(r)
	if err != nil {
		return badRequest("geçersiz istek gövdesi")
	}
	payload, unknown, err := ingest.Decode(body)
	if errors.Is(err, ingest.ErrMalformed) {
		return badRequest("geçersiz istek gövdesi")
	}
	if err != nil {
		return badRequest(err.Error())
	}

	err = d.ingest.Record(r.Context(), ingest.Report{
		HostID: hostID, OrgID: orgID, Payload: payload, Agent: agentInfoFromRequest(r, unknown), Source: ingest.SourcePush,
	})
	if err != nil {
		return serverErr("metrikler kaydedilemedi", "ingest metrics: insert", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
