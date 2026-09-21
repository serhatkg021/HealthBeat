package httpapi

import (
	"net/http"

	"healthbeat-server/internal/version"
)

type metaResponse struct {
	ServerVersion string `json:"server_version"`
	// Protocol, server'ın anladığı en yüksek ingest protokolüdür (bkz. docs/COMPATIBILITY.md).
	Protocol int `json:"protocol"`
	// LatestAgentVersion/MinAgentVersion sürüm politikasıdır; boş = tanımsız. Panel agent'ları
	// bunlara göre "güncel / güncellenmeli / desteklenmiyor" diye sınıflandırır.
	LatestAgentVersion string `json:"latest_agent_version"`
	MinAgentVersion    string `json:"min_agent_version"`
}

// handleMeta, panelin sürüm bilgisini (server sürümü ve agent sürüm politikası) verir. Herhangi
// bir oturum açmış kullanıcı okuyabilir: hassas bir şey içermez.
func (d *Deps) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, metaResponse{
		ServerVersion:      version.Version,
		Protocol:           version.Protocol,
		LatestAgentVersion: d.agentPolicy.Latest,
		MinAgentVersion:    d.agentPolicy.Min,
	})
}
