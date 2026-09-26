// Package access, bir panel kullanıcısının hangi organizasyonları ve sunucuları görüp yönetebileceğini tek yerde
// yanıtlar (bkz. docs/MIMARI.md bölüm 4). Rol izni (ne yapabilir: rbac) ile kapsam (nerede yapabilir: bu paket)
// ayrıdır; handler önce izni (requirePermission), sonra kapsamı denetler.
//
// Kurallar:
//   - super_admin kapsamsızdır: her organizasyonu ve sunucuyu görür ve yönetir.
//   - Organizasyon ataması aşağıya miras kalır: atandığı organizasyon ve altındaki bütün dallar TAM erişimlidir; üst
//     zincir yalnızca bağlam olarak (ad ve konum) görünür.
//   - org_admin tam erişimli organizasyonlarındaki sunucuları, operator yalnızca kendisine doğrudan atanmış sunucuları
//     görür; başka bir rol hiçbir sunucuyu görmez.
package access

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
)

// OrgAssignments, kullanıcının organizasyon atamalarını çözer (store.UserOrganizations).
type OrgAssignments interface {
	// ListOrganizationIDs, TAM erişimli organizasyonlardır: atananlar ve altlarındaki dallar.
	ListOrganizationIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	// ListContextIDs, yalnızca bağlam olarak görünen üst zincirdir.
	ListContextIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
}

// HostAssignments, kullanıcıya doğrudan atanmış sunucuları çözer (store.UserHosts).
type HostAssignments interface {
	ListHostIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
}

// HostIndex, organizasyonların sunucularını bulur (store.Hosts).
type HostIndex interface {
	ListIDsByOrganizations(ctx context.Context, orgIDs []uuid.UUID) ([]uuid.UUID, error)
}

// Resolver, istek başına Scope üretir.
type Resolver struct {
	orgs      OrgAssignments
	userHosts HostAssignments
	hosts     HostIndex
}

func NewResolver(orgs OrgAssignments, userHosts HostAssignments, hosts HostIndex) *Resolver {
	return &Resolver{orgs: orgs, userHosts: userHosts, hosts: hosts}
}

// Scope, kimliği doğrulanmış bir kullanıcının kapsamıdır. Kümeler ilk gerektiğinde bir kez okunur ve isteğin sonuna
// kadar tutulur; böylece bir istek içindeki tekrar eden denetimler (ör. bir listedeki her sunucu) yeni sorgu üretmez.
// Bir Scope tek bir isteğe aittir ve eşzamanlı kullanılmaz.
type Scope struct {
	res    *Resolver
	role   string
	userID uuid.UUID

	fullOrgs    []uuid.UUID            // TAM erişimli organizasyonlar
	fullOrgSet  map[uuid.UUID]struct{} // nil = henüz okunmadı
	contextOrgs []uuid.UUID            // nil = henüz okunmadı
	ownHosts    []uuid.UUID            // operator'a doğrudan atanmış sunucular
	ownHostSet  map[uuid.UUID]struct{} // nil = henüz okunmadı
	visible     []uuid.UUID            // nil = henüz okunmadı (bkz. VisibleHostIDs)
}

// Scope, role ve userID için yeni bir kapsam döndürür.
func (r *Resolver) Scope(role string, userID uuid.UUID) *Scope {
	return &Scope{res: r, role: role, userID: userID}
}

func (s *Scope) Role() string      { return s.role }
func (s *Scope) UserID() uuid.UUID { return s.userID }

// IsSuperAdmin, kapsamsız kullanıcıdır; ona özel "hepsini listele" yollarını seçmek içindir.
func (s *Scope) IsSuperAdmin() bool { return s.role == model.RoleSuperAdmin }

// ManagedOrgIDs, TAM erişimli organizasyonlardır (super_admin için anlamsızdır: o her şeyi yönetir). Dönen dilim
// çağıranındır.
func (s *Scope) ManagedOrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	if err := s.loadFullOrgs(ctx); err != nil {
		return nil, err
	}
	return slices.Clone(s.fullOrgs), nil
}

// ContextOrgIDs, yalnızca bağlam olarak görünen üst zincirdir. Dönen dilim çağıranındır.
func (s *Scope) ContextOrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	if s.contextOrgs == nil {
		ids, err := s.res.orgs.ListContextIDs(ctx, s.userID)
		if err != nil {
			return nil, err
		}
		s.contextOrgs = nonNil(ids)
	}
	return slices.Clone(s.contextOrgs), nil
}

// CanManageOrg, çağıranın orgID üzerinde TAM erişimi olup olmadığını söyler: super_admin her zaman; diğerleri orgID
// atandıkları bir organizasyon ya da onun altındaysa.
func (s *Scope) CanManageOrg(ctx context.Context, orgID uuid.UUID) (bool, error) {
	if s.IsSuperAdmin() {
		return true, nil
	}
	if err := s.loadFullOrgs(ctx); err != nil {
		return false, err
	}
	_, ok := s.fullOrgSet[orgID]
	return ok, nil
}

// OrgAccess, çağıranın orgID'deki erişimidir: model.OrgAccessFull, yalnızca üst zincir bilgisi olarak
// model.OrgAccessContext ya da "" (görünmez).
func (s *Scope) OrgAccess(ctx context.Context, orgID uuid.UUID) (string, error) {
	full, err := s.CanManageOrg(ctx, orgID)
	if err != nil {
		return "", err
	}
	if full {
		return model.OrgAccessFull, nil
	}
	contextIDs, err := s.ContextOrgIDs(ctx)
	if err != nil {
		return "", err
	}
	if slices.Contains(contextIDs, orgID) {
		return model.OrgAccessContext, nil
	}
	return "", nil
}

// CanViewHost, host.view'ın rol başına kapsamıdır: super_admin her sunucuyu, org_admin tam erişimli
// organizasyonlarındakileri, operator yalnızca kendisine atananları görür.
func (s *Scope) CanViewHost(ctx context.Context, host model.Host) (bool, error) {
	switch s.role {
	case model.RoleSuperAdmin:
		return true, nil
	case model.RoleOrgAdmin:
		return s.CanManageOrg(ctx, host.OrganizationID)
	case model.RoleOperator:
		if err := s.loadOwnHosts(ctx); err != nil {
			return false, err
		}
		_, ok := s.ownHostSet[host.ID]
		return ok, nil
	default:
		return false, nil
	}
}

// CanManageThreshold, bir varsayılan eşik satırının kapsamıdır: organizasyon varsayılanı organizasyon erişimini izler,
// genel varsayılan (orgID nil) yalnızca super_admin içindir. Sunucuya özel eşikler sunucunun kapsamını izler.
func (s *Scope) CanManageThreshold(ctx context.Context, orgID *uuid.UUID) (bool, error) {
	if orgID == nil {
		return s.IsSuperAdmin(), nil
	}
	return s.CanManageOrg(ctx, *orgID)
}

// VisibleHostIDs, çağıranın görebildiği sunuculardır. nil "kapsamsız" demektir (super_admin); diğer her rol somut
// (belki boş) bir dilim alır. Dönen dilim çağıranındır.
func (s *Scope) VisibleHostIDs(ctx context.Context) ([]uuid.UUID, error) {
	if s.IsSuperAdmin() {
		return nil, nil
	}
	if s.visible == nil {
		var ids []uuid.UUID
		switch s.role {
		case model.RoleOrgAdmin:
			if err := s.loadFullOrgs(ctx); err != nil {
				return nil, err
			}
			var err error
			if ids, err = s.res.hosts.ListIDsByOrganizations(ctx, s.fullOrgs); err != nil {
				return nil, err
			}
		case model.RoleOperator:
			if err := s.loadOwnHosts(ctx); err != nil {
				return nil, err
			}
			ids = s.ownHosts
		}
		s.visible = nonNil(ids)
	}
	return slices.Clone(s.visible), nil
}

func (s *Scope) loadFullOrgs(ctx context.Context) error {
	if s.fullOrgSet != nil {
		return nil
	}
	ids, err := s.res.orgs.ListOrganizationIDs(ctx, s.userID)
	if err != nil {
		return err
	}
	s.fullOrgs, s.fullOrgSet = nonNil(ids), toSet(ids)
	return nil
}

func (s *Scope) loadOwnHosts(ctx context.Context) error {
	if s.ownHostSet != nil {
		return nil
	}
	ids, err := s.res.userHosts.ListHostIDs(ctx, s.userID)
	if err != nil {
		return err
	}
	s.ownHosts, s.ownHostSet = nonNil(ids), toSet(ids)
	return nil
}

func toSet(ids []uuid.UUID) map[uuid.UUID]struct{} {
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
