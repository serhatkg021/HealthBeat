package pusher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"healthbeat-agent/internal/version"
)

// probeEvery, degraded moddayken tam payload'ın kaç döngüde bir yeniden denendiğidir.
const probeEvery = 10

// Sender, tek bir payload'ı server'a gönderen şeydir (*Pusher).
type Sender interface {
	Push(ctx context.Context, payload MetricsPayload) error
}

// Compat, tam payload'ı 400 ile reddeden (bilinmeyen alanı kabul etmeyen eski) bir server'a karşı
// kendini toparlar: aynı döngüde yalnızca çekirdek alanlarla yeniden dener ve "degraded" moda
// geçer; böylece server güncellenene kadar CPU/RAM/disk/Docker metrikleri kaybolmaz. Her
// probeEvery döngüde tam payload yeniden denenir; server güncellendiyse normale döner.
//
// Yalnızca 400'de devreye girer: ağ hatası, 401 (kimlik), 429 ya da 5xx bir uyumsuzluk değildir
// ve olduğu gibi bildirilir. Çekirdek payload da 400 alırsa sorun uyumluluk değildir; hata
// olduğu gibi döner ve degraded moda geçilmez. Bkz. docs/COMPATIBILITY.md.
type Compat struct {
	sender     Sender
	degraded   bool
	sinceProbe int
	logf       func(format string, args ...any)
}

func NewCompat(s Sender) *Compat { return &Compat{sender: s, logf: log.Printf} }

// Degraded, şu an yalnızca çekirdek alanların gönderildiğini söyler.
func (c *Compat) Degraded() bool { return c.degraded }

func isBadRequest(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusBadRequest
}

func (c *Compat) Push(ctx context.Context, payload MetricsPayload) error {
	if c.degraded {
		c.sinceProbe++
		if c.sinceProbe < probeEvery {
			return c.sender.Push(ctx, payload.Core())
		}
		c.sinceProbe = 0
		err := c.sender.Push(ctx, payload)
		if err == nil {
			c.degraded = false
			c.logf("server accepts the full payload again; sending everything")
			return nil
		}
		if !isBadRequest(err) {
			return err
		}
		return c.sender.Push(ctx, payload.Core()) // hâlâ eski: çekirdekle devam
	}

	err := c.sender.Push(ctx, payload)
	if !isBadRequest(err) {
		return err
	}
	coreErr := c.sender.Push(ctx, payload.Core())
	if coreErr != nil {
		return err // sorun uyumluluk değil; ilk hatayı bildir
	}
	c.degraded, c.sinceProbe = true, 0
	c.logf("server rejected the full payload (%v); switching to core metrics only (cpu, ram, disk, docker) and retrying the full payload every %d cycles - the server probably needs an update", err, probeEvery)
	return nil
}

// ServerNotes, server'ın bildirdiği sürümden çıkan, operatöre söylenecek notları döndürür.
// Sürüm bilgisi vermeyen (eski) bir server için hiçbir şey söylenmez.
func ServerNotes(info ServerInfo, ownVersion string, ownProtocol int) []string {
	var notes []string
	if info.Version != "" {
		notes = append(notes, fmt.Sprintf("server %s (protocol %d)", info.Version, info.Protocol))
	}
	if info.Protocol > 0 && info.Protocol < ownProtocol {
		notes = append(notes, fmt.Sprintf("server speaks protocol %d, older than this agent's %d: newer fields will be ignored until the server is updated", info.Protocol, ownProtocol))
	}
	if info.LatestAgent != "" && version.Compare(info.LatestAgent, ownVersion) > 0 {
		notes = append(notes, fmt.Sprintf("server recommends agent %s (this agent is %s); update when convenient", info.LatestAgent, ownVersion))
	}
	return notes
}
