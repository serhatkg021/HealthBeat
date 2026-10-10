package model

import (
	"time"

	"github.com/google/uuid"
)

// Bakım penceresinin tekrar türleri (maintenance_windows.recurrence).
const (
	RecurrenceOnce    = "once"
	RecurrenceDaily   = "daily"
	RecurrenceWeekly  = "weekly"
	RecurrenceMonthly = "monthly"
)

// MonthLast, aylık tekrarda "ayın son günü" (MonthDay) ya da "ayın son ... günü" (MonthWeek) anlamındadır.
const MonthLast = -1

// MaintenanceWindow, bir bakım penceresidir: sürerken kapsamındaki sunucuların alert'leri kaydedilir ama bildirimi
// gönderilmez. Tek seferlik pencerede StartsAt–EndsAt mutlak aralıktır; tekrarlıda alanlar kurulumun saat dilimine
// göre yorumlanır (bkz. migration 000008 ve internal/maintenance).
type MaintenanceWindow struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Recurrence string    `json:"recurrence"`
	// Tek seferlik.
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	// Tekrarlı: gün içindeki başlangıç dakikası (02:30 = 150), süre, aralık ("her N'de bir"), haftalıkta günler
	// (bit 0 = Pazartesi … bit 6 = Pazar), aylıkta ayın günü (1–28, MonthLast) ya da ayın n'inci haftanın günü
	// (MonthWeek 1–4 ya da MonthLast, MonthWeekday 1 = Pazartesi … 7 = Pazar).
	StartMinute     *int `json:"start_minute,omitempty"`
	DurationMinutes *int `json:"duration_minutes,omitempty"`
	RepeatEvery     int  `json:"repeat_every"`
	Weekdays        *int `json:"weekdays,omitempty"`
	MonthDay        *int `json:"month_day,omitempty"`
	MonthWeek       *int `json:"month_week,omitempty"`
	MonthWeekday    *int `json:"month_weekday,omitempty"`
	// ValidFrom ve ValidUntil takvim günleridir (saat bilgisi yok; UTC gece yarısı olarak taşınır). ValidFrom sayımın
	// çapasıdır; geçmişte olabilir.
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
	// EndedAt, "pencereyi bitir" anıdır: seri kapanır, süren tekrar da o anda biter.
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	// Kapsam: seçilen sunucular ve organizasyonlar (organizasyon yalnızca doğrudan bağlı sunucularını kapsar).
	HostIDs []uuid.UUID `json:"host_ids"`
	OrgIDs  []uuid.UUID `json:"organization_ids"`
	// Overrides, tek tek tekrarların istisnalarıdır (atlanan ya da erken bitirilen).
	Overrides []MaintenanceOverride `json:"overrides,omitempty"`
}

// MaintenanceOverride, tek bir tekrarın istisnasıdır: EndedAt nil ise OccurrenceStart'ta başlayan tekrar atlanmıştır,
// doluysa o anda erken bitirilmiştir.
type MaintenanceOverride struct {
	OccurrenceStart time.Time  `json:"occurrence_start"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
}
