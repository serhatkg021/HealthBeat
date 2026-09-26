package httpapi

import (
	"encoding"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
)

// validator, istek gövdesi tiplerinin çözümden hemen sonra kendi kurallarını denetlediği arayüzdür. Validate alanları
// normalleştirebilir (ör. boşlukları kırpmak), bu yüzden işaretçi alıcıyla tanımlanır. Döndürdüğü hata *apiError
// değilse metni 400 mesajı olur.
type validator interface {
	Validate() error
}

// bind, gövdeyi T'ye çözer (decodeJSON: 1 MiB sınırı, bilinmeyen alan reddi) ve T validator ise doğrular.
//
// Çözme hatası "geçersiz istek gövdesi" ile 400'dür; sorun bir alana bağlanabiliyorsa (yanlış tür, bilinmeyen alan)
// Fields o alanı adlandırır.
func bind[T any](r *http.Request) (T, error) {
	var v T
	if err := decodeJSON(r, &v); err != nil {
		return v, decodeError(err)
	}
	if val, ok := any(&v).(validator); ok {
		if err := val.Validate(); err != nil {
			var apiErr *apiError
			if errors.As(err, &apiErr) {
				return v, apiErr
			}
			return v, badRequest(err.Error())
		}
	}
	return v, nil
}

// decodeError, decodeJSON'un hatasını 400 yanıtına çevirir.
func decodeError(err error) *apiError {
	e := newError(http.StatusBadRequest, "geçersiz istek gövdesi")
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		e.Fields = map[string]string{typeErr.Field: jsonTypeName(typeErr.Type) + " olmalı"}
		return e
	}
	// encoding/json bilinmeyen alan için ayrı bir hata tipi sunmaz; metni sabittir: json: unknown field "ad".
	if name, ok := strings.CutPrefix(err.Error(), `json: unknown field "`); ok {
		e.Fields = map[string]string{strings.TrimSuffix(name, `"`): "bilinmeyen alan"}
	}
	return e
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// jsonTypeName, beklenen Go tipini istemcinin anlayacağı JSON türüyle adlandırır.
func jsonTypeName(t reflect.Type) string {
	if t == nil {
		return "geçerli bir değer"
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return "metin" // uuid.UUID, time.Time vb. JSON'da string'dir
	}
	switch t.Kind() {
	case reflect.String:
		return "metin"
	case reflect.Bool:
		return "true ya da false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "tam sayı"
	case reflect.Float32, reflect.Float64:
		return "sayı"
	case reflect.Slice, reflect.Array:
		return "dizi"
	case reflect.Map, reflect.Struct:
		return "nesne"
	case reflect.Pointer:
		return jsonTypeName(t.Elem())
	default:
		return "geçerli bir değer"
	}
}

// Create/update isteklerinde tekrar eden kurallar; mesajlar API sözleşmesinin parçasıdır.

func validateIP(ip string) error {
	if net.ParseIP(ip) == nil {
		return errors.New("ip geçerli bir IP adresi olmalı")
	}
	return nil
}

func validateInterval(seconds int) error {
	if seconds <= 0 {
		return errors.New("interval_seconds pozitif olmalı")
	}
	return nil
}
