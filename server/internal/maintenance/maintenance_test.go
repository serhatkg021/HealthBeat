package maintenance

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func ptr[T any](v T) *T { return &v }

func day(s string) *time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &d
}

// at, loc'ta "2006-01-02 15:04" yerel saatini anlık zamana çevirir.
func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// spans, tekrarları loc'ta "başlangıç–bitiş" olarak yazar (bitiş başka güne düşerse tarihiyle).
func spans(os []Occurrence, loc *time.Location) []string {
	out := make([]string, 0, len(os))
	for _, o := range os {
		s, e := o.Start.In(loc), o.End.In(loc)
		end := e.Format("15:04")
		if e.YearDay() != s.YearDay() {
			end = e.Format("2006-01-02 15:04")
		}
		out = append(out, s.Format("2006-01-02 15:04")+"–"+end)
	}
	return out
}

func recurring(kind, from string, startMinute, duration, every int) model.MaintenanceWindow {
	return model.MaintenanceWindow{ID: uuid.New(), Title: "t", Recurrence: kind, StartMinute: &startMinute,
		DurationMinutes: &duration, RepeatEvery: every, ValidFrom: day(from), HostIDs: []uuid.UUID{uuid.New()}}
}

func expect(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", name, got, want)
	}
}

func TestDailyCountsFromTheAnchor(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := recurring(model.RecurrenceDaily, "2026-10-10", 120, 120, 2)
	expect(t, "every 2 days", spans(Next(w, ist, at(t, ist, "2026-10-11 00:00"), 3), ist),
		[]string{"2026-10-12 02:00–04:00", "2026-10-14 02:00–04:00", "2026-10-16 02:00–04:00"})

	// Geçmiş bir çapa: 9'u sayılırsa tekrarlar tek günlere düşer; geçmiş tekrarlar yok sayılır.
	w.ValidFrom = day("2026-10-09")
	expect(t, "past anchor", spans(Next(w, ist, at(t, ist, "2026-10-11 03:00"), 2), ist),
		[]string{"2026-10-13 02:00–04:00", "2026-10-15 02:00–04:00"})
	// Başlangıç bugün ve saat henüz gelmediyse bugünkü tekrar da yapılır.
	w.ValidFrom = day("2026-10-11")
	expect(t, "today, not yet", spans(Next(w, ist, at(t, ist, "2026-10-11 01:00"), 1), ist), []string{"2026-10-11 02:00–04:00"})
}

func TestWeeklyCountsWeeksFromTheAnchorWeek(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := recurring(model.RecurrenceWeekly, "2026-10-07", 22*60, 90, 2) // Çarşamba
	w.Weekdays = ptr(1<<1 | 1<<3)                                      // Salı, Perşembe
	// Çapanın haftasında Salı (6'sı) başlangıçtan önce kalır; sonra iki haftada bir.
	expect(t, "every 2 weeks", spans(Next(w, ist, at(t, ist, "2026-10-01 00:00"), 4), ist),
		[]string{"2026-10-08 22:00–23:30", "2026-10-20 22:00–23:30", "2026-10-22 22:00–23:30", "2026-11-03 22:00–23:30"})

	w.RepeatEvery = 1
	w.Weekdays = ptr(1<<0 | 1<<1 | 1<<2 | 1<<3 | 1<<4) // hafta içi
	expect(t, "weekdays", spans(Next(w, ist, at(t, ist, "2026-10-09 23:00"), 3), ist),
		[]string{"2026-10-12 22:00–23:30", "2026-10-13 22:00–23:30", "2026-10-14 22:00–23:30"})
}

func TestMonthly(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	from := at(t, ist, "2026-09-30 00:00")
	cases := []struct {
		name  string
		from  string
		every int
		set   func(w *model.MaintenanceWindow)
		after time.Time
		want  []string
	}{
		{"15th", "2026-10-01", 1, func(w *model.MaintenanceWindow) { w.MonthDay = ptr(15) }, from,
			[]string{"2026-10-15", "2026-11-15", "2026-12-15"}},
		{"15th, anchor after the day", "2026-10-20", 1, func(w *model.MaintenanceWindow) { w.MonthDay = ptr(15) }, from,
			[]string{"2026-11-15", "2026-12-15", "2027-01-15"}},
		{"last day over a leap February", "2027-12-01", 1, func(w *model.MaintenanceWindow) { w.MonthDay = ptr(model.MonthLast) },
			at(t, ist, "2027-11-30 00:00"), []string{"2027-12-31", "2028-01-31", "2028-02-29", "2028-03-31"}},
		{"first Sunday", "2026-10-01", 1, func(w *model.MaintenanceWindow) { w.MonthWeek, w.MonthWeekday = ptr(1), ptr(7) }, from,
			[]string{"2026-10-04", "2026-11-01", "2026-12-06"}},
		{"second Tuesday", "2026-10-01", 1, func(w *model.MaintenanceWindow) { w.MonthWeek, w.MonthWeekday = ptr(2), ptr(2) }, from,
			[]string{"2026-10-13", "2026-11-10"}},
		{"last Friday", "2026-10-01", 1, func(w *model.MaintenanceWindow) { w.MonthWeek, w.MonthWeekday = ptr(model.MonthLast), ptr(5) }, from,
			[]string{"2026-10-30", "2026-11-27", "2026-12-25"}},
		{"first Sunday every 3 months", "2026-10-01", 3, func(w *model.MaintenanceWindow) { w.MonthWeek, w.MonthWeekday = ptr(1), ptr(7) }, from,
			[]string{"2026-10-04", "2027-01-03", "2027-04-04", "2027-07-04"}},
	}
	for _, c := range cases {
		w := recurring(model.RecurrenceMonthly, c.from, 3*60, 120, c.every)
		c.set(&w)
		var got []string
		for _, o := range Next(w, ist, c.after, len(c.want)) {
			got = append(got, o.Start.In(ist).Format("2006-01-02"))
		}
		expect(t, c.name, got, c.want)
	}
}

func TestOccurrenceCrossesMidnight(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := recurring(model.RecurrenceDaily, "2026-10-05", 23*60, 180, 1)
	running := func(s string) string {
		t.Helper()
		if o, ok := Current(w, ist, at(t, ist, s)); ok {
			return spans([]Occurrence{o}, ist)[0]
		}
		return ""
	}
	if got := running("2026-10-06 01:00"); got != "2026-10-05 23:00–2026-10-06 02:00" {
		t.Errorf("01:00 the next day: %q, want the previous night's occurrence", got)
	}
	for _, s := range []string{"2026-10-06 02:00", "2026-10-06 22:59"} {
		if got := running(s); got != "" {
			t.Errorf("%s: %q, want none", s, got)
		}
	}
	// Bitiş günü başlayan son tekrar tam süresince sürer, sonrası başlamaz.
	w.ValidUntil = day("2026-10-07")
	if got := running("2026-10-08 01:00"); got != "2026-10-07 23:00–2026-10-08 02:00" {
		t.Errorf("after the last day: %q, want the last occurrence to finish", got)
	}
	if got := running("2026-10-08 23:30"); got != "" {
		t.Errorf("a day after the series: %q, want none", got)
	}

	// Pazar gecesi başlayan haftalık tekrar Pazartesi seçili olmasa da sürer; hafta sonu boyunca süren pencere.
	weekly := recurring(model.RecurrenceWeekly, "2026-10-01", 23*60, 180, 1)
	weekly.Weekdays = ptr(1 << 6)
	if _, ok := Current(weekly, ist, at(t, ist, "2026-10-12 01:30")); !ok {
		t.Error("Sunday 23:00 + 3h is not running on Monday 01:30")
	}
	weekend := recurring(model.RecurrenceWeekly, "2026-10-01", 22*60, 32*60, 1)
	weekend.Weekdays = ptr(1 << 5)
	if _, ok := Current(weekend, ist, at(t, ist, "2026-10-12 05:59")); !ok {
		t.Error("Saturday 22:00 + 32h is not running on Monday 05:59")
	}
	if _, ok := Current(weekend, ist, at(t, ist, "2026-10-12 06:00")); ok {
		t.Error("Saturday 22:00 + 32h is still running on Monday 06:00")
	}
}

func TestOverridesAndEndingTheWindow(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := recurring(model.RecurrenceWeekly, "2026-10-04", 3*60, 120, 2)
	w.Weekdays = ptr(1 << 6) // Pazar: 4, 18 Ekim, 1, 15 Kasım…
	w.Overrides = []model.MaintenanceOverride{{OccurrenceStart: at(t, ist, "2026-10-18 03:00")}}
	// Atlamak sayımı bozmaz: sonraki tekrar yine 1 Kasım'dır.
	expect(t, "skip", spans(Next(w, ist, at(t, ist, "2026-10-05 00:00"), 2), ist),
		[]string{"2026-11-01 03:00–05:00", "2026-11-15 03:00–05:00"})

	w.Overrides = append(w.Overrides, model.MaintenanceOverride{OccurrenceStart: at(t, ist, "2026-11-01 03:00"), EndedAt: ptr(at(t, ist, "2026-11-01 03:40"))})
	if o, ok := Current(w, ist, at(t, ist, "2026-11-01 03:30")); !ok || !o.End.Equal(at(t, ist, "2026-11-01 03:40")) {
		t.Errorf("early end: %+v ok=%v, want the occurrence to end at 03:40", o, ok)
	}
	if _, ok := Current(w, ist, at(t, ist, "2026-11-01 03:50")); ok {
		t.Error("an occurrence ended at 03:40 is still running at 03:50")
	}

	w.EndedAt = ptr(at(t, ist, "2026-11-15 03:10"))
	if o, ok := Current(w, ist, at(t, ist, "2026-11-15 03:05")); !ok || !o.End.Equal(*w.EndedAt) {
		t.Errorf("before the end: %+v ok=%v, want it to run until the window ended", o, ok)
	}
	if _, ok := Current(w, ist, at(t, ist, "2026-11-15 03:20")); ok {
		t.Error("an ended window is still running")
	}
	if got := Next(w, ist, at(t, ist, "2026-11-15 03:20"), 1); len(got) != 0 {
		t.Errorf("next of an ended window = %v, want none", spans(got, ist))
	}

	once := model.MaintenanceWindow{ID: uuid.New(), Recurrence: model.RecurrenceOnce,
		StartsAt: ptr(at(t, ist, "2026-10-12 01:00")), EndsAt: ptr(at(t, ist, "2026-10-12 05:00"))}
	expect(t, "one-off", spans(Occurrences(once, ist, at(t, ist, "2026-10-12 04:00"), at(t, ist, "2026-10-13 00:00")), ist),
		[]string{"2026-10-12 01:00–05:00"})
	if got := Next(once, ist, at(t, ist, "2026-10-12 02:00"), 1); len(got) != 0 {
		t.Errorf("next of a running one-off window = %v, want none", spans(got, ist))
	}
}

func TestActiveAtMergesOverlappingAndAdjacentOccurrences(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	once := func(from, to string) model.MaintenanceWindow {
		return model.MaintenanceWindow{ID: uuid.New(), Recurrence: model.RecurrenceOnce,
			StartsAt: ptr(at(t, ist, from)), EndsAt: ptr(at(t, ist, to))}
	}
	ws := []model.MaintenanceWindow{
		once("2026-10-12 02:00", "2026-10-12 03:00"),
		once("2026-10-12 02:30", "2026-10-12 04:00"),
		recurring(model.RecurrenceDaily, "2026-10-01", 4*60, 60, 1), // 04:00–05:00, art arda
		once("2026-10-12 06:00", "2026-10-12 07:00"),                // arada boşluk var
	}
	until, ok := ActiveAt(ws, ist, at(t, ist, "2026-10-12 02:15"))
	if !ok || !until.Equal(at(t, ist, "2026-10-12 05:00")) {
		t.Errorf("ActiveAt 02:15 = %v %v, want until 05:00", until.In(ist), ok)
	}
	if _, ok := ActiveAt(ws, ist, at(t, ist, "2026-10-12 05:00")); ok {
		t.Error("active at 05:00, want the gap before 06:00")
	}

	always := recurring(model.RecurrenceDaily, "2026-10-01", 0, 24*60, 1)
	now := at(t, ist, "2026-10-12 12:00")
	if until, ok := ActiveAt([]model.MaintenanceWindow{always}, ist, now); !ok || !until.Equal(now.Add(horizon)) {
		t.Errorf("back-to-back days: until %v ok=%v, want the horizon", until, ok)
	}
}

// Yaz saati: hiç yaşanmayan saat ileri kayar, iki kez yaşanan saat ilk geldiğinde başlar, süre gerçek dakikadır.
// Europe/Berlin 2026: 29 Mart 02:00 → 03:00, 25 Ekim 03:00 → 02:00.
func TestDaylightSavingTransitions(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	utc := func(o Occurrence) string {
		return o.Start.UTC().Format("01-02 15:04") + "–" + o.End.UTC().Format("15:04")
	}
	w := recurring(model.RecurrenceDaily, "2026-03-27", 150, 60, 1) // 02:30, 1 saat
	var got []string
	for _, o := range Next(w, berlin, time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC), 4) {
		got = append(got, utc(o))
	}
	expect(t, "spring forward (UTC)", got, []string{"03-27 01:30–02:30", "03-28 01:30–02:30", "03-29 01:30–02:30", "03-30 00:30–01:30"})
	if s := Next(w, berlin, time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), 1)[0].Start.In(berlin).Format("15:04"); s != "03:30" {
		t.Errorf("02:30 on 29 March starts at %s local, want 03:30", s)
	}

	w.ValidFrom = day("2026-10-24")
	got = nil
	for _, o := range Next(w, berlin, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), 3) {
		got = append(got, utc(o))
	}
	expect(t, "fall back (UTC)", got, []string{"10-24 00:30–01:30", "10-25 00:30–01:30", "10-26 01:30–02:30"})

	across := recurring(model.RecurrenceDaily, "2026-03-29", 60, 180, 1) // 01:00 + 3 saat, geçişin üstünden
	o := Next(across, berlin, time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC), 1)[0]
	if s, e := o.Start.In(berlin).Format("15:04"), o.End.In(berlin).Format("15:04"); s != "01:00" || e != "05:00" {
		t.Errorf("01:00 + 180 min over the transition = %s–%s local, want 01:00–05:00 (3 real hours)", s, e)
	}

	ist := mustLoc(t, "Europe/Istanbul")
	w.ValidFrom = day("2026-03-27")
	for _, o := range Next(w, ist, time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC), 4) {
		if s := o.Start.UTC().Format("15:04"); s != "23:30" {
			t.Errorf("Istanbul 02:30 = %s UTC, want 23:30 every day", s)
		}
	}
}

func TestValidate(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	start := at(t, ist, "2026-10-12 01:00")
	host := []uuid.UUID{uuid.New()}
	base := func(kind string) model.MaintenanceWindow {
		w := recurring(kind, "2026-10-01", 120, 60, 1)
		w.HostIDs = host
		switch kind {
		case model.RecurrenceWeekly:
			w.Weekdays = ptr(1)
		case model.RecurrenceMonthly:
			w.MonthDay = ptr(1)
		}
		return w
	}
	once := model.MaintenanceWindow{Title: "x", Recurrence: model.RecurrenceOnce, StartsAt: &start, EndsAt: ptr(start.Add(time.Hour)), RepeatEvery: 1, HostIDs: host}

	for _, ok := range []model.MaintenanceWindow{once, base(model.RecurrenceDaily), base(model.RecurrenceWeekly), base(model.RecurrenceMonthly),
		func() model.MaintenanceWindow {
			w := base(model.RecurrenceMonthly)
			w.MonthDay, w.MonthWeek, w.MonthWeekday = nil, ptr(model.MonthLast), ptr(5)
			return w
		}(),
		func() model.MaintenanceWindow {
			w := base(model.RecurrenceDaily)
			w.HostIDs, w.OrgIDs = nil, host
			return w
		}(),
	} {
		if err := Validate(ok); err != nil {
			t.Errorf("%s rejected: %v", ok.Recurrence, err)
		}
	}

	bad := []struct {
		field string
		edit  func(w *model.MaintenanceWindow)
		kind  string
	}{
		{"title", func(w *model.MaintenanceWindow) { w.Title = "   " }, model.RecurrenceDaily},
		{"title", func(w *model.MaintenanceWindow) { w.Title = strings.Repeat("ş", 201) }, model.RecurrenceDaily},
		{"scope", func(w *model.MaintenanceWindow) { w.HostIDs = nil }, model.RecurrenceDaily},
		{"recurrence", func(w *model.MaintenanceWindow) { w.Recurrence = "yearly" }, model.RecurrenceDaily},
		{"start_minute", func(w *model.MaintenanceWindow) { w.StartMinute = ptr(1440) }, model.RecurrenceDaily},
		{"duration_minutes", func(w *model.MaintenanceWindow) { w.DurationMinutes = ptr(1441) }, model.RecurrenceDaily},
		{"duration_minutes", func(w *model.MaintenanceWindow) { w.DurationMinutes = ptr(10081) }, model.RecurrenceWeekly},
		{"repeat_every", func(w *model.MaintenanceWindow) { w.RepeatEvery = 31 }, model.RecurrenceDaily},
		{"repeat_every", func(w *model.MaintenanceWindow) { w.RepeatEvery = 13 }, model.RecurrenceMonthly},
		{"valid_from", func(w *model.MaintenanceWindow) { w.ValidFrom = nil }, model.RecurrenceDaily},
		{"valid_until", func(w *model.MaintenanceWindow) { w.ValidUntil = day("2026-09-30") }, model.RecurrenceDaily},
		{"weekdays", func(w *model.MaintenanceWindow) { w.Weekdays = ptr(0) }, model.RecurrenceWeekly},
		{"weekdays", func(w *model.MaintenanceWindow) { w.Weekdays = ptr(1) }, model.RecurrenceDaily},
		{"month_day", func(w *model.MaintenanceWindow) { w.MonthDay = ptr(29) }, model.RecurrenceMonthly},
		{"month_day", func(w *model.MaintenanceWindow) { w.MonthWeek, w.MonthWeekday = ptr(1), ptr(1) }, model.RecurrenceMonthly},
		{"month_day", func(w *model.MaintenanceWindow) { w.MonthDay = nil }, model.RecurrenceMonthly},
		{"month_week", func(w *model.MaintenanceWindow) { w.MonthDay, w.MonthWeek, w.MonthWeekday = nil, ptr(5), ptr(1) }, model.RecurrenceMonthly},
		{"recurrence", func(w *model.MaintenanceWindow) { w.StartsAt = &start }, model.RecurrenceDaily},
	}
	for _, c := range bad {
		w := base(c.kind)
		c.edit(&w)
		var fe *FieldError
		if err := Validate(w); !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%s/%s: err = %v, want a %s field error", c.kind, c.field, err, c.field)
		}
	}
	for field, edit := range map[string]func(w *model.MaintenanceWindow){
		"ends_at":    func(w *model.MaintenanceWindow) { w.EndsAt = &start },
		"starts_at":  func(w *model.MaintenanceWindow) { w.StartsAt = nil },
		"recurrence": func(w *model.MaintenanceWindow) { w.ValidFrom = day("2026-10-01") },
	} {
		w := once
		edit(&w)
		var fe *FieldError
		if err := Validate(w); !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("once/%s: err = %v", field, err)
		}
	}
}
