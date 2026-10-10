package maintenance

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"healthbeat-server/internal/model"
)

// FieldError, bir pencerenin neden kabul edilmediğini söyler: Field API'deki alan adıdır, Message istemciye
// gösterilir.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Sınırlar (migration 000008'deki CHECK'lerle aynı).
const (
	maxTitle         = 200
	maxDailyMinutes  = 24 * 60
	maxLongMinutes   = 7 * 24 * 60
	maxDailyRepeat   = 30
	maxWeeklyRepeat  = 12
	maxMonthlyRepeat = 12
	maxMonthDay      = 28
	maxMonthWeek     = 4
	allWeekdays      = 1<<7 - 1
	minutesInDay     = 24 * 60
)

func fieldErr(field, format string, a ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, a...)}
}

// Validate, w'nin kurallara uyup uymadığını denetler ve ilk sorunu *FieldError olarak döndürür. Kapsam (sunucular ya
// da organizasyonlar) boş olamaz. Alanlar türe göre ya dolu ya boş olmalıdır; boş bırakılması gereken bir alanın
// dolu gelmesi de hatadır (istemci karışık bir pencere göndermesin).
func Validate(w model.MaintenanceWindow) error {
	title := strings.TrimSpace(w.Title)
	if title == "" {
		return fieldErr("title", "açıklama boş olamaz")
	}
	if utf8.RuneCountInString(title) > maxTitle {
		return fieldErr("title", "açıklama en çok %d karakter olabilir", maxTitle)
	}
	if len(w.HostIDs) == 0 && len(w.OrgIDs) == 0 {
		return fieldErr("scope", "en az bir sunucu ya da organizasyon seçilmeli")
	}
	switch w.Recurrence {
	case model.RecurrenceOnce:
		return validateOnce(w)
	case model.RecurrenceDaily, model.RecurrenceWeekly, model.RecurrenceMonthly:
		return validateRecurring(w)
	default:
		return fieldErr("recurrence", "tek seferlik, günlük, haftalık ya da aylık olmalı")
	}
}

func validateOnce(w model.MaintenanceWindow) error {
	switch {
	case w.StartsAt == nil:
		return fieldErr("starts_at", "başlangıç gerekli")
	case w.EndsAt == nil:
		return fieldErr("ends_at", "bitiş gerekli")
	case !w.EndsAt.After(*w.StartsAt):
		return fieldErr("ends_at", "bitiş başlangıçtan sonra olmalı")
	case w.StartMinute != nil || w.DurationMinutes != nil || w.ValidFrom != nil || w.ValidUntil != nil ||
		w.Weekdays != nil || w.MonthDay != nil || w.MonthWeek != nil || w.MonthWeekday != nil || w.RepeatEvery > 1:
		return fieldErr("recurrence", "tek seferlik pencerede tekrar alanları kullanılmaz")
	}
	return nil
}

func validateRecurring(w model.MaintenanceWindow) error {
	if w.StartsAt != nil || w.EndsAt != nil {
		return fieldErr("recurrence", "tekrarlı pencerede başlangıç ve bitiş anı yerine saat ve süre kullanılır")
	}
	if w.StartMinute == nil || *w.StartMinute < 0 || *w.StartMinute >= minutesInDay {
		return fieldErr("start_minute", "başlangıç saati 00:00 ile 23:59 arasında olmalı")
	}
	maxMinutes, maxRepeat, unit := maxLongMinutes, maxWeeklyRepeat, "hafta"
	switch w.Recurrence {
	case model.RecurrenceDaily:
		maxMinutes, maxRepeat, unit = maxDailyMinutes, maxDailyRepeat, "gün"
	case model.RecurrenceMonthly:
		maxRepeat, unit = maxMonthlyRepeat, "ay"
	}
	if w.DurationMinutes == nil || *w.DurationMinutes < 1 || *w.DurationMinutes > maxMinutes {
		limit := "7 gün"
		if maxMinutes == maxDailyMinutes {
			limit = "24 saat"
		}
		return fieldErr("duration_minutes", "süre 1 dakika ile %s arasında olmalı", limit)
	}
	if w.RepeatEvery < 1 || w.RepeatEvery > maxRepeat {
		return fieldErr("repeat_every", "aralık 1 ile %d %s arasında olmalı", maxRepeat, unit)
	}
	if w.ValidFrom == nil {
		return fieldErr("valid_from", "ilk tekrarın tarihi gerekli")
	}
	if w.ValidUntil != nil && civil(*w.ValidUntil).Before(civil(*w.ValidFrom)) {
		return fieldErr("valid_until", "bitiş tarihi ilk tekrarın tarihinden önce olamaz")
	}

	weekly, monthly := w.Recurrence == model.RecurrenceWeekly, w.Recurrence == model.RecurrenceMonthly
	switch {
	case weekly && (w.Weekdays == nil || *w.Weekdays <= 0 || *w.Weekdays > allWeekdays):
		return fieldErr("weekdays", "en az bir gün seçilmeli")
	case !weekly && w.Weekdays != nil:
		return fieldErr("weekdays", "günler yalnızca haftalık tekrarda seçilir")
	case !monthly && (w.MonthDay != nil || w.MonthWeek != nil || w.MonthWeekday != nil):
		return fieldErr("month_day", "ayın günü yalnızca aylık tekrarda seçilir")
	case !monthly:
		return nil
	}

	switch {
	case w.MonthDay != nil && (w.MonthWeek != nil || w.MonthWeekday != nil):
		return fieldErr("month_day", "ayın günü ya da ayın n'inci haftanın günü seçilmeli, ikisi birden değil")
	case w.MonthDay != nil:
		if d := *w.MonthDay; d != model.MonthLast && (d < 1 || d > maxMonthDay) {
			return fieldErr("month_day", "ayın günü 1 ile %d arasında ya da \"son gün\" olmalı", maxMonthDay)
		}
	case w.MonthWeek == nil || w.MonthWeekday == nil:
		return fieldErr("month_day", "ayın günü ya da ayın n'inci haftanın günü seçilmeli")
	case *w.MonthWeek != model.MonthLast && (*w.MonthWeek < 1 || *w.MonthWeek > maxMonthWeek):
		return fieldErr("month_week", "ilk, ikinci, üçüncü, dördüncü ya da son olmalı")
	case *w.MonthWeekday < 1 || *w.MonthWeekday > 7:
		return fieldErr("month_weekday", "haftanın günü seçilmeli")
	}
	return nil
}
