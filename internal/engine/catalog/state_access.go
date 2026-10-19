package catalog

import (
	"iter"
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Access to the non-descriptor fields of a catalog state and view.
//
// Readers use the getters on any state. The setters, like the write API in
// state_write.go, are only for a state that is still being built: the
// bootstrap seed, a clone, a stage candidate or a replacement before
// publication. A published state is never written again.

// NewState returns an empty state at epoch with empty descriptor and name maps.
func NewState(epoch ids.CatalogEpoch) *State {
	return NewStateSized(epoch, 0)
}

// NewStateSized is NewState with room for n descriptors and n names, so a
// seed of known size fills its maps without rehashing them as they grow.
func NewStateSized(epoch ids.CatalogEpoch, n int) *State {
	return &State{epoch: epoch, descriptors: make(map[ids.ObjectAddress]Descriptor, n), names: make(map[ids.NameKey]ids.ObjectAddress, n)}
}

// cloneMap copies m without rehashing its entries; a nil m yields an empty,
// writable map. Catalog copies use it because they copy whole catalogs.
func cloneMap[M ~map[K]V, K comparable, V any](m M) M {
	if m == nil {
		return make(M)
	}
	return maps.Clone(m)
}

// newCatalogRead returns the view of state owned by owner.
func NewRead(owner *Owner, state *State) Read {
	return Read{owner: owner, state: state}
}

// Owner returns the owner record of the view, or nil for a zero view.
func (r Read) Owner() *Owner { return r.owner }

// State returns the state the view reads, or nil for a zero view.
func (r Read) State() *State { return r.state }

// WithState returns a view of state with the same owner.
func (r Read) WithState(state *State) Read {
	return Read{owner: r.owner, state: state}
}

// Epoch returns the catalog epoch of the state.
func (s *State) Epoch() ids.CatalogEpoch { return s.epoch }

// SetEpoch sets the epoch of an unpublished state.
func (s *State) SetEpoch(epoch ids.CatalogEpoch) { s.epoch = epoch }

// NamespaceGeneration returns the namespace generation of the state.
func (s *State) NamespaceGeneration() uint64 { return s.namespaceGeneration }

// SetNamespaceGeneration sets the namespace generation of an unpublished
// state.
func (s *State) SetNamespaceGeneration(generation uint64) {
	s.namespaceGeneration = generation
}

// OperatorNames returns the operator name index of the state.
func (s *State) OperatorNames() *OperatorNameIndex { return s.operatorNames }

// SetOperatorNames sets the operator name index of an unpublished state.
func (s *State) SetOperatorNames(index *OperatorNameIndex) { s.operatorNames = index }

// DefaultACL returns the default ACL record at key, or nil.
func (s *State) DefaultACL(key DefaultACLKey) *DefaultACLRecord {
	return s.defaultACLs[key]
}

// DefaultACLs iterates every default ACL record in unspecified order.
func (s *State) DefaultACLs() iter.Seq2[DefaultACLKey, *DefaultACLRecord] {
	return func(yield func(DefaultACLKey, *DefaultACLRecord) bool) {
		for key, record := range s.defaultACLs {
			if !yield(key, record) {
				return
			}
		}
	}
}

// DefaultACLCount returns the number of default ACL records.
func (s *State) DefaultACLCount() int { return len(s.defaultACLs) }

// SetDefaultACLs replaces the default ACL records of an unpublished state
// with records, which the state then owns.
func (s *State) SetDefaultACLs(records map[DefaultACLKey]*DefaultACLRecord) {
	s.defaultACLs = records
}

// SetDefaultACL stores record at key in an unpublished state that owns its
// default ACL map.
func (s *State) SetDefaultACL(key DefaultACLKey, record *DefaultACLRecord) {
	if s.defaultACLs == nil {
		s.defaultACLs = make(map[DefaultACLKey]*DefaultACLRecord)
	}
	s.defaultACLs[key] = record
}

// OverlayDefaultACLs gives an unpublished state the default ACL records of
// source with changes applied. A nil change removes its key. Without changes
// the state shares source's records.
func (s *State) OverlayDefaultACLs(source *State, changes map[DefaultACLKey]*DefaultACLRecord) {
	s.defaultACLs = overlayDefaultACLs(source.defaultACLs, changes)
}

// DumpPriority returns the dump priority recorded for a, or zero.
func (s *State) DumpPriority(a ids.ObjectAddress) ids.OID { return s.dumpPriorities[a] }

// LookupDumpPriority returns the dump priority recorded for a and whether one
// is recorded.
func (s *State) LookupDumpPriority(a ids.ObjectAddress) (ids.OID, bool) {
	priority, ok := s.dumpPriorities[a]
	return priority, ok
}

// HasDumpPriorities reports whether the state holds a dump priority map. The
// map is allocated only when a priority is recorded.
func (s *State) HasDumpPriorities() bool { return s.dumpPriorities != nil }

// DumpPriorities iterates every recorded dump priority in unspecified order.
func (s *State) DumpPriorities() iter.Seq2[ids.ObjectAddress, ids.OID] {
	return func(yield func(ids.ObjectAddress, ids.OID) bool) {
		for a, priority := range s.dumpPriorities {
			if !yield(a, priority) {
				return
			}
		}
	}
}

// DumpPriorityCount returns the number of recorded dump priorities.
func (s *State) DumpPriorityCount() int { return len(s.dumpPriorities) }

// SetDumpPriority records the dump priority of a in an unpublished state
// that owns its priority map.
func (s *State) SetDumpPriority(a ids.ObjectAddress, priority ids.OID) {
	if s.dumpPriorities == nil {
		s.dumpPriorities = make(map[ids.ObjectAddress]ids.OID)
	}
	s.dumpPriorities[a] = priority
}

// RemoveDumpPriority deletes the dump priority of a from an unpublished state
// that owns its priority map.
func (s *State) RemoveDumpPriority(a ids.ObjectAddress) { delete(s.dumpPriorities, a) }

// SetDumpPriorities replaces the dump priorities of an unpublished state with
// priorities, which the state then owns.
func (s *State) SetDumpPriorities(priorities map[ids.ObjectAddress]ids.OID) {
	s.dumpPriorities = priorities
}

// ScalarImports returns the replacement candidate's scalar type translation:
// exact source codecs to their destination codecs. Only replacement code
// reads or writes it, and it is cleared before publication.
func (s *State) ScalarImports() map[*sqltype.Descriptor]*sqltype.Descriptor {
	return s.scalarImports
}

// SetScalarImports replaces the scalar type translation.
func (s *State) SetScalarImports(imports map[*sqltype.Descriptor]*sqltype.Descriptor) {
	s.scalarImports = imports
}

// SetScalarImport records the translation of source to destination.
func (s *State) SetScalarImport(source, destination *sqltype.Descriptor) {
	if s.scalarImports == nil {
		s.scalarImports = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
	}
	s.scalarImports[source] = destination
}

// ACLItemImport returns the replacement candidate's aclitem translation, or
// nil.
func (s *State) ACLItemImport() func(sqltype.Value) (sqltype.Value, error) {
	return s.aclItemImport
}

// SetACLItemImport sets the aclitem translation of a replacement candidate.
func (s *State) SetACLItemImport(importer func(sqltype.Value) (sqltype.Value, error)) {
	s.aclItemImport = importer
}
