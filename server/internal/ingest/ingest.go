// Package ingest, bir agent raporunun (push'ta istek gövdesi, pull'da poll yanıtı) server'a alınmasıdır: çözme ve
// doğrulama (Decode) ile kayıt ve değerlendirme (Service.Record). İki alım yolu da buradan geçer; böylece biri
// değişince öteki geride kalmaz (bkz. docs/MIMARI.md bölüm 6).
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Alım yolları; log satırlarında source alanıdır.
const (
	SourcePush = "push"
	SourcePull = "pull"
)

// ErrMalformed, raporun JSON olarak çözülemediğini söyler; doğrulama hatalarından (değer aralığı vb.) ayrıdır.
var ErrMalformed = errors.New("malformed metrics report")

// Decode, raporu çözer ve doğrular. unknown, server'ın tanımadığı alan adlarıdır: hata değildir, agent'ın
// server'dan yeni olduğunu gösterir (bkz. docs/COMPATIBILITY.md). Çözme hatası ErrMalformed'ı sarar; doğrulama
// hatasının metni istemciye gösterilebilir.
func Decode(body []byte) (model.MetricsIngestRequest, []string, error) {
	payload, unknown, err := model.ParseMetricsIngest(body)
	if err != nil {
		return model.MetricsIngestRequest{}, nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := payload.Validate(); err != nil {
		return model.MetricsIngestRequest{}, nil, err
	}
	return payload, unknown, nil
}

// Report, doğrulanmış bir agent raporudur.
type Report struct {
	HostID  uuid.UUID
	OrgID   uuid.UUID // alert motoru organizasyon/genel eşikleri bununla çözer
	Payload model.MetricsIngestRequest
	Agent   model.AgentInfo // sürüm bilgisi: push'ta istek, pull'da yanıt başlıklarından
	Source  string          // SourcePush ya da SourcePull
}

// Service, raporu kaydeder ve alert motorunu çalıştırır.
type Service struct {
	metrics *store.Metrics
	hosts   *store.Hosts
	engine  *alertengine.Engine
}

func New(metrics *store.Metrics, hosts *store.Hosts, engine *alertengine.Engine) *Service {
	return &Service{metrics: metrics, hosts: hosts, engine: engine}
}

// Record, raporu şu sırayla işler: metrik satırı, container durumları, sunucunun çevrimiçi işaretlenmesi (donanım ve
// agent sürümüyle), sonra alert motoru (offline alert'i kapatma, docker ve metrik değerlendirmesi).
//
// Yalnızca metrik satırı yazılamazsa hata döner ve başka bir şey yapılmaz; çağıran onu kendi biçiminde loglar/yanıtlar.
// Sonraki adımların hataları burada loglanır ve alımı durdurmaz: hatalı bir container raporu ya da çevrimiçi işareti
// CPU/RAM/disk'i kaybettirmemeli.
func (s *Service) Record(ctx context.Context, r Report) error {
	p := r.Payload
	if err := s.metrics.Insert(ctx, r.HostID, p.CPUUsagePct, p.RAMUsagePct, p.Disk); err != nil {
		return err
	}
	logErr := func(msg string, err error) {
		slog.ErrorContext(ctx, msg, "host_id", r.HostID.String(), "source", r.Source, "err", err)
	}
	if err := s.metrics.ReplaceDockerContainers(ctx, r.HostID, p.DockerContainers); err != nil {
		logErr("ingest: store docker containers", err)
	}
	if err := s.hosts.MarkOnline(ctx, r.HostID, p.Hardware(), r.Agent); err != nil {
		logErr("ingest: mark host online", err)
	}

	s.engine.EvaluateReport(ctx, r.HostID, r.OrgID, alertengine.Report{
		CPUPct: p.CPUUsagePct, RAMPct: p.RAMUsagePct, Disks: p.Disk, Containers: p.DockerContainers,
	})
	return nil
}
