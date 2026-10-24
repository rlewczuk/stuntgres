package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// roleStore owns the engine's role state: the principal domain, the ACL
// authority, the committed role publication and the principal allocator. It
// has no mutex. Engine.mu guards every field, and mutating methods carry the Locked
// suffix.
//
// Cluster runtimes share one pointer-stable store:
// catalog.Owner.Principals, catalog.Owner.ACLAuthority and
// Root.Reprovenance keep pointers to its domain and ACL authority.
// Transaction overlays of the role publication live in session.Journal, not
// here; published is only the committed record.
type roleStore struct {
	domain        authorization.Domain
	aclAuthority  sqltype.ACLAuthority
	published     *access.RoleRecord
	nextPrincipal uint64
	nextRevision  uint64
}

// initialize seeds the predefined roles and the principal allocator. It runs
// once, while the engine is not yet shared.
func (s *roleStore) initialize() {
	s.published = &access.RoleRecord{Root: authorization.Seed(&s.domain), Revision: 1}
	s.nextPrincipal = authorization.LastPredefinedRoleID + 1
	s.nextRevision = 2
}

// principalCandidateLocked returns a private candidate; only successful
// publication advances allocation.
func (s *roleStore) principalCandidateLocked() (authorization.PrincipalRef, error) {
	id := s.nextPrincipal
	if id == 0 || id == ^uint64(0) {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity or authorization revision exhausted")
	}
	if id <= authorization.LastPredefinedRoleID {
		id = authorization.LastPredefinedRoleID + 1
	}
	ref := s.domain.Principal(id)
	if _, exists := s.published.Root.Read(authorization.PrincipalRef{}).Find(ref); exists {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity already allocated")
	}
	return ref, nil
}

// read returns the role view of root as seen by view's current database
// owner. It is empty unless view belongs to owner, the catalog owner this
// store backs, and root belongs to this store's domain.
func (s *roleStore) read(owner *catalog.Owner, view catalog.Read, root *access.RoleRecord) authorization.Read {
	return access.RoleRead(owner, &s.domain, view, root)
}

func (s *roleStore) revisionCandidateLocked() (uint64, error) {
	if s.published.Revision == ^uint64(0) || s.nextRevision == ^uint64(0) {
		return 0, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	return max(s.nextRevision, s.published.Revision+1), nil
}
