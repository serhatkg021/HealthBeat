package report

import (
	"sync"
	"time"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/pusher"
)

// fullServicesEvery, tam servis listesinin gönderilme aralığıdır; aradaki raporlar yalnızca sorunlu ve değişen
// servisleri taşır (bir makinede yüzlerce servis olur, çoğu hiç değişmez).
const fullServicesEvery = 5 * time.Minute

// serviceReporter, hangi servislerin rapora gireceğine karar verir: ilk raporda ve fullServicesEvery'de bir tam liste;
// arada sorunlu durumdakiler (her raporda, alert'i kaçırmamak için) ve son rapordan beri durumu değişenler. Eşzamanlı
// çağrılabilir (pull modunda istekler paralel gelir).
//
// Gönderilemeyen bir rapordaki "değişti" bilgisi server'a ulaşmaz: sorunlu servisler zaten her raporda gider, öbür
// değişiklikler en geç bir sonraki tam listede düzelir.
type serviceReporter struct {
	now func() time.Time

	mu       sync.Mutex
	last     map[string]collector.ServiceState
	lastFull time.Time
}

func newServiceReporter() *serviceReporter { return &serviceReporter{now: time.Now} }

// next, bu raporun servis bölümüdür; known=false (systemd yok, liste alınamadı ya da bayat) ise nil: gönderilmez.
func (r *serviceReporter) next(current []collector.ServiceState, known bool) *pusher.Services {
	if !known || current == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	full := r.last == nil || now.Sub(r.lastFull) >= fullServicesEvery
	items := make([]collector.ServiceState, 0)
	for _, s := range current {
		prev, seen := r.last[s.Name]
		if full || troubled(s) || !seen || changed(prev, s) {
			items = append(items, s)
		}
	}

	r.last = make(map[string]collector.ServiceState, len(current))
	for _, s := range current {
		r.last[s.Name] = s
	}
	if full {
		r.lastFull = now
	}
	return pusher.FromServices(items, full)
}

// troubled, servisin alert açısından önemli bir ara ya da hata durumunda olup olmadığıdır.
func troubled(s collector.ServiceState) bool {
	switch s.Active {
	case "failed", "activating", "deactivating", "reloading":
		return true
	}
	return s.Sub == "auto-restart"
}

func changed(a, b collector.ServiceState) bool {
	if a.Active != b.Active || a.Sub != b.Sub {
		return true
	}
	if (a.Restarts == nil) != (b.Restarts == nil) {
		return true
	}
	return a.Restarts != nil && *a.Restarts != *b.Restarts
}
