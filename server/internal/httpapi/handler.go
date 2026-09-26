package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// handlerFunc, hatasını döndüren bir handler'dır; handle onu http.HandlerFunc'a çevirir. Başarılı yanıtı handler
// kendisi yazar ve nil döner; hata döndürdüyse henüz hiçbir şey yazmamış olmalıdır.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// handle, h'nin döndürdüğü hatayı yanıta çevirir:
//   - *apiError (badRequest, notFound, forbidden, conflict, bind…) olduğu gibi yazılır;
//   - *serverError (failWith, serverErr) loglanır ve kendi mesajıyla 500 döner;
//   - başka bir hata beklenmeyen bir durumdur: loglanır ve genel mesajla 500 döner.
func handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			writeAPIError(w, apiErr)
			return
		}
		var srvErr *serverError
		if errors.As(err, &srvErr) {
			slog.ErrorContext(r.Context(), srvErr.op, "err", srvErr.err)
			writeError(w, http.StatusInternalServerError, srvErr.message)
			return
		}
		slog.ErrorContext(r.Context(), "unhandled handler error", "err", err)
		writeError(w, http.StatusInternalServerError, "beklenmeyen bir hata oluştu")
	}
}

// serverError, istemciye ayrıntısı gösterilmeyen bir hatadır: handle err'i op mesajıyla loglar, istemciye message
// ile 500 döner.
type serverError struct {
	message string // istemciye giden Türkçe metin, ör. "sunucu oluşturulamadı"
	op      string // log mesajı, ör. "create host: lookup organization"
	err     error
}

func (e *serverError) Error() string { return e.op + ": " + e.err.Error() }
func (e *serverError) Unwrap() error { return e.err }

// serverErr, message ile 500 dönen ve err'i op ile loglayan hatadır.
func serverErr(message, op string, err error) error {
	return &serverError{message: message, op: op, err: err}
}

// failFunc, beklenmeyen bir hatayı op log mesajıyla serverError'a çevirir (bkz. failWith).
type failFunc func(op string, err error) error

// failWith, bir handler'ın bütün beklenmeyen hatalarında aynı 500 mesajını kullanmasını sağlar:
//
//	fail := failWith("sunucu oluşturulamadı")
//	…
//	return fail("create host: lookup organization", err)
func failWith(message string) failFunc {
	return func(op string, err error) error { return serverErr(message, op, err) }
}

// newError, özel bir durum kodu ya da mesajla hata yanıtıdır; sık kullanılanlar için aşağıdaki kısayollar vardır.
func newError(status int, message string) *apiError {
	return &apiError{Status: status, Message: message}
}

func badRequest(message string) error { return newError(http.StatusBadRequest, message) }
func notFound(message string) error   { return newError(http.StatusNotFound, message) }
func conflict(message string) error   { return newError(http.StatusConflict, message) }

// forbidden, rolün ya da kapsamın yetmediği isteklerin ortak yanıtıdır.
func forbidden() error { return newError(http.StatusForbidden, "yetkiniz yok") }

// pathID, rotadaki {id}'yi UUID olarak çözer; geçersizse message ile 400 döner (ör. "geçersiz sunucu kimliği").
func pathID(r *http.Request, message string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, badRequest(message)
	}
	return id, nil
}
