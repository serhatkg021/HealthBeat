// Package jobs, server'ın arka plan döngülerini (pull zamanlayıcısı, offline izleyici, temizlik işleri, bildirim
// işçileri…) çalıştırır ve kapanışta bitmelerini bekler: veritabanı havuzu ancak hepsi durduktan sonra kapanmalı.
package jobs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"healthbeat-server/internal/logging"
)

// Runner, ctx bitene kadar dönen işleri çalıştırır.
type Runner struct {
	ctx     context.Context
	wg      sync.WaitGroup
	mu      sync.Mutex
	running map[string]int // iş adı → çalışan kopya sayısı
}

// New, işleri ctx ile çalıştıran bir Runner döndürür; ctx iptal edilince işler durur.
func New(ctx context.Context) *Runner {
	return &Runner{ctx: ctx, running: map[string]int{}}
}

// Go, fn'i kendi goroutine'inde ctx bitene kadar çalıştırır. fn panic'lerse loglanır ve kısa bir beklemeden sonra
// yeniden başlatılır (bkz. logging.RunLoop).
func (r *Runner) Go(name string, fn func(context.Context)) {
	r.mu.Lock()
	r.running[name]++
	r.mu.Unlock()
	r.wg.Go(func() {
		defer func() {
			r.mu.Lock()
			if r.running[name]--; r.running[name] == 0 {
				delete(r.running, name)
			}
			r.mu.Unlock()
		}()
		logging.RunLoop(r.ctx, name, fn)
	})
}

// Wait, bütün işlerin bitmesini bekler; ctx önce biterse hâlâ çalışan işlerin adlarıyla hata döner.
func (r *Runner) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("still running: %s", strings.Join(r.Running(), ", "))
	}
}

// Running, şu an çalışan işlerin adlarıdır (sıralı).
func (r *Runner) Running() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.running))
	for name := range r.running {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
