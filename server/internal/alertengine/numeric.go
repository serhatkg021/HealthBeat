package alertengine

import (
	"context"
	"log/slog"
	"math"
	"time"

	"healthbeat-server/internal/model"
)

// Protokol 4'ün sayısal alert'leri: eşik sistemini kullanırlar (uyarı/kritik ve isteğe bağlı süre, bkz. apply).
// Eşik tanımlı değilse alert yoktur; eşik kaldırılınca açık alert'ler kapanır. Veri raporda yoksa (eski agent, sanal
// makinede sıcaklık) hiçbir şey değişmez; konu (disk, sensör, servis) listeden kalkınca alert'i kapanır.

// restartWindow, yeniden başlatma döngüsü alert'inin baktığı süredir.
const restartWindow = 10 * time.Minute

func (e *Engine) evaluateNumeric(ctx context.Context, st *hostState, r Report) {
	var latency, temperature map[string]float64
	if len(r.DiskIO) > 0 {
		latency = make(map[string]float64, len(r.DiskIO))
		for _, d := range r.DiskIO {
			latency[d.Name] = d.AwaitMs
		}
	}
	if r.State != nil && len(r.State.Temperatures) > 0 {
		temperature = make(map[string]float64, len(r.State.Temperatures))
		for _, t := range r.State.Temperatures {
			temperature[t.Sensor] = t.Celsius
		}
	}
	e.evaluateSubjects(ctx, st, model.MetricTypeDiskLatency, model.MetricTypeDiskLatency, latency)
	e.evaluateSubjects(ctx, st, model.MetricTypeTemperature, model.MetricTypeTemperature, temperature)
	e.evaluateRestartLoops(ctx, st)
	e.evaluateTimeOffset(ctx, st, r.State)
}

// evaluateSubjects, konu başına bir değeri thresholdType eşiğine göre değerlendirir ve alertType alert'i üretir.
// values nil ise veri yoktur: yalnızca eşik hiç kalmadıysa açık alert'ler kapanır.
func (e *Engine) evaluateSubjects(ctx context.Context, st *hostState, thresholdType, alertType string, values map[string]float64) {
	thresholds := st.thresholds.Metric(thresholdType)
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		e.resolveAll(ctx, st, alertType)
		return
	}
	if values == nil {
		return
	}
	evaluated := make(map[string]condition, len(values))
	for subject, v := range values {
		threshold, ok := thresholds.For(subject)
		if !ok {
			continue // eşiği olmayan konunun alert'i aşağıda kapanır
		}
		evaluated[subject] = condition{keep: true}
		e.apply(ctx, st, alertType, subject, v, threshold)
	}
	// Değerlendirilmeyen (listeden kalkan ya da eşiği kaldırılan) konuların alert'leri ve bekleme kayıtları kapanır.
	e.reconcile(ctx, st, alertType, evaluated, true)
}

// evaluateRestartLoops: izlenen servisin son 10 dakikadaki yeniden başlatma sayısı service_restart eşiğine göre
// değerlendirilir (service_restart_loop). Sayı servis tablosundaki sayaç geçmişinden hesaplanır.
func (e *Engine) evaluateRestartLoops(ctx context.Context, st *hostState) {
	thresholds := st.thresholds.Metric(model.MetricTypeServiceRestart)
	if thresholds.Base == nil && len(thresholds.PerSubject) == 0 {
		e.resolveAll(ctx, st, model.AlertTypeServiceRestartLoop)
		return
	}
	services, ok := e.services(ctx, st)
	if !ok {
		return
	}
	values := map[string]float64{}
	since := e.now().Add(-restartWindow)
	for _, s := range services {
		if s.Watched {
			values[s.Name] = float64(s.RestartsSince(since))
		}
	}
	e.evaluateSubjects(ctx, st, model.MetricTypeServiceRestart, model.AlertTypeServiceRestartLoop, values)
}

// evaluateTimeOffset: saat farkının mutlak değeri time_offset eşiğine göre değerlendirilir (time_sync/offset).
func (e *Engine) evaluateTimeOffset(ctx context.Context, st *hostState, state *model.SystemState) {
	threshold := st.thresholds.Metric(model.MetricTypeTimeOffset).Base
	if threshold == nil {
		e.reconcile(ctx, st, model.AlertTypeTimeSync, map[string]condition{model.TimeSyncSubjectOffset: {}}, false)
		return
	}
	if state == nil || state.TimeSync == nil || state.TimeSync.OffsetMs == nil {
		return
	}
	e.apply(ctx, st, model.AlertTypeTimeSync, model.TimeSyncSubjectOffset, math.Abs(*state.TimeSync.OffsetMs), *threshold)
}

// services, sunucunun servislerini rapor başına bir kez okur (service_failed ve service_restart_loop paylaşır).
func (e *Engine) services(ctx context.Context, st *hostState) ([]model.HostService, bool) {
	if st.servicesLoaded {
		return st.serviceList, st.serviceList != nil
	}
	st.servicesLoaded = true
	list, err := e.hosts.Services(ctx, st.hostID)
	if err != nil {
		slog.ErrorContext(ctx, "alert engine: read services", "host_id", st.hostID.String(), "err", err)
		return nil, false
	}
	if list == nil {
		list = []model.HostService{}
	}
	st.serviceList = list
	return list, true
}
