package access

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

// fakeStore, organizasyon ağacını ve atamaları bellekte tutar ve her sorguyu sayar.
type fakeStore struct {
	full, context map[uuid.UUID][]uuid.UUID // kullanıcı → TAM erişimli / bağlam organizasyonları
	userHosts     map[uuid.UUID][]uuid.UUID // kullanıcı → atanmış sunucular
	orgHosts      map[uuid.UUID][]uuid.UUID // organizasyon → sunucular
	calls         int
	err           error
}

func (f *fakeStore) ListOrganizationIDs(_ context.Context, u uuid.UUID) ([]uuid.UUID, error) {
	f.calls++
	return f.full[u], f.err
}

func (f *fakeStore) ListContextIDs(_ context.Context, u uuid.UUID) ([]uuid.UUID, error) {
	f.calls++
	return f.context[u], f.err
}

func (f *fakeStore) ListHostIDs(_ context.Context, u uuid.UUID) ([]uuid.UUID, error) {
	f.calls++
	return f.userHosts[u], f.err
}

func (f *fakeStore) ListIDsByOrganizations(_ context.Context, orgs []uuid.UUID) ([]uuid.UUID, error) {
	f.calls++
	var ids []uuid.UUID
	for _, o := range orgs {
		ids = append(ids, f.orgHosts[o]...)
	}
	return ids, f.err
}

// Ağaç: parent → child (admin child'a atanmış), sibling ayrı dal. Sunucular: hChild child'da, hSibling sibling'de.
var (
	parent, child, sibling = uuid.New(), uuid.New(), uuid.New()
	hChild, hSibling       = uuid.New(), uuid.New()
	admin, operator        = uuid.New(), uuid.New()
)

func newFake() *fakeStore {
	return &fakeStore{
		full:      map[uuid.UUID][]uuid.UUID{admin: {child}},
		context:   map[uuid.UUID][]uuid.UUID{admin: {parent}},
		userHosts: map[uuid.UUID][]uuid.UUID{operator: {hSibling}},
		orgHosts:  map[uuid.UUID][]uuid.UUID{child: {hChild}, sibling: {hSibling}},
	}
}

func newScope(f *fakeStore, role string, user uuid.UUID) *Scope {
	return NewResolver(f, f, f).Scope(role, user)
}

func TestScopeMatrix(t *testing.T) {
	ctx := context.Background()
	hostIn := func(id, org uuid.UUID) model.Host { return model.Host{ID: id, OrganizationID: org} }

	cases := []struct {
		name                   string
		role                   string
		user                   uuid.UUID
		manageChild, manageSib bool
		accessParent           string
		viewChild, viewSibling bool
		globalThreshold        bool
		visible                []uuid.UUID // nil = kapsamsız
	}{
		{"super_admin", model.RoleSuperAdmin, uuid.New(), true, true, model.OrgAccessFull, true, true, true, nil},
		{"org_admin", model.RoleOrgAdmin, admin, true, false, model.OrgAccessContext, true, false, false, []uuid.UUID{hChild}},
		{"operator", model.RoleOperator, operator, false, false, "", false, true, false, []uuid.UUID{hSibling}},
		{"unknown role", "guest", uuid.New(), false, false, "", false, false, false, []uuid.UUID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newScope(newFake(), tc.role, tc.user)
			check := func(what string, got bool, err error, want bool) {
				t.Helper()
				if err != nil || got != want {
					t.Errorf("%s = %v, %v; want %v", what, got, err, want)
				}
			}
			ok, err := s.CanManageOrg(ctx, child)
			check("CanManageOrg(child)", ok, err, tc.manageChild)
			ok, err = s.CanManageOrg(ctx, sibling)
			check("CanManageOrg(sibling)", ok, err, tc.manageSib)
			if got, err := s.OrgAccess(ctx, parent); err != nil || got != tc.accessParent {
				t.Errorf("OrgAccess(parent) = %q, %v; want %q", got, err, tc.accessParent)
			}
			ok, err = s.CanViewHost(ctx, hostIn(hChild, child))
			check("CanViewHost(child host)", ok, err, tc.viewChild)
			ok, err = s.CanViewHost(ctx, hostIn(hSibling, sibling))
			check("CanViewHost(sibling host)", ok, err, tc.viewSibling)
			ok, err = s.CanManageThreshold(ctx, nil)
			check("CanManageThreshold(global)", ok, err, tc.globalThreshold)
			ok, err = s.CanManageThreshold(ctx, &child)
			check("CanManageThreshold(child)", ok, err, tc.manageChild)

			visible, err := s.VisibleHostIDs(ctx)
			if err != nil || (visible == nil) != (tc.visible == nil) || !slices.Equal(visible, tc.visible) {
				t.Errorf("VisibleHostIDs = %v, %v; want %v", visible, err, tc.visible)
			}
		})
	}
}

// Bir istek içindeki tekrar eden denetimler kümeleri yeniden sorgulamaz; super_admin hiç sorgu üretmez.
func TestScopeLoadsEachSetOnce(t *testing.T) {
	ctx := context.Background()

	f := newFake()
	s := newScope(f, model.RoleOrgAdmin, admin)
	for range 5 {
		_, _ = s.CanManageOrg(ctx, child)
		_, _ = s.CanViewHost(ctx, model.Host{OrganizationID: sibling})
		_, _ = s.OrgAccess(ctx, parent)
		_, _ = s.VisibleHostIDs(ctx)
	}
	if f.calls != 3 { // TAM erişim, bağlam, organizasyonların sunucuları
		t.Fatalf("org_admin queries = %d, want 3", f.calls)
	}

	f = newFake()
	s = newScope(f, model.RoleSuperAdmin, uuid.New())
	_, _ = s.CanManageOrg(ctx, child)
	_, _ = s.CanViewHost(ctx, model.Host{OrganizationID: sibling})
	_, _ = s.VisibleHostIDs(ctx)
	if f.calls != 0 {
		t.Fatalf("super_admin queries = %d, want 0", f.calls)
	}
}

// Dönen dilimler çağıranındır: değiştirmek kapsamı bozmaz (eşik listesi bağlam organizasyonlarını ekler).
func TestScopeReturnsCopies(t *testing.T) {
	ctx := context.Background()
	s := newScope(newFake(), model.RoleOrgAdmin, admin)
	ids, _ := s.ManagedOrgIDs(ctx)
	ids[0] = sibling
	_ = append(ids, sibling)
	if ok, _ := s.CanManageOrg(ctx, sibling); ok {
		t.Fatal("modifying ManagedOrgIDs' result changed the scope")
	}
	if again, _ := s.ManagedOrgIDs(ctx); !slices.Equal(again, []uuid.UUID{child}) {
		t.Fatalf("ManagedOrgIDs = %v after caller modification", again)
	}
}

// Sorgu hatası iletilir ve önbelleğe alınmaz: sonraki çağrı yeniden dener.
func TestScopeDoesNotCacheErrors(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	f.err = errors.New("db down")
	s := newScope(f, model.RoleOrgAdmin, admin)
	if _, err := s.CanManageOrg(ctx, child); err == nil {
		t.Fatal("want error")
	}
	f.err = nil
	if ok, err := s.CanManageOrg(ctx, child); err != nil || !ok {
		t.Fatalf("after recovery = %v, %v; want true", ok, err)
	}
}
