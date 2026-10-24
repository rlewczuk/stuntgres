package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
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
//
// State is the published snapshot. It changes only through PublishLocked,
// and the OID and revision frontiers only through their Install…Locked
// writers, so every publication point stays greppable.
type catalogStore struct {
	catalog.Owner
	State          *catalog.State
	nextOID        ids.OID
	nextRevision   ids.DefinitionRevision
	nextEnumMember int64
}

// The store is the ddl.Allocator of identity-only callers such as partition
// trigger synchronization. A pointer fits an interface without allocating, so
// those callers add no allocation per publication. Engine.mu is held.
var _ ddl.Allocator = (*catalogStore)(nil)

func (s *catalogStore) AllocateOID() (ids.OID, error)             { return s.AllocateOIDLocked() }
func (s *catalogStore) Revision() (ids.DefinitionRevision, error) { return s.RevisionLocked() }

// Read returns a view of state owned by this store.
func (s *catalogStore) Read(state *catalog.State) catalog.Read {
	return catalog.NewRead(&s.Owner, state)
}

// Current returns a view of the published state.
func (s *catalogStore) Current() catalog.Read { return s.Read(s.State) }

// PublishLocked installs next as the published state. Every publication goes
// through it, so publication points stay greppable.
func (s *catalogStore) PublishLocked(next *catalog.State) { s.State = next }

// NextOID returns the OID allocation frontier. Zero means it is exhausted.
func (s *catalogStore) NextOID() ids.OID { return s.nextOID }

// InstallOIDFrontierLocked sets the OID frontier, for publication of a
// replacement that reserved OIDs privately.
func (s *catalogStore) InstallOIDFrontierLocked(next ids.OID) { s.nextOID = next }

// NextRevision returns the definition revision frontier.
func (s *catalogStore) NextRevision() ids.DefinitionRevision { return s.nextRevision }

// InstallRevisionFrontierLocked sets the definition revision frontier, for
// publication of a state whose descriptors carry revisions at or past it.
func (s *catalogStore) InstallRevisionFrontierLocked(next ids.DefinitionRevision) {
	s.nextRevision = next
}

// RegisterProviderLocked adds a provider before the builtin catalog is
// installed. Registration closes once state exists.
func (s *catalogStore) RegisterProviderLocked(id ids.OID, p catalog.Provider) error {
	if s.State != nil {
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

// AllocateOIDLocked returns the next unused object identifier, skipping
// reserved OIDs and those that current descriptors already use.
func (s *catalogStore) AllocateOIDLocked() (ids.OID, error) {
	return s.AllocateOIDFromLocked(&s.nextOID, 0)
}

// catalogOIDReservation belongs to one unpublished replacement. Its frontier
// becomes live only at the catalog installation boundary; Base fences ordinary
// allocations made while candidate providers run outside the target lock.
type catalogOIDReservation struct{ Base, Next ids.OID }

func (s *catalogStore) OIDReservationLocked() catalogOIDReservation {
	return catalogOIDReservation{Base: s.nextOID, Next: s.nextOID}
}

// AllocateOIDFromLocked is the single allocator, for both normal issuance and
// replacement reservations. Only the supplied cursor changes; all reservation,
// descriptor, definition and exhaustion checks below are identical.
func (s *catalogStore) AllocateOIDFromLocked(cursor *ids.OID, floor ids.OID) (ids.OID, error) {
	if *cursor != 0 && *cursor < floor {
		*cursor = floor
	}
	for *cursor != 0 {
		n := *cursor
		if n == ^ids.OID(0) {
			*cursor = 0
		} else {
			(*cursor)++
		}
		if n == ids.NamespaceClassID || n == ids.RelationClassID || n == ids.PGCatalogNamespaceOID || n == ids.PublicNamespaceOID {
			continue
		}
		if _, ok := s.State.Find(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: n}); ok {
			continue
		}
		if _, ok := s.State.Find(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: n}); ok {
			continue
		}
		if _, ok := s.State.Find(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n}); ok {
			continue
		}
		if _, ok := s.State.Find(ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: n}); ok {
			continue
		}
		usedRule := false
		for _, descriptor := range s.State.Descriptors() {
			if catalog.DefinitionOIDUsed(descriptor, n) {
				usedRule = true
				break
			}
		}
		if usedRule {
			continue
		}
		usedDefault := false
		for _, record := range s.State.DefaultACLs() {
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
	return 0, catalog.ErrObjectIdentityExhausted
}

// AllocateOIDAtLeastLocked skips an unused numeric interval without replacing
// the ordinary allocator's reservation/definition checks or recycling identities.
// Only affected alias endpoint planning supplies a nonzero floor.
func (s *catalogStore) AllocateOIDAtLeastLocked(floor ids.OID) (ids.OID, error) {
	return s.AllocateOIDFromLocked(&s.nextOID, floor)
}

// RevisionLocked reserves the next definition revision.
func (s *catalogStore) RevisionLocked() (ids.DefinitionRevision, error) {
	if s.nextRevision == ids.DefinitionRevision(^uint64(0)) {
		return 0, fmt.Errorf("stuntgres: catalog definition revision exhausted")
	}
	r := s.nextRevision
	s.nextRevision++
	return r, nil
}

// AllocateEnumMemberLocked reserves the next enum member identity. Aborted
// reservations are never recycled.
func (s *catalogStore) AllocateEnumMemberLocked() (int64, error) {
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

// ValidateObjectRefLocked checks that view and ref belong to this store and
// its current epoch, and that ref names a live object. It preserves live epoch
// and provenance checks without reacquiring synchronization when called from
// catalog mutation preflight.
func (s *catalogStore) ValidateObjectRefLocked(view catalog.Read, ref ids.ObjectRef) error {
	if s.Domain == nil || view.Owner() != &s.Owner || view.State() == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if ref.Domain() == nil || ref.Domain() != s.Domain {
		return fmt.Errorf("stuntgres: foreign catalog reference")
	}
	if view.State().Epoch() != s.State.Epoch() || ref.Epoch != s.State.Epoch() {
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

// ValidateCatalogViewLocked checks that view belongs to this store and its
// current epoch.
func (s *catalogStore) ValidateCatalogViewLocked(view catalog.Read) error {
	if s.Domain == nil || view.Owner() != &s.Owner || view.State() == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if view.Epoch() != s.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}
