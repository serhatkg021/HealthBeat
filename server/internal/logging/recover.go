package logging

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"
)

// Recover, bir panic'i yakalar ve yığın iziyle loglar; süreç ayakta kalır. Doğrudan defer
// edilmelidir (defer logging.Recover(ctx, "...")): recover yalnızca öyle çalışır.
func Recover(ctx context.Context, where string) {
	if v := recover(); v != nil {
		LogPanic(ctx, where, v)
	}
}

// LogPanic, recover'dan dönen v'yi yığın iziyle ERROR seviyesinde loglar.
func LogPanic(ctx context.Context, where string, v any) {
	slog.ErrorContext(ctx, "panic recovered", "where", where, "panic", v, "stack", string(debug.Stack()))
}

// Go, fn'i yeni bir goroutine'de çalıştırır; bir panic loglanır ve süreci düşürmez.
func Go(ctx context.Context, where string, fn func()) {
	go func() {
		defer Recover(ctx, where)
		fn()
	}()
}

// loopRestartDelay, panic'le biten bir döngünün yeniden başlatılmadan önce beklediği süredir;
// hemen tekrar panic'leyen bir döngünün logu ve CPU'yu doldurmasını önler. Testler kısaltır.
var loopRestartDelay = 5 * time.Second

// RunLoop, ctx bitene kadar dönen bir arka plan döngüsünü (fn) çalıştırır. fn panic'lerse loglanır
// ve kısa bir beklemeden sonra yeniden başlatılır: döngü sessizce ölmez (ör. offline izleyici
// durursa hiçbir host offline alert'i üretilmez). fn normal dönerse RunLoop da döner.
func RunLoop(ctx context.Context, where string, fn func(context.Context)) {
	for {
		if !runRecovering(ctx, where, fn) || ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "restarting background loop after panic", "where", where, "delay", loopRestartDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(loopRestartDelay):
		}
	}
}

func runRecovering(ctx context.Context, where string, fn func(context.Context)) (panicked bool) {
	defer func() {
		if v := recover(); v != nil {
			LogPanic(ctx, where, v)
			panicked = true
		}
	}()
	fn(ctx)
	return false
}
