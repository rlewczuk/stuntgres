package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	catalog.Owner
	state          *catalog.State
	nextOID        ids.OID
	nextRevision   ids.DefinitionRevision
	nextEnumMember int64
}

// owner returns the store's owner record.
func (s *catalogStore) owner() *catalog.Owner { return &s.Owner }

// read returns a view of state owned by this store.
func (s *catalogStore) read(state *catalog.State) catalog.Read {
	return catalog.NewRead(s.owner(), state)
}

// registerProviderLocked adds a provider before the builtin catalog is
// installed. Registration closes once state exists.
func (s *catalogStore) registerProviderLocked(id ids.OID, p catalog.Provider) error {
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
		usedDefault := false
		for _, record := range s.state.DefaultACLs() {
			if record != nil && record.OID == n {
				usedDefault = true
				break
			}
		}
		if usedDefault {
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

// advanceRevisionFrontier calculates a speculative allocator frontier without
// reserving it. Publication commits the scalar under Engine.mu only at install.
// terminal describes the descriptor clocks, not saturation of the result.
func (s *catalogStore) advanceRevisionFrontier(candidate ids.DefinitionRevision, d catalog.Descriptor) (next ids.DefinitionRevision, terminal bool) {
	frontier := d.Revision()
	if relation, ok := d.(*catalog.Relation); ok && relation.SequencePlanStamp > frontier {
		frontier = relation.SequencePlanStamp
	}
	if frontier == ids.DefinitionRevision(^uint64(0)) {
		return frontier, true
	}
	if candidate > frontier {
		return candidate, false
	}
	return frontier + 1, false
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
func (s *catalogStore) validateObjectRefLocked(view catalog.Read, ref ids.ObjectRef) error {
	if s.Domain == nil || view.Owner() != s.owner() || view.State() == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if ref.Domain() == nil || ref.Domain() != s.Domain {
		return fmt.Errorf("stuntgres: foreign catalog reference")
	}
	if view.State().Epoch() != s.state.Epoch() || ref.Epoch != s.state.Epoch() {
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
		r, ok := d.(*catalog.Relation)
		if !ok || !r.HasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid catalog subobject")
		}
	}
	return nil
}

// validateCatalogViewLocked checks that view belongs to this store and its
// current epoch.
func (s *catalogStore) validateCatalogViewLocked(view catalog.Read) error {
	if s.Domain == nil || view.Owner() != s.owner() || view.State() == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if view.Epoch() != s.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}
