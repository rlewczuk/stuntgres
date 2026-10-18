package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// catalogStore owns the engine's catalog state: the provenance domain, the
// published catalog snapshot, the provider registry and the identity
// allocators. It has no mutex. Engine.mu guards every field, and mutating
// methods carry the Locked suffix.
//
// Engine holds the store by value and never replaces it whole. The domain is
// allocated once per engine and kept across Reset and Restore; it is the
// engine's single provenance domain, so session tokens, transaction
// references and the lock manager use it as well as catalog references.
type catalogStore struct {
	catalogOwner
	state          *catalogState
	nextOID        ids.OID
	nextRevision   ids.DefinitionRevision
	nextEnumMember int64
}

// catalogOwner is the identity every CatalogRead of one engine carries: the
// provenance domain, the identities that stored principal and ACL values must
// belong to, and the provider registries. The store embeds it, and Engine
// never copies or replaces the store, so its address identifies the engine's
// catalog without reaching the engine. It holds no engine services: code that needs roles,
// paths or engine state takes them from its AuthorizationView or caller.
type catalogOwner struct {
	Domain       *ids.Domain
	Principals   *authorization.Domain
	ACLAuthority *sqltype.ACLAuthority
	Providers    map[ids.OID]CatalogProvider
	// Extensions is the registry of extension implementations. Like
	// providers, it closes when construction ends.
	Extensions map[string]extensionImplementation
}

// owner returns the store's owner record.
func (s *catalogStore) owner() *catalogOwner { return &s.catalogOwner }

// read returns a view of state owned by this store.
func (s *catalogStore) read(state *catalogState) CatalogRead {
	return CatalogRead{owner: s.owner(), state: state}
}

// registerProviderLocked adds a provider before the builtin catalog is
// installed. Registration closes once state exists.
func (s *catalogStore) registerProviderLocked(id ids.OID, p CatalogProvider) error {
	if s.state != nil {
		return fmt.Errorf("stuntgres: catalog providers are already initialized")
	}
	if s.Providers == nil {
		return fmt.Errorf("stuntgres: engine is not initialized")
	}
	if _, ok := s.Providers[id]; ok {
		return fmt.Errorf("stuntgres: duplicate catalog class")
	}
	s.Providers[id] = p
	return nil
}

// provider returns the registered provider of a catalog class, or nil. The
// registry is immutable after construction.
func (s *catalogOwner) provider(class ids.OID) CatalogProvider {
	return s.Providers[class]
}

// allocateOIDLocked returns the next unused object identifier, skipping
// reserved OIDs and those that current descriptors already use.
func (s *catalogStore) allocateOIDLocked() (ids.OID, error) {
	for s.nextOID != 0 {
		n := s.nextOID
		if n == ^ids.OID(0) {
			s.nextOID = 0
		} else {
			s.nextOID++
		}
		if n == ids.NamespaceClassID || n == ids.RelationClassID || n == ids.PGCatalogNamespaceOID || n == ids.PublicNamespaceOID {
			continue
		}
		if _, ok := s.state.Find(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: n}); ok {
			continue
		}
		if _, ok := s.state.Find(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: n}); ok {
			continue
		}
		if _, ok := s.state.Find(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n}); ok {
			continue
		}
		usedRule := false
		for _, descriptor := range s.state.Descriptors() {
			if definitionOIDUsed(descriptor, n) {
				usedRule = true
				break
			}
		}
		if usedRule {
			continue
		}
		return n, nil
	}
	return 0, fmt.Errorf("stuntgres: catalog object identity exhausted")
}

// revisionLocked reserves the next definition revision.
func (s *catalogStore) revisionLocked() (ids.DefinitionRevision, error) {
	if s.nextRevision == ids.DefinitionRevision(^uint64(0)) {
		return 0, fmt.Errorf("stuntgres: catalog definition revision exhausted")
	}
	r := s.nextRevision
	s.nextRevision++
	return r, nil
}

// allocateEnumMemberLocked reserves the next enum member identity. Aborted
// reservations are never recycled.
func (s *catalogStore) allocateEnumMemberLocked() (int64, error) {
	if s.nextEnumMember > math.MaxUint32-2 {
		return 0, fmt.Errorf("stuntgres: enum member identity exhausted")
	}
	if s.nextEnumMember == 0 {
		s.nextEnumMember = 1
	} else {
		s.nextEnumMember += 2
	}
	return s.nextEnumMember, nil
}

// validateObjectRefLocked checks that view and ref belong to this store and
// its current epoch, and that ref names a live object. It preserves live epoch
// and provenance checks without reacquiring synchronization when called from
// catalog mutation preflight.
func (s *catalogStore) validateObjectRefLocked(view CatalogRead, ref ids.ObjectRef) error {
	if s.Domain == nil || view.owner != s.owner() || view.state == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if ref.Domain() == nil || ref.Domain() != s.Domain {
		return fmt.Errorf("stuntgres: foreign catalog reference")
	}
	if view.state.epoch != s.state.epoch || ref.Epoch != s.state.epoch {
		return ids.ErrCatalogReplaced
	}
	d, ok := view.Find(ids.ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID})
	if !ok {
		return fmt.Errorf("stuntgres: catalog object is missing")
	}
	if ref.Address.SubID < 0 {
		return fmt.Errorf("stuntgres: invalid catalog subobject")
	}
	if ref.Address.SubID > 0 {
		r, ok := d.(*relationDescriptor)
		if !ok || !r.hasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid catalog subobject")
		}
	}
	return nil
}

// validateCatalogViewLocked checks that view belongs to this store and its
// current epoch.
func (s *catalogStore) validateCatalogViewLocked(view CatalogRead) error {
	if s.Domain == nil || view.owner != s.owner() || view.state == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if view.Epoch() != s.state.epoch {
		return ids.ErrCatalogReplaced
	}
	return nil
}
