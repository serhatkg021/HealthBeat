package httpapi

import (
	"errors"
	"net/http"

	"healthbeat-server/internal/store"
)

// storeReasons, store'un adlandırılmış iş kuralı hatalarının yanıtlarıdır: durum kodu ve istemciye giden Türkçe metin.
// Store'a yeni bir neden eklendiğinde buraya da satırı eklenir; yoksa hata 500 olarak loglanır.
var storeReasons = []struct {
	err     error
	status  int
	message string
}{
	{store.ErrParentOrganizationMissing, http.StatusNotFound, "üst organizasyon bulunamadı"},
	{store.ErrOrganizationMissing, http.StatusNotFound, "organizasyon bulunamadı"},
	{store.ErrOrganizationNameTaken, http.StatusConflict, "aynı üst şirketin altında bu ada sahip bir organizasyon zaten var"},
	{store.ErrOrganizationCycle, http.StatusConflict, "organizasyon ağacında döngü oluşturulamaz (bir organizasyon kendi altındaki bir dalın altına taşınamaz)"},
	{store.ErrOrganizationNotEmpty, http.StatusConflict, "organizasyonda hâlâ alt organizasyon ya da sunucu var"},

	{store.ErrHostMissing, http.StatusNotFound, "sunucu bulunamadı"},
	{store.ErrHostTitleTaken, http.StatusConflict, "bu organizasyonda aynı adlı bir sunucu zaten var"},
	{store.ErrHostModeMismatch, http.StatusConflict, "alanlar seçilen moda uymuyor"},

	{store.ErrEmailTaken, http.StatusConflict, "bu e-posta zaten kullanımda"},
	{store.ErrTwoFactorNoChannel, http.StatusConflict, "iki faktörlü doğrulama açıkken bir kanal seçilmeli (email, sms veya app)"},
	{store.ErrLastSuperAdmin, http.StatusConflict, "son super_admin'in rolü düşürülemez ya da hesabı silinemez"},

	{store.ErrThresholdExists, http.StatusConflict, "bu kapsam ve metrik için zaten bir eşik var"},
	{store.ErrThresholdLevelsInvalid, http.StatusConflict, "warning_level, critical_level'dan büyük olamaz"},
	{store.ErrHostThresholdInvalid, http.StatusBadRequest, "geçersiz sunucu eşiği"},
	{store.ErrStatusRuleInvalid, http.StatusBadRequest, "geçersiz durum kuralı"},

	{store.ErrRouteExists, http.StatusConflict, "bu alıcı ve kanal için bu kapsamda zaten bir kural var"},
	{store.ErrRouteTargetMissing, http.StatusNotFound, "organizasyon, sunucu, kullanıcı ya da iletişim kişisi bulunamadı"},
	{store.ErrRouteShape, http.StatusConflict, "kuralın tam olarak bir kapsamı ve bir alıcısı olmalı"},

	{store.ErrMaintenanceWindowInvalid, http.StatusBadRequest, "geçersiz bakım penceresi"},
	{store.ErrMaintenanceScopeMissing, http.StatusNotFound, "seçilen sunuculardan ya da organizasyonlardan biri bulunamadı"},
	{store.ErrMaintenanceWindowEnded, http.StatusConflict, "bu bakım penceresi bitirilmiş; değiştirilemez"},

	// İletişim kişisi kuralları istemcinin düzeltebileceği istek hatalarıdır.
	{store.ErrContactManagerInvalid, http.StatusBadRequest, "yönetici aynı organizasyondan bir iletişim kişisi olmalı"},
	{store.ErrContactInvalid, http.StatusBadRequest, "en az bir iletişim yolu (telefon ya da e-posta) gerekli ve kişi kendi yöneticisi olamaz"},
}

// storeError, bir store çağrısının hatasını yanıta çevirir: adlandırılmış bir neden (storeReasons) kendi durum kodu ve
// metniyle, genel store.ErrNotFound notFoundMessage ile 404 döner; geri kalan her hata fail(op) ile 500 olur.
func storeError(err error, notFoundMessage string, fail failFunc, op string) error {
	for _, r := range storeReasons {
		if errors.Is(err, r.err) {
			return newError(r.status, r.message)
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		return notFound(notFoundMessage)
	}
	return fail(op, err)
}
