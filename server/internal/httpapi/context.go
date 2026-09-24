package httpapi

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota
	ctxKeyRole
	ctxKeyEmail
	ctxKeyHostID
	ctxKeyHostOrgID
	ctxKeyClientIP
)

func withUser(ctx context.Context, userID uuid.UUID, email, role string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	ctx = context.WithValue(ctx, ctxKeyEmail, email)
	ctx = context.WithValue(ctx, ctxKeyRole, role)
	return ctx
}

func userIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyUserID).(uuid.UUID)
	return id, ok
}

func emailFromContext(ctx context.Context) (string, bool) {
	email, ok := ctx.Value(ctxKeyEmail).(string)
	return email, ok
}

func roleFromContext(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(ctxKeyRole).(string)
	return role, ok
}

// withHostID, isteği panel kullanıcısı JWT'si yerine host kimlik bilgileriyle (push modu
// api_token) doğrulanmış olarak işaretler — tamamen ayrı bir kimlik doğrulama yolu (bkz.
// docs/MIMARI.md bölüm 5). orgID onunla birlikte taşınır; böylece alert engine ikinci
// bir sorgu olmadan org/global eşiklerini çözebilir.
func withHostID(ctx context.Context, hostID, orgID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyHostID, hostID)
	return context.WithValue(ctx, ctxKeyHostOrgID, orgID)
}

func hostIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyHostID).(uuid.UUID)
	return id, ok
}

func hostOrgIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyHostOrgID).(uuid.UUID)
	return id, ok
}

// withClientIP, istek başına bir kez belirlenen istemci IP'sini taşır (bkz. Deps.resolveClientIP).
func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyClientIP, ip)
}

func clientIPFromContext(ctx context.Context) (string, bool) {
	ip, ok := ctx.Value(ctxKeyClientIP).(string)
	return ip, ok
}
