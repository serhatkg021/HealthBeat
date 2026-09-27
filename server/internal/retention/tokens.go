package retention

import (
	"context"
	"log/slog"
	"time"

	"healthbeat-server/internal/store"
)

// TokenPurger, süresi çoktan dolmuş oturum (refresh token) ve şifre sıfırlama kayıtlarını saatte bir siler.
type TokenPurger struct {
	refreshTokens *store.RefreshTokens
	resets        *store.PasswordResets
}

func NewTokenPurger(refreshTokens *store.RefreshTokens, resets *store.PasswordResets) *TokenPurger {
	return &TokenPurger{refreshTokens: refreshTokens, resets: resets}
}

// Run, ctx iptal edilene kadar periyodik olarak temizler. Kendi goroutine'inde çalıştırın.
func (p *TokenPurger) Run(ctx context.Context) {
	ticker := time.NewTicker(purgeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := p.refreshTokens.PurgeExpired(ctx); err != nil {
				slog.ErrorContext(ctx, "purge refresh tokens", "err", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "purged expired refresh tokens", "count", n)
			}
			if n, err := p.resets.PurgeExpired(ctx); err != nil {
				slog.ErrorContext(ctx, "purge password reset tokens", "err", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "purged expired password reset records", "count", n)
			}
		}
	}
}
