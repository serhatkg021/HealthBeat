package pullserver

import (
	"testing"
	"time"
)

func TestIntervalTrackerLearnsFromRequests(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tr := newIntervalTracker(func() time.Time { return now })
	if got := tr.Interval(); got != defaultPullInterval {
		t.Fatalf("before any request: %s, want the default %s", got, defaultPullInterval)
	}
	tr.Observe()
	if got := tr.Interval(); got != defaultPullInterval {
		t.Fatalf("after one request: %s, want still the default", got)
	}
	for _, c := range []struct {
		gap, want time.Duration
	}{
		{time.Minute, time.Minute},
		{2 * time.Second, minPullInterval}, // art arda iki istek (ör. elle deneme) aralığı aşırı kısaltmasın
		{time.Hour, maxPullInterval},       // uzun bir kesinti toplamayı saatlerce durdurmasın
		{45 * time.Second, 45 * time.Second},
	} {
		now = now.Add(c.gap)
		tr.Observe()
		if got := tr.Interval(); got != c.want {
			t.Errorf("after a %s gap: %s, want %s", c.gap, got, c.want)
		}
	}
}
