package pullserver

import (
	"sync"
	"time"
)

// Pull modunda sorgu aralığına server karar verir; agent'ın yapılandırmasında aralık yoktur. Arka plan toplayıcılarının
// (ör. Docker) ne sıklıkla çalışacağı bu yüzden gelen isteklerden öğrenilir: server 5 dakikada bir soruyorsa Docker
// boşuna 30 sn'de bir toplanmaz.
const (
	defaultPullInterval = 30 * time.Second
	minPullInterval     = 10 * time.Second
	maxPullInterval     = 5 * time.Minute
)

// intervalTracker, son iki yetkili istek arasındaki süreyi (sınırlanmış) aralık olarak tutar.
type intervalTracker struct {
	now func() time.Time

	mu       sync.Mutex
	last     time.Time
	interval time.Duration
}

func newIntervalTracker(now func() time.Time) *intervalTracker {
	return &intervalTracker{now: now, interval: defaultPullInterval}
}

// Observe, yetkili bir isteğin geldiğini kaydeder.
func (t *intervalTracker) Observe() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if !t.last.IsZero() {
		t.interval = min(max(now.Sub(t.last), minPullInterval), maxPullInterval)
	}
	t.last = now
}

// Interval, öğrenilen aralıktır; ilk iki istekten önce varsayılan.
func (t *intervalTracker) Interval() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.interval
}
