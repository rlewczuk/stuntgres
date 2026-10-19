package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// roleStore owns the engine's role state: the principal domain, the ACL
// authority, the committed role publication, the principal allocator and the
// registry of role references owned by other internal features. It has no
// mutex. Engine.mu guards every field, and mutating methods carry the Locked
// suffix.
//
// Engine holds the store by value and never replaces it whole:
// catalog.Owner.Principals, catalog.Owner.ACLAuthority and
// Root.Reprovenance keep pointers to its domain and ACL authority.
// Transaction overlays of the role publication live in catalogChanges, not
// here; published is only the committed record.
type roleStore struct {
	domain        authorization.Domain
	aclAuthority  sqltype.ACLAuthority
	published     *roleCatalog
	nextPrincipal uint64
	references    map[string][]authorization.PrincipalRef
}

// initialize seeds the predefined roles and the principal allocator. It runs
// once, while the engine is not yet shared.
func (s *roleStore) initialize() {
	s.published = &roleCatalog{Root: authorization.Seed(&s.domain), Revision: 1}
	s.nextPrincipal = authorization.LastPredefinedRoleID + 1
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
func (s *roleStore) read(owner *catalog.Owner, view catalog.Read, root *roleCatalog) authorization.Read {
	return access.RoleRead(owner, &s.domain, view, root)
}

// setReferencesLocked replaces one feature's copied role references and
// advances the committed role revision. An empty payload unregisters the
// feature. The registry map is copied, never mutated in place; nothing
// changes on revision exhaustion or cancellation.
func (s *roleStore) setReferencesLocked(ctx context.Context, feature string, refs []authorization.PrincipalRef) error {
	if s.published.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	next := make(map[string][]authorization.PrincipalRef, len(s.references)+1)
	for key, payload := range s.references {
		next[key] = payload
	}
	if len(refs) == 0 {
		delete(next, feature)
	} else {
		next[feature] = append([]authorization.PrincipalRef(nil), refs...)
	}
	roles := s.published.Advance()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.references = next
	s.published = roles
	return nil
}

// referencingFeatures returns, in no particular order, the features whose
// registered references include ref.
func (s *roleStore) referencingFeatures(ref authorization.PrincipalRef) []string {
	var features []string
	for feature, refs := range s.references {
		if slices.Contains(refs, ref) {
			features = append(features, feature)
		}
	}
	return features
}
