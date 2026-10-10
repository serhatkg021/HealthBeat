package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"healthbeat-server/internal/tz"
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
	// Kurulumun saat dilimi (bakım pencereleri ve bildirimler buna göre) ve server'ın saati: panel alt çubukta server
	// saatini bilgisayarın saatinden bağımsız gösterir. UTCOffset "UTC+3" biçimindedir, UTCOffsetSeconds aynı ofsettir.
	Timezone         string    `json:"timezone"`
	UTCOffset        string    `json:"utc_offset"`
	UTCOffsetSeconds int       `json:"utc_offset_seconds"`
	ServerTime       time.Time `json:"server_time"`
}

// location, kurulumun saat dilimidir (ayarlar bağlanmamışsa server'ın TZ'si ya da UTC).
func (d *Deps) location() *time.Location {
	if d.appSettings == nil {
		return tz.Resolve("")
	}
	return d.appSettings.Location()
}

// handleMeta, panelin sürüm bilgisini (server sürümü ve agent sürüm politikası) verir. Herhangi
// bir oturum açmış kullanıcı okuyabilir: hassas bir şey içermez.
func (d *Deps) handleMeta(w http.ResponseWriter, r *http.Request) {
	policy := d.agentPolicy.Load()
	loc, now := d.location(), time.Now()
	_, offset := now.In(loc).Zone()
	writeJSON(w, http.StatusOK, metaResponse{
		ServerVersion:      version.Version,
		Protocol:           version.Protocol,
		LatestAgentVersion: policy.Latest,
		MinAgentVersion:    policy.Min,
		Timezone:           loc.String(),
		UTCOffset:          tz.FormatOffset(offset),
		UTCOffsetSeconds:   offset,
		ServerTime:         now.UTC(),
	})
}

// readyTimeout, /readyz'in veritabanı ping'ine tanıdığı süredir: yük dengeleyicinin denetimi takılmamalı.
const readyTimeout = 2 * time.Second

// errorCodeDatabaseUnavailable, /readyz'in veritabanına ulaşamadığında döndürdüğü koddur.
const errorCodeDatabaseUnavailable = "database_unavailable"

// handleReadyz, server'ın istek karşılamaya hazır olup olmadığını söyler: veritabanına ulaşılabiliyorsa 200, yoksa 503.
// /healthz yalnızca sürecin ayakta olduğunu söyler (container'ı yeniden başlatma kararı için); veritabanı düştüğünde
// server'ı yeniden başlatmak işe yaramaz, trafiği başka yere yönlendirmek yarar.
func (d *Deps) handleReadyz(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	if err := d.pool.Ping(ctx); err != nil {
		slog.WarnContext(r.Context(), "readyz: database unreachable", "err", err)
		return &apiError{Status: http.StatusServiceUnavailable, Message: "veritabanına ulaşılamıyor", Code: errorCodeDatabaseUnavailable}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	return nil
}
