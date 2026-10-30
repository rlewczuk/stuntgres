package catalog

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// Store owns one engine's catalog state: the provenance domain, the
// published catalog snapshot, the provider registry and the identity
// allocators. It has no mutex. The engine's mutex guards every field, and
// mutating methods carry the Locked suffix.
//
// The engine holds the store by value and never replaces it whole: every Read
// it returns points at its Owner, so a store is never copied after NewStore.
// The domain is allocated once per engine and kept across Reset and Restore;
// it is the engine's single provenance domain, so session tokens, transaction
// references and the lock manager use it as well as catalog references.
//
// State is the published snapshot. It changes only through PublishLocked,
// and the OID and revision frontiers only through their Install…Locked
// writers, so every publication point stays greppable.
type Store struct {
	Owner
	State          *State
	nextOID        ids.OID
	nextRevision   ids.DefinitionRevision
	nextEnumMember int64
	allocatedOIDs  allocatedOIDSet
	// seeding is set from BeginSeedLocked to FinishSeedLocked.
	seeding bool
}

// allocatedOIDSet holds globally allocated descriptor and owned-definition
// OIDs of one descriptor map. Every descriptor write replaces a State's map
// value, so an equal map value has the same descriptors. The set is immutable
// after construction.
type allocatedOIDSet struct {
	valid       bool
	descriptors util.PersistentMap[ids.ObjectAddress, Descriptor]
	used        map[ids.OID]bool
}

// allocatedOIDUsedLocked reports whether the published state owns id in the
// global allocation space. Large-object descriptor identities are class-local,
// as in ValidateDefinitionOIDs. An id above the state's OID ceiling is unused;
// below it, it builds one set per descriptor map instead of scanning every
// descriptor per candidate. A bulk load writes in place behind
// an unchanged map, so it scans using the same identity rules, except for the
// builtin seed: compiled builtin OIDs are below the first allocated OID, and
// seed descriptors and definitions take their OIDs from the same cursor, so
// none owns a candidate.
func (s *Store) allocatedOIDUsedLocked(id ids.OID) bool {
	state := s.State
	if s.seeding || id > state.oidCeiling {
		return false
	}
	if state.bulk != nil {
		for address, descriptor := range state.Descriptors() {
			if address.ClassID != ids.LargeObjectClassID && address.ClassID != ids.ReplicationOriginClassID && address.ObjectID == id || DefinitionOIDUsed(descriptor, id) {
				return true
			}
		}
		return false
	}
	if !s.allocatedOIDs.valid || s.allocatedOIDs.descriptors != state.descriptors {
		used := make(map[ids.OID]bool, state.DescriptorCount())
		for address, descriptor := range state.Descriptors() {
			if address.ClassID != ids.LargeObjectClassID && address.ClassID != ids.ReplicationOriginClassID {
				used[address.ObjectID] = true
			}
			ReserveDefinitionOIDs(used, descriptor)
		}
		s.allocatedOIDs = allocatedOIDSet{valid: true, descriptors: state.descriptors, used: used}
	}
	return s.allocatedOIDs.used[id]
}

// NewStore returns a store with no published state, a fresh provenance
// domain in place of owner.Domain, and allocation starting at the given
// frontiers.
func NewStore(owner Owner, nextOID ids.OID, nextRevision ids.DefinitionRevision) Store {
	owner.Domain = &ids.Domain{}
	return Store{Owner: owner, nextOID: nextOID, nextRevision: nextRevision}
}

func (s *Store) AllocateOID() (ids.OID, error)             { return s.AllocateOIDLocked() }
func (s *Store) Revision() (ids.DefinitionRevision, error) { return s.RevisionLocked() }

// Read returns a view of state owned by this store.
func (s *Store) Read(state *State) Read {
	return NewRead(&s.Owner, state)
}

// Current returns a view of the published state.
func (s *Store) Current() Read { return s.Read(s.State) }

// PublishLocked installs next as the published state. Every publication goes
// through it, so publication points stay greppable.
func (s *Store) PublishLocked(next *State) { s.State = next }

// NextOID returns the OID allocation frontier. Zero means it is exhausted.
func (s *Store) NextOID() ids.OID { return s.nextOID }

// InstallOIDFrontierLocked sets the OID frontier, for publication of a
// replacement that reserved OIDs privately.
func (s *Store) InstallOIDFrontierLocked(next ids.OID) { s.nextOID = next }

// NextRevision returns the definition revision frontier.
func (s *Store) NextRevision() ids.DefinitionRevision { return s.nextRevision }

// InstallRevisionFrontierLocked sets the definition revision frontier, for
// publication of a state whose descriptors carry revisions at or past it.
func (s *Store) InstallRevisionFrontierLocked(next ids.DefinitionRevision) {
	s.nextRevision = next
}

// RegisterProviderLocked adds a provider before the builtin catalog is
// installed. Registration closes once state exists.
func (s *Store) RegisterProviderLocked(id ids.OID, p Provider) error {
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
func (s *Store) AllocateOIDLocked() (ids.OID, error) {
	return s.AllocateOIDFromLocked(&s.nextOID, 0)
}

// catalogOIDReservation belongs to one unpublished replacement. Its frontier
// becomes live only at the catalog installation boundary; Base fences ordinary
// allocations made while candidate providers run outside the target lock.
type OIDReservation struct {
	Base, Next ids.OID
	// Replacement-private shared identity preconditions, captured only when
	// parameter reminting needs them. Never installed as catalog authority.
	PeerFrontiers map[*Store]ids.OID
}

func (s *Store) OIDReservationLocked() OIDReservation {
	return OIDReservation{Base: s.nextOID, Next: s.nextOID}
}

// AllocateOIDFromLocked is the single allocator, for both normal issuance and
// replacement reservations. Only the supplied cursor changes; all reservation,
// descriptor, definition and exhaustion checks below are identical.
func (s *Store) AllocateOIDFromLocked(cursor *ids.OID, floor ids.OID) (ids.OID, error) {
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
		if s.allocatedOIDUsedLocked(n) {
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
	return 0, ErrObjectIdentityExhausted
}

// AllocateOIDAtLeastLocked skips an unused numeric interval without replacing
// the ordinary allocator's reservation/definition checks or recycling identities.
// Only affected alias endpoint planning supplies a nonzero floor.
func (s *Store) AllocateOIDAtLeastLocked(floor ids.OID) (ids.OID, error) {
	return s.AllocateOIDFromLocked(&s.nextOID, floor)
}

// RevisionLocked reserves the next definition revision.
func (s *Store) RevisionLocked() (ids.DefinitionRevision, error) {
	if s.nextRevision == ids.DefinitionRevision(^uint64(0)) {
		return 0, fmt.Errorf("stuntgres: catalog definition revision exhausted")
	}
	r := s.nextRevision
	s.nextRevision++
	return r, nil
}

// AllocateEnumMemberLocked reserves the next enum member identity. Aborted
// reservations are never recycled.
func (s *Store) AllocateEnumMemberLocked() (int64, error) {
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
func (s *Store) ValidateObjectRefLocked(view Read, ref ids.ObjectRef) error {
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
	if ref.Address.SubID != 0 {
		r, ok := d.(*Relation)
		if !ok || !r.HasSupportedAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid catalog subobject")
		}
	}
	return nil
}

// ValidateCatalogViewLocked checks that view belongs to this store and its
// current epoch.
func (s *Store) ValidateCatalogViewLocked(view Read) error {
	if s.Domain == nil || view.Owner() != &s.Owner || view.State() == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if view.Epoch() != s.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}
