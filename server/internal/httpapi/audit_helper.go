package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// logAudit, kritik bir eylemi istek bağlamındaki kimliği doğrulanmış çağıranla kaydeder
// (bkz. internal/store.Audit.Write). Hatalar loglanır, istemciye yansıtılmaz — denetim
// kaydı asıl işlemi asla engellememeli.
func (d *Deps) logAudit(r *http.Request, action, targetType string, targetID *string, details any) {
	userID, hasUser := userIDFromContext(r.Context())
	email, _ := emailFromContext(r.Context())

	var actorUserID *uuid.UUID
	if hasUser {
		actorUserID = &userID
	}

	if err := d.audit.Write(r.Context(), actorUserID, email, action, targetType, targetID, details, remoteIP(r)); err != nil {
		slog.ErrorContext(r.Context(), "audit log write failed", "err", err)
	}
}
