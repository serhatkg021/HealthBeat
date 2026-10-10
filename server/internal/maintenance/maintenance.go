// Package maintenance, bakım pencerelerinin zaman hesabıdır: bir pencerenin tekrarları, bir anda sürüp sürmediği,
// sıradaki tekrarları ve kesintisiz bakımın ne zaman bittiği. Veritabanına ya da HTTP'ye dokunmaz; saat dilimi
// çağırandan gelir (kurulumun saat dilimi). Kurallar migration 000008'deki alanlara göredir:
//
//   - Tek seferlik pencere StartsAt–EndsAt mutlak aralığıdır.
//   - Tekrarlı pencerede günler ValidFrom'dan (çapa) sayılır: günlükte her RepeatEvery günde bir; haftalıkta haftalar
//     ValidFrom'un haftasından (Pazartesi başlangıçlı) sayılır ve seçili günler her RepeatEvery haftada bir; aylıkta
//     aylar ValidFrom'un ayından sayılır. ValidFrom'dan önceki günler ve ValidUntil'den sonraki günler tekrar
//     başlatmaz; ValidUntil günü başlayan tekrar tam süresince sürer.
//   - Tekrar, günün StartMinute'inde (yerel saat) başlar ve DurationMinutes gerçek dakika sürer; gece yarısını, ay
//     sonunu geçebilir. Yaz saatinde hiç yaşanmayan saat ileri kayar, iki kez yaşanan saat ilk geldiğinde başlar.
//   - İstisna: atlanan tekrar yok sayılır, erken bitirilen o anda biter. EndedAt'ten sonra başlayacak tekrarlar yok
//     sayılır, süren tekrar EndedAt'te biter.
package maintenance

import (
	"slices"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

// Occurrence, bir pencerenin tek bir tekrarıdır (tek seferlik pencerenin kendisi de bir tekrardır). Start planlanan
// başlangıçtır ve istisnaların anahtarıdır; End istisnalar ve pencerenin bitirilmesi uygulanmış bitiştir. Aralık
// [Start, End) yarı açıktır.
type Occurrence struct {
	WindowID uuid.UUID
	Start    time.Time
	End      time.Time
}

// maxDuration, bir tekrarın en uzun süresidir (haftalık ve aylıkta 7 gün): bir anı kapsayabilecek tekrarlar en çok bu
// kadar önce başlamış olabilir.
const maxDuration = 7 * 24 * time.Hour

// date, saatsiz bir takvim günüdür (UTC gece yarısı olarak taşınır; gün aritmetiği yaz saatinden etkilenmez).
type date = time.Time

func dateOf(t time.Time, loc *time.Location) date {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func civil(t time.Time) date {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func daysBetween(a, b date) int { return int(b.Sub(a).Hours() / 24) }

// isoWeekday, Pazartesi = 1 … Pazar = 7.
func isoWeekday(d date) int {
	if w := int(d.Weekday()); w != 0 {
		return w
	}
	return 7
}

// wallInstant, loc'ta d gününün minute'inci dakikasındaki anı döndürür. Yaz saati geçişinde o saat hiç yaşanmıyorsa
// geçişten önceki ofsetle hesaplanır (ör. 02:30 → 03:30); iki kez yaşanıyorsa ilk geldiği an seçilir.
func wallInstant(d date, minute int, loc *time.Location) time.Time {
	wall := d.Add(time.Duration(minute) * time.Minute) // yerel saat, UTC'ymiş gibi
	_, before := wall.Add(-48 * time.Hour).In(loc).Zone()
	_, after := wall.Add(48 * time.Hour).In(loc).Zone()
	var best time.Time
	for _, off := range []int{before, after} {
		c := wall.Add(-time.Duration(off) * time.Second)
		l := c.In(loc)
		if l.Year() == wall.Year() && l.YearDay() == wall.YearDay() && l.Hour() == wall.Hour() && l.Minute() == wall.Minute() {
			if best.IsZero() || c.Before(best) {
				best = c
			}
		}
	}
	if best.IsZero() {
		best = wall.Add(-time.Duration(before) * time.Second)
	}
	return best
}

// FromLocal, loc'ta wall'un takvim günü ve saat:dakikasının gösterdiği anı döndürür (wall'un kendi saat dilimi yok
// sayılır). Tekrarlarla aynı kural: yaz saatinde hiç yaşanmayan saat ileri kayar, iki kez yaşanan saat ilkidir.
func FromLocal(wall time.Time, loc *time.Location) time.Time {
	return wallInstant(civil(wall), wall.Hour()*60+wall.Minute(), loc)
}

// nextStartDate, d ve sonrasında tekrarın başladığı ilk günü döndürür (ValidUntil'i aşarsa ok=false). Pencere tekrarlı
// olmalıdır.
func nextStartDate(w model.MaintenanceWindow, d date) (date, bool) {
	anchor := civil(*w.ValidFrom)
	if d.Before(anchor) {
		d = anchor
	}
	n := max(w.RepeatEvery, 1)
	var out date
	switch w.Recurrence {
	case model.RecurrenceDaily:
		k := daysBetween(anchor, d)
		if r := k % n; r != 0 {
			k += n - r
		}
		out = anchor.AddDate(0, 0, k)
	case model.RecurrenceWeekly:
		monday := anchor.AddDate(0, 0, 1-isoWeekday(anchor))
		mask := 0
		if w.Weekdays != nil {
			mask = *w.Weekdays
		}
		if mask&0x7f == 0 {
			return date{}, false
		}
		for {
			week := daysBetween(monday, d) / 7
			if r := week % n; r != 0 {
				d = monday.AddDate(0, 0, (week+n-r)*7) // hizalı haftanın Pazartesisi
				continue
			}
			for ; daysBetween(monday, d)/7 == week; d = d.AddDate(0, 0, 1) {
				if mask&(1<<(isoWeekday(d)-1)) != 0 {
					out = d
					break
				}
			}
			if !out.IsZero() {
				break
			}
		}
	case model.RecurrenceMonthly:
		months := (d.Year()-anchor.Year())*12 + int(d.Month()) - int(anchor.Month())
		if r := months % n; r != 0 {
			months += n - r
		}
		for {
			first := time.Date(anchor.Year(), anchor.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
			if c := monthDay(w, first); !c.Before(d) {
				out = c
				break
			}
			months += n
		}
	default:
		return date{}, false
	}
	if w.ValidUntil != nil && out.After(civil(*w.ValidUntil)) {
		return date{}, false
	}
	return out, true
}

// monthDay, first ile başlayan ayda tekrarın günüdür: ayın günü (1–28 ya da son gün) ya da ayın n'inci (ya da son)
// haftanın günü.
func monthDay(w model.MaintenanceWindow, first date) date {
	last := first.AddDate(0, 1, -1)
	if w.MonthDay != nil {
		if *w.MonthDay == model.MonthLast {
			return last
		}
		return first.AddDate(0, 0, *w.MonthDay-1)
	}
	wd := 1
	if w.MonthWeekday != nil {
		wd = *w.MonthWeekday
	}
	if w.MonthWeek != nil && *w.MonthWeek == model.MonthLast {
		return last.AddDate(0, 0, -((isoWeekday(last) - wd + 7) % 7))
	}
	nth := 1
	if w.MonthWeek != nil {
		nth = *w.MonthWeek
	}
	return first.AddDate(0, 0, (wd-isoWeekday(first)+7)%7+(nth-1)*7)
}

// planned, d günü başlayan tekrarın istisnasız aralığıdır.
func planned(w model.MaintenanceWindow, d date, loc *time.Location) Occurrence {
	start := wallInstant(d, *w.StartMinute, loc)
	return Occurrence{WindowID: w.ID, Start: start, End: start.Add(time.Duration(*w.DurationMinutes) * time.Minute)}
}

// apply, istisnaları ve pencerenin bitirilmesini o'ya uygular; tekrar hiç yaşanmayacaksa ok=false.
func apply(w model.MaintenanceWindow, o Occurrence) (Occurrence, bool) {
	for _, ov := range w.Overrides {
		if ov.OccurrenceStart.Equal(o.Start) {
			if ov.EndedAt == nil {
				return o, false
			}
			if ov.EndedAt.Before(o.End) {
				o.End = *ov.EndedAt
			}
		}
	}
	if w.EndedAt != nil && w.EndedAt.Before(o.End) {
		o.End = *w.EndedAt
	}
	return o, o.End.After(o.Start)
}

// Occurrences, w'nin [from, to) aralığına değen tekrarlarını başlangıca göre sıralı döndürür.
func Occurrences(w model.MaintenanceWindow, loc *time.Location, from, to time.Time) []Occurrence {
	if !to.After(from) {
		return nil
	}
	if w.Recurrence == model.RecurrenceOnce {
		if w.StartsAt == nil || w.EndsAt == nil {
			return nil
		}
		o, ok := apply(w, Occurrence{WindowID: w.ID, Start: *w.StartsAt, End: *w.EndsAt})
		if !ok || !o.Start.Before(to) || !o.End.After(from) {
			return nil
		}
		return []Occurrence{o}
	}
	if w.ValidFrom == nil || w.StartMinute == nil || w.DurationMinutes == nil {
		return nil
	}
	var out []Occurrence
	last := dateOf(to, loc)
	for d, ok := nextStartDate(w, dateOf(from.Add(-maxDuration), loc).AddDate(0, 0, -1)); ok && !d.After(last); d, ok = nextStartDate(w, d.AddDate(0, 0, 1)) {
		o, live := apply(w, planned(w, d, loc))
		if live && o.Start.Before(to) && o.End.After(from) {
			out = append(out, o)
		}
	}
	return out
}

// Current, w'nin t anında süren tekrarını döndürür.
func Current(w model.MaintenanceWindow, loc *time.Location, t time.Time) (Occurrence, bool) {
	for _, o := range Occurrences(w, loc, t, t.Add(time.Nanosecond)) {
		if !o.Start.After(t) && o.End.After(t) {
			return o, true
		}
	}
	return Occurrence{}, false
}

// Next, w'nin after'dan sonra başlayacak (atlanmamış) ilk n tekrarını döndürür. Tek seferlik pencerede en çok bir
// tekrar vardır.
func Next(w model.MaintenanceWindow, loc *time.Location, after time.Time, n int) []Occurrence {
	if n <= 0 {
		return nil
	}
	if w.Recurrence == model.RecurrenceOnce {
		if os := Occurrences(w, loc, after, after.Add(1<<62)); len(os) == 1 && os[0].Start.After(after) {
			return os
		}
		return nil
	}
	if w.ValidFrom == nil || w.StartMinute == nil || w.DurationMinutes == nil {
		return nil
	}
	var out []Occurrence
	// Sınır, istisnalarla dolu bir pencerede aramanın uzamasını engeller (atlanan tekrarlar sayılmaz).
	guard := 0
	for d, ok := nextStartDate(w, dateOf(after, loc).AddDate(0, 0, -1)); ok && len(out) < n && guard < 10000; d, ok = nextStartDate(w, d.AddDate(0, 0, 1)) {
		guard++
		if o, live := apply(w, planned(w, d, loc)); live && o.Start.After(after) {
			out = append(out, o)
		}
	}
	return out
}

// horizon, ActiveAt'in kesintisiz bakımın bitişini en çok ne kadar ileride aradığıdır: art arda gelen tekrarlar
// (ör. her gün 24 saat) bundan uzun sürse de bitiş bu sınırda verilir.
const horizon = 32 * 24 * time.Hour

// ActiveAt, ws'den en az biri t anında sürüyorsa kesintisiz bakımın bitişini döndürür: çakışan ya da art arda gelen
// tekrarlar (aynı ya da farklı pencerelerden) birleştirilir.
func ActiveAt(ws []model.MaintenanceWindow, loc *time.Location, t time.Time) (until time.Time, active bool) {
	var all []Occurrence
	for _, w := range ws {
		all = append(all, Occurrences(w, loc, t, t.Add(horizon))...)
	}
	slices.SortFunc(all, func(a, b Occurrence) int { return a.Start.Compare(b.Start) })
	for _, o := range all {
		if !o.Start.After(t) && o.End.After(t) && o.End.After(until) {
			until, active = o.End, true
		}
	}
	if !active {
		return time.Time{}, false
	}
	for _, o := range all {
		if o.Start.After(until) {
			break // sıralı: bundan sonrakiler de kesintisiz zincire değmez
		}
		if o.End.After(until) {
			until = o.End
		}
	}
	if limit := t.Add(horizon); until.After(limit) {
		until = limit
	}
	return until, true
}
