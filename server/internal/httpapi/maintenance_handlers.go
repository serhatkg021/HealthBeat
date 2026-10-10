package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/access"
	"healthbeat-server/internal/maintenance"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Bakım pencereleri: sürerken kapsamındaki sunucuların alert'leri kaydedilir ama bildirimi gönderilmez. Saatler
// kurulumun saat diliminde yerel saat olarak alınır ve verilir ("2026-10-12T01:00", "02:30"); kesin ana çeviriyi server
// yapar (bkz. internal/maintenance). Yanıtlar ayrıca kesin anı (UTC) taşır.
//
// Görünürlük: pencere, kapsamındaki bir sunucuyu ya da organizasyonu çağıran görebiliyorsa görünür; görülemeyen
// kapsam öğelerinin adı verilmez, yalnızca sayısı. Yönetme: kapsamın tamamı çağıranın yönettiği dalda olmalıdır.

const (
	localDateTime = "2006-01-02T15:04"
	localDate     = "2006-01-02"
	localClock    = "15:04"
	// upcomingCount, ayrıntı ve önizlemede gösterilen sonraki tekrar sayısıdır.
	upcomingCount = 5
)

type maintenanceRequest struct {
	Title           string      `json:"title"`
	Recurrence      string      `json:"recurrence"`
	StartsLocal     *string     `json:"starts_local"`
	EndsLocal       *string     `json:"ends_local"`
	StartTime       *string     `json:"start_time"`
	DurationMinutes *int        `json:"duration_minutes"`
	RepeatEvery     int         `json:"repeat_every"`
	Weekdays        []int       `json:"weekdays"`
	MonthDay        *int        `json:"month_day"`
	MonthWeek       *int        `json:"month_week"`
	MonthWeekday    *int        `json:"month_weekday"`
	ValidFrom       *string     `json:"valid_from"`
	ValidUntil      *string     `json:"valid_until"`
	HostIDs         []uuid.UUID `json:"host_ids"`
	OrgIDs          []uuid.UUID `json:"organization_ids"`
}

// fieldError400, bir alanı adlandıran 400'dür.
func fieldError400(field, message string) error {
	e := newError(http.StatusBadRequest, message)
	e.Fields = map[string]string{field: message}
	return e
}

// window, isteği loc'a göre bir pencereye çevirir ve kurallarını denetler.
func (req maintenanceRequest) window(loc *time.Location) (model.MaintenanceWindow, error) {
	w := model.MaintenanceWindow{
		Title: req.Title, Recurrence: req.Recurrence, DurationMinutes: req.DurationMinutes, RepeatEvery: max(req.RepeatEvery, 1),
		MonthDay: req.MonthDay, MonthWeek: req.MonthWeek, MonthWeekday: req.MonthWeekday,
		HostIDs: dedupe(req.HostIDs), OrgIDs: dedupe(req.OrgIDs),
	}
	parse := func(field, layout string, v *string, set func(time.Time)) error {
		if v == nil || *v == "" {
			return nil
		}
		t, err := time.Parse(layout, *v)
		if err != nil {
			return fieldError400(field, "geçersiz biçim (beklenen "+map[string]string{
				localDateTime: "YYYY-AA-GGTSS:DD", localDate: "YYYY-AA-GG", localClock: "SS:DD"}[layout]+")")
		}
		set(t)
		return nil
	}
	for _, p := range []struct {
		field, layout string
		v             *string
		set           func(time.Time)
	}{
		{"starts_local", localDateTime, req.StartsLocal, func(t time.Time) { at := maintenance.FromLocal(t, loc); w.StartsAt = &at }},
		{"ends_local", localDateTime, req.EndsLocal, func(t time.Time) { at := maintenance.FromLocal(t, loc); w.EndsAt = &at }},
		{"start_time", localClock, req.StartTime, func(t time.Time) { m := t.Hour()*60 + t.Minute(); w.StartMinute = &m }},
		{"valid_from", localDate, req.ValidFrom, func(t time.Time) { w.ValidFrom = &t }},
		{"valid_until", localDate, req.ValidUntil, func(t time.Time) { w.ValidUntil = &t }},
	} {
		if err := parse(p.field, p.layout, p.v, p.set); err != nil {
			return w, err
		}
	}
	if req.Weekdays != nil {
		mask := 0
		for _, d := range req.Weekdays {
			if d < 1 || d > 7 {
				return w, fieldError400("weekdays", "günler 1 (Pazartesi) ile 7 (Pazar) arasında olmalı")
			}
			mask |= 1 << (d - 1)
		}
		w.Weekdays = &mask
	}
	if err := maintenance.Validate(w); err != nil {
		var fe *maintenance.FieldError
		if errors.As(err, &fe) {
			field := fe.Field
			if api, ok := requestField[field]; ok {
				field = api
			}
			return w, fieldError400(field, fe.Message)
		}
		return w, err
	}
	return w, nil
}

// requestField, modeldeki alan adlarının istekteki karşılığıdır (doğrulama hatası formdaki alanın altında görünsün).
var requestField = map[string]string{"starts_at": "starts_local", "ends_at": "ends_local", "start_minute": "start_time"}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

type occurrenceView struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	StartLocal string    `json:"start_local"`
	EndLocal   string    `json:"end_local"`
}

type scopeItem struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type maintenanceView struct {
	ID              uuid.UUID       `json:"id"`
	Title           string          `json:"title"`
	Recurrence      string          `json:"recurrence"`
	StartsAt        *time.Time      `json:"starts_at,omitempty"`
	EndsAt          *time.Time      `json:"ends_at,omitempty"`
	StartsLocal     *string         `json:"starts_local,omitempty"`
	EndsLocal       *string         `json:"ends_local,omitempty"`
	StartTime       *string         `json:"start_time,omitempty"`
	DurationMinutes *int            `json:"duration_minutes,omitempty"`
	RepeatEvery     int             `json:"repeat_every"`
	Weekdays        []int           `json:"weekdays,omitempty"`
	MonthDay        *int            `json:"month_day,omitempty"`
	MonthWeek       *int            `json:"month_week,omitempty"`
	MonthWeekday    *int            `json:"month_weekday,omitempty"`
	ValidFrom       *string         `json:"valid_from,omitempty"`
	ValidUntil      *string         `json:"valid_until,omitempty"`
	EndedAt         *time.Time      `json:"ended_at,omitempty"`
	EndedLocal      *string         `json:"ended_local,omitempty"`
	Hosts           []scopeItem     `json:"hosts"`
	Organizations   []scopeItem     `json:"organizations"`
	HiddenScope     int             `json:"hidden_scope"`
	Status          string          `json:"status"` // active | scheduled | past
	Current         *occurrenceView `json:"current,omitempty"`
	Next            *occurrenceView `json:"next,omitempty"`
	// Last, geçmiş bir pencerenin son yaşanan tekrarıdır (son bir yıl içinde; yoksa verilmez).
	Last     *occurrenceView  `json:"last,omitempty"`
	Upcoming []occurrenceView `json:"upcoming,omitempty"`
	// SkippedLocal, atlanmış ve henüz gelmemiş tekrarların başlangıçlarıdır (yerel saat; listede neden eksik oldukları
	// anlaşılsın diye).
	SkippedLocal []string `json:"skipped_local,omitempty"`
	// CanManage, çağıranın pencereyi yönetebildiğidir (kapsamın tamamı dalında); bitirilmiş pencere yalnızca silinebilir.
	CanManage bool      `json:"can_manage"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func occurrence(o maintenance.Occurrence, loc *time.Location) occurrenceView {
	return occurrenceView{Start: o.Start.UTC(), End: o.End.UTC(), StartLocal: o.Start.In(loc).Format(localDateTime), EndLocal: o.End.In(loc).Format(localDateTime)}
}

func localPtr(t *time.Time, loc *time.Location, layout string) *string {
	if t == nil {
		return nil
	}
	s := t.In(loc).Format(layout)
	return &s
}

func datePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(localDate)
	return &s
}

// maintenanceAccess, bir isteğin pencereleri görme ve yönetme kapsamıdır: super_admin için her şey; diğerleri için
// görülebilen sunucular ve bu sunucuların ya da yönetilen dalın organizasyonları.
type maintenanceAccess struct {
	scope       *access.Scope
	all         bool
	hosts, orgs map[uuid.UUID]bool // görülebilenler (all ise boş)
}

func (d *Deps) maintenanceAccess(ctx context.Context, r *http.Request) (*maintenanceAccess, error) {
	scope := d.scope(r)
	a := &maintenanceAccess{scope: scope, all: scope.IsSuperAdmin(), hosts: map[uuid.UUID]bool{}, orgs: map[uuid.UUID]bool{}}
	if a.all {
		return a, nil
	}
	hostIDs, err := scope.VisibleHostIDs(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := d.hosts.ListByIDs(ctx, hostIDs)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		a.hosts[h.ID], a.orgs[h.OrganizationID] = true, true
	}
	managed, err := scope.ManagedOrgIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range managed {
		a.orgs[id] = true
	}
	return a, nil
}

func (a *maintenanceAccess) visibility() *store.MaintenanceVisibility {
	if a.all {
		return nil
	}
	v := &store.MaintenanceVisibility{}
	for id := range a.hosts {
		v.HostIDs = append(v.HostIDs, id)
	}
	for id := range a.orgs {
		v.OrgIDs = append(v.OrgIDs, id)
	}
	return v
}

func (a *maintenanceAccess) visible(w model.MaintenanceWindow) bool {
	if a.all {
		return true
	}
	return slices.ContainsFunc(w.HostIDs, func(id uuid.UUID) bool { return a.hosts[id] }) ||
		slices.ContainsFunc(w.OrgIDs, func(id uuid.UUID) bool { return a.orgs[id] })
}

// manageable, kapsamın tamamının çağıranın yönettiği dalda olup olmadığını söyler. hostOrg, sunucuların
// organizasyonlarıdır (olmayan sunucu yönetilemez sayılır).
func (a *maintenanceAccess) manageable(ctx context.Context, hostIDs, orgIDs []uuid.UUID, hostOrg map[uuid.UUID]uuid.UUID) (bool, error) {
	if a.all {
		return true, nil
	}
	orgs := slices.Clone(orgIDs)
	for _, id := range hostIDs {
		org, ok := hostOrg[id]
		if !ok {
			return false, nil
		}
		orgs = append(orgs, org)
	}
	for _, org := range orgs {
		ok, err := a.scope.CanManageOrg(ctx, org)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// names, pencerelerin kapsamındaki sunucuların organizasyonlarını ve adlarını, organizasyonların adlarını okur.
type names struct {
	hostOrg   map[uuid.UUID]uuid.UUID
	hostTitle map[uuid.UUID]string
	orgName   map[uuid.UUID]string
}

func (d *Deps) maintenanceNames(ctx context.Context, ws ...model.MaintenanceWindow) (names, error) {
	n := names{hostOrg: map[uuid.UUID]uuid.UUID{}, hostTitle: map[uuid.UUID]string{}, orgName: map[uuid.UUID]string{}}
	var hostIDs, orgIDs []uuid.UUID
	for _, w := range ws {
		hostIDs = append(hostIDs, w.HostIDs...)
		orgIDs = append(orgIDs, w.OrgIDs...)
	}
	if len(hostIDs) > 0 {
		hosts, err := d.hosts.ListByIDs(ctx, dedupe(hostIDs))
		if err != nil {
			return n, err
		}
		for _, h := range hosts {
			n.hostOrg[h.ID], n.hostTitle[h.ID] = h.OrganizationID, h.Title
		}
	}
	if len(orgIDs) > 0 {
		orgs, err := d.organizations.ListByIDs(ctx, dedupe(orgIDs))
		if err != nil {
			return n, err
		}
		for _, o := range orgs {
			n.orgName[o.ID] = o.Name
		}
	}
	return n, nil
}

// view, pencereyi now anına göre durumu ve görülebilen kapsamıyla yanıta çevirir; upcoming true ise sonraki tekrarlar
// da eklenir.
func (d *Deps) maintenanceView(ctx context.Context, a *maintenanceAccess, canManage bool, w model.MaintenanceWindow, n names,
	loc *time.Location, now time.Time, upcoming bool) (maintenanceView, error) {
	v := maintenanceView{
		ID: w.ID, Title: w.Title, Recurrence: w.Recurrence, DurationMinutes: w.DurationMinutes, RepeatEvery: w.RepeatEvery,
		MonthDay: w.MonthDay, MonthWeek: w.MonthWeek, MonthWeekday: w.MonthWeekday,
		ValidFrom: datePtr(w.ValidFrom), ValidUntil: datePtr(w.ValidUntil),
		EndedAt: w.EndedAt, EndedLocal: localPtr(w.EndedAt, loc, localDateTime),
		StartsAt: w.StartsAt, EndsAt: w.EndsAt, StartsLocal: localPtr(w.StartsAt, loc, localDateTime), EndsLocal: localPtr(w.EndsAt, loc, localDateTime),
		Hosts: []scopeItem{}, Organizations: []scopeItem{}, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
	if w.StartMinute != nil {
		s := time.Date(2000, 1, 1, *w.StartMinute/60, *w.StartMinute%60, 0, 0, time.UTC).Format(localClock)
		v.StartTime = &s
	}
	if w.Weekdays != nil {
		for day := 1; day <= 7; day++ {
			if *w.Weekdays&(1<<(day-1)) != 0 {
				v.Weekdays = append(v.Weekdays, day)
			}
		}
	}
	for _, id := range w.HostIDs {
		if title, ok := n.hostTitle[id]; ok && (a.all || a.hosts[id]) {
			v.Hosts = append(v.Hosts, scopeItem{ID: id, Name: title})
		} else {
			v.HiddenScope++
		}
	}
	for _, id := range w.OrgIDs {
		if name, ok := n.orgName[id]; ok && (a.all || a.orgs[id]) {
			v.Organizations = append(v.Organizations, scopeItem{ID: id, Name: name})
		} else {
			v.HiddenScope++
		}
	}
	if canManage {
		ok, err := a.manageable(ctx, w.HostIDs, w.OrgIDs, n.hostOrg)
		if err != nil {
			return v, err
		}
		v.CanManage = ok // bitirilmiş pencere yine silinebilir; düzenleme kuralını ended_at belirler
	}

	cur, running := maintenance.Current(w, loc, now)
	next := maintenance.Next(w, loc, now, 1)
	switch {
	case running:
		v.Status = "active"
		o := occurrence(cur, loc)
		v.Current = &o
	case len(next) > 0:
		v.Status = "scheduled"
	default:
		v.Status = "past"
		if past := maintenance.Occurrences(w, loc, now.AddDate(-1, 0, 0), now); len(past) > 0 {
			o := occurrence(past[len(past)-1], loc)
			v.Last = &o
		}
	}
	for _, ov := range w.Overrides {
		if ov.EndedAt == nil && ov.OccurrenceStart.After(now) {
			v.SkippedLocal = append(v.SkippedLocal, ov.OccurrenceStart.In(loc).Format(localDateTime))
		}
	}
	if len(next) > 0 {
		o := occurrence(next[0], loc)
		v.Next = &o
	}
	if upcoming {
		v.Upcoming = []occurrenceView{}
		for _, o := range maintenance.Next(w, loc, now, upcomingCount) {
			v.Upcoming = append(v.Upcoming, occurrence(o, loc))
		}
	}
	return v, nil
}

func (d *Deps) canManageMaintenance(r *http.Request) (bool, error) {
	role, _ := roleFromContext(r.Context())
	return d.perms.HasPermission(r.Context(), role, "maintenance.manage")
}

// maintenanceUntil, verilen sunuculardan şu an bakımda olanların kesintisiz bakımının bitişidir (açık pencereler bir
// kez okunur). Sunucu, doğrudan ya da doğrudan bağlı olduğu organizasyon üzerinden kapsanır.
func (d *Deps) maintenanceUntil(ctx context.Context, hosts []model.Host) (map[uuid.UUID]time.Time, error) {
	now := time.Now()
	ws, err := d.maintenance.Open(ctx, now)
	if err != nil || len(ws) == 0 {
		return nil, err
	}
	loc := d.location()
	out := map[uuid.UUID]time.Time{}
	for _, h := range hosts {
		var mine []model.MaintenanceWindow
		for _, w := range ws {
			if slices.Contains(w.HostIDs, h.ID) || slices.Contains(w.OrgIDs, h.OrganizationID) {
				mine = append(mine, w)
			}
		}
		if until, ok := maintenance.ActiveAt(mine, loc, now); ok {
			out[h.ID] = until
		}
	}
	return out, nil
}

type maintenanceListResponse struct {
	Timezone string            `json:"timezone"`
	Windows  []maintenanceView `json:"windows"`
}

func (d *Deps) handleListMaintenance(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bakım pencereleri alınamadı")
	ctx := r.Context()
	a, err := d.maintenanceAccess(ctx, r)
	if err != nil {
		return fail("list maintenance: access", err)
	}
	canManage, err := d.canManageMaintenance(r)
	if err != nil {
		return fail("list maintenance: permission", err)
	}
	ws, err := d.maintenance.List(ctx, a.visibility())
	if err != nil {
		return fail("list maintenance", err)
	}
	n, err := d.maintenanceNames(ctx, ws...)
	if err != nil {
		return fail("list maintenance: names", err)
	}
	loc, now := d.location(), time.Now()
	out := maintenanceListResponse{Timezone: loc.String(), Windows: []maintenanceView{}}
	for _, mw := range ws {
		v, err := d.maintenanceView(ctx, a, canManage, mw, n, loc, now, false)
		if err != nil {
			return fail("list maintenance: view", err)
		}
		out.Windows = append(out.Windows, v)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// loadMaintenance, yoldaki pencereyi okur; görülemiyorsa 404, manage true ise ve yönetilemiyorsa 403 döner.
func (d *Deps) loadMaintenance(r *http.Request, manage bool, op string, fail failFunc) (model.MaintenanceWindow, *maintenanceAccess, names, error) {
	ctx := r.Context()
	id, err := pathID(r, "geçersiz bakım penceresi kimliği")
	if err != nil {
		return model.MaintenanceWindow{}, nil, names{}, err
	}
	mw, err := d.maintenance.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return mw, nil, names{}, notFound("bakım penceresi bulunamadı")
	}
	if err != nil {
		return mw, nil, names{}, fail(op, err)
	}
	a, err := d.maintenanceAccess(ctx, r)
	if err != nil {
		return mw, nil, names{}, fail(op+": access", err)
	}
	if !a.visible(mw) {
		return mw, nil, names{}, notFound("bakım penceresi bulunamadı")
	}
	n, err := d.maintenanceNames(ctx, mw)
	if err != nil {
		return mw, nil, names{}, fail(op+": names", err)
	}
	if manage {
		ok, err := a.manageable(ctx, mw.HostIDs, mw.OrgIDs, n.hostOrg)
		if err != nil {
			return mw, nil, names{}, fail(op+": check access", err)
		}
		if !ok {
			return mw, nil, names{}, forbidden()
		}
	}
	return mw, a, n, nil
}

func (d *Deps) respondMaintenance(w http.ResponseWriter, r *http.Request, status int, mw model.MaintenanceWindow, a *maintenanceAccess, fail failFunc, op string) error {
	ctx := r.Context()
	canManage, err := d.canManageMaintenance(r)
	if err != nil {
		return fail(op+": permission", err)
	}
	n, err := d.maintenanceNames(ctx, mw)
	if err != nil {
		return fail(op+": names", err)
	}
	v, err := d.maintenanceView(ctx, a, canManage, mw, n, d.location(), time.Now(), true)
	if err != nil {
		return fail(op+": view", err)
	}
	writeJSON(w, status, v)
	return nil
}

func (d *Deps) handleGetMaintenance(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bakım penceresi alınamadı")
	mw, a, _, err := d.loadMaintenance(r, false, "get maintenance", fail)
	if err != nil {
		return err
	}
	return d.respondMaintenance(w, r, http.StatusOK, mw, a, fail, "get maintenance")
}

// checkScope, isteğin kapsamındaki sunucuların ve organizasyonların var olduğunu ve hepsinin çağıranın yönettiği dalda
// olduğunu denetler.
func (d *Deps) checkScope(ctx context.Context, a *maintenanceAccess, mw model.MaintenanceWindow, fail failFunc, op string) error {
	n, err := d.maintenanceNames(ctx, mw)
	if err != nil {
		return fail(op+": names", err)
	}
	for _, id := range mw.HostIDs {
		if _, ok := n.hostOrg[id]; !ok {
			return fieldError400("scope", "seçilen sunuculardan biri bulunamadı")
		}
	}
	for _, id := range mw.OrgIDs {
		if _, ok := n.orgName[id]; !ok {
			return fieldError400("scope", "seçilen organizasyonlardan biri bulunamadı")
		}
	}
	ok, err := a.manageable(ctx, mw.HostIDs, mw.OrgIDs, n.hostOrg)
	if err != nil {
		return fail(op+": check access", err)
	}
	if !ok {
		return forbidden()
	}
	return nil
}

func maintenanceAuditDetails(mw model.MaintenanceWindow) map[string]any {
	return map[string]any{"title": mw.Title, "recurrence": mw.Recurrence, "host_ids": mw.HostIDs, "organization_ids": mw.OrgIDs}
}

func (d *Deps) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bakım penceresi oluşturulamadı")
	ctx := r.Context()
	req, err := bind[maintenanceRequest](r)
	if err != nil {
		return err
	}
	mw, err := req.window(d.location())
	if err != nil {
		return err
	}
	a, err := d.maintenanceAccess(ctx, r)
	if err != nil {
		return fail("create maintenance: access", err)
	}
	if err := d.checkScope(ctx, a, mw, fail, "create maintenance"); err != nil {
		return err
	}
	if uid, ok := userIDFromContext(ctx); ok {
		mw.CreatedBy = &uid
	}
	created, err := d.maintenance.Create(ctx, mw)
	if err != nil {
		return storeError(err, "kayıt bulunamadı", fail, "create maintenance")
	}
	target := created.ID.String()
	d.logAudit(r, "maintenance.create", "maintenance_window", &target, maintenanceAuditDetails(created))
	return d.respondMaintenance(w, r, http.StatusCreated, created, a, fail, "create maintenance")
}

func (d *Deps) handleUpdateMaintenance(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bakım penceresi güncellenemedi")
	ctx := r.Context()
	existing, a, _, err := d.loadMaintenance(r, true, "update maintenance", fail)
	if err != nil {
		return err
	}
	if existing.EndedAt != nil {
		return conflict("bu bakım penceresi bitirilmiş; değiştirilemez")
	}
	req, err := bind[maintenanceRequest](r)
	if err != nil {
		return err
	}
	mw, err := req.window(d.location())
	if err != nil {
		return err
	}
	mw.ID = existing.ID
	if err := d.checkScope(ctx, a, mw, fail, "update maintenance"); err != nil {
		return err
	}
	updated, err := d.maintenance.Update(ctx, mw)
	if err != nil {
		return storeError(err, "bakım penceresi bulunamadı", fail, "update maintenance")
	}
	target := updated.ID.String()
	d.logAudit(r, "maintenance.update", "maintenance_window", &target, maintenanceAuditDetails(updated))
	return d.respondMaintenance(w, r, http.StatusOK, updated, a, fail, "update maintenance")
}

func (d *Deps) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bakım penceresi silinemedi")
	mw, _, _, err := d.loadMaintenance(r, true, "delete maintenance", fail)
	if err != nil {
		return err
	}
	if err := d.maintenance.Delete(r.Context(), mw.ID); err != nil {
		return storeError(err, "bakım penceresi bulunamadı", fail, "delete maintenance")
	}
	target := mw.ID.String()
	d.logAudit(r, "maintenance.delete", "maintenance_window", &target, maintenanceAuditDetails(mw))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleEndMaintenance, pencereyi şimdi bitirir: seri kapanır, süren tekrar da biter.
func (d *Deps) handleEndMaintenance(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bakım penceresi bitirilemedi")
	mw, a, _, err := d.loadMaintenance(r, true, "end maintenance", fail)
	if err != nil {
		return err
	}
	ended, err := d.maintenance.End(r.Context(), mw.ID, time.Now())
	if err != nil {
		return storeError(err, "bakım penceresi bulunamadı", fail, "end maintenance")
	}
	target := mw.ID.String()
	d.logAudit(r, "maintenance.end", "maintenance_window", &target, map[string]any{"title": mw.Title})
	return d.respondMaintenance(w, r, http.StatusOK, ended, a, fail, "end maintenance")
}

// handleEndOccurrence ("bu tekrarı bitir") süren tekrarı şimdi bitirir; seri devam eder.
func (d *Deps) handleEndOccurrence(w http.ResponseWriter, r *http.Request) error {
	return d.overrideOccurrence(w, r, "maintenance.end_occurrence", "tekrar bitirilemedi", func(mw model.MaintenanceWindow, now time.Time) (time.Time, *time.Time, error) {
		cur, ok := maintenance.Current(mw, d.location(), now)
		if !ok {
			return time.Time{}, nil, conflict("şu an süren bir tekrar yok")
		}
		return cur.Start, &now, nil
	})
}

// handleSkipNext ("sıradaki tekrarı atla") henüz başlamamış ilk tekrarı atlar.
func (d *Deps) handleSkipNext(w http.ResponseWriter, r *http.Request) error {
	return d.overrideOccurrence(w, r, "maintenance.skip", "tekrar atlanamadı", func(mw model.MaintenanceWindow, now time.Time) (time.Time, *time.Time, error) {
		next := maintenance.Next(mw, d.location(), now, 1)
		if len(next) == 0 {
			return time.Time{}, nil, conflict("yaklaşan bir tekrar yok")
		}
		return next[0].Start, nil, nil
	})
}

func (d *Deps) overrideOccurrence(w http.ResponseWriter, r *http.Request, action, message string,
	pick func(mw model.MaintenanceWindow, now time.Time) (time.Time, *time.Time, error)) error {
	fail := failWith(message)
	ctx := r.Context()
	mw, a, _, err := d.loadMaintenance(r, true, action, fail)
	if err != nil {
		return err
	}
	if mw.EndedAt != nil {
		return conflict("bu bakım penceresi bitirilmiş; değiştirilemez")
	}
	start, endedAt, err := pick(mw, time.Now())
	if err != nil {
		return err
	}
	var by *uuid.UUID
	if uid, ok := userIDFromContext(ctx); ok {
		by = &uid
	}
	if err := d.maintenance.SetOverride(ctx, mw.ID, start, endedAt, by); err != nil {
		return storeError(err, "bakım penceresi bulunamadı", fail, action)
	}
	target := mw.ID.String()
	d.logAudit(r, action, "maintenance_window", &target, map[string]any{"title": mw.Title, "occurrence_start": start.UTC()})
	updated, err := d.maintenance.Get(ctx, mw.ID)
	if err != nil {
		return fail(action+": reload", err)
	}
	return d.respondMaintenance(w, r, http.StatusOK, updated, a, fail, action)
}

type maintenancePreviewResponse struct {
	Timezone string           `json:"timezone"`
	Upcoming []occurrenceView `json:"upcoming"`
}

// handlePreviewMaintenance, kaydetmeden doğrular ve sonraki tekrarları döndürür (formdaki önizleme). Kapsam henüz
// seçilmemiş olabilir; denetlenmez.
func (d *Deps) handlePreviewMaintenance(w http.ResponseWriter, r *http.Request) error {
	req, err := bind[maintenanceRequest](r)
	if err != nil {
		return err
	}
	if len(req.HostIDs) == 0 && len(req.OrgIDs) == 0 {
		req.HostIDs = []uuid.UUID{uuid.Nil}
	}
	loc := d.location()
	mw, err := req.window(loc)
	if err != nil {
		return err
	}
	out := maintenancePreviewResponse{Timezone: loc.String(), Upcoming: []occurrenceView{}}
	for _, o := range maintenance.Next(mw, loc, time.Now(), upcomingCount) {
		out.Upcoming = append(out.Upcoming, occurrence(o, loc))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
