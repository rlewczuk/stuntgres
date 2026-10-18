package engine

import (
	"maps"

	"stuntgres/internal/engine/ids"
)

// Write access to an unpublished catalog state.
//
// These methods, the constructors in catalog literals and cloneCatalog are the
// only code that changes the descriptor and name maps. Callers write only to
// states they are still building: the bootstrap seed, a clone, a stage
// candidate or a replacement before publication. A published state is never
// written again; publication swaps the whole state under Engine.mu.

// setDescriptor stores d at a.
func (s *catalogState) setDescriptor(a ids.ObjectAddress, d Descriptor) { s.descriptors[a] = d }

// removeDescriptor deletes the descriptor at a.
func (s *catalogState) removeDescriptor(a ids.ObjectAddress) { delete(s.descriptors, a) }

// bindName binds k to a.
func (s *catalogState) bindName(k ids.NameKey, a ids.ObjectAddress) { s.names[k] = a }

// unbindName removes the binding of k.
func (s *catalogState) unbindName(k ids.NameKey) { delete(s.names, k) }

// cloneDescriptors gives s its own copy of its descriptor map, so that s can
// be written without changing the state it was copied from. Names stay
// shared.
func (s *catalogState) cloneDescriptors() { s.descriptors = maps.Clone(s.descriptors) }

// cloneNames gives s its own copy of its name map. Descriptors stay shared.
func (s *catalogState) cloneNames() { s.names = maps.Clone(s.names) }

// resetDescriptors gives s an empty descriptor map with room for capacity
// entries. Names stay shared.
func (s *catalogState) resetDescriptors(capacity int) {
	s.descriptors = make(map[ids.ObjectAddress]Descriptor, capacity)
}

// resetMaps gives s empty descriptor and name maps with room for capacity
// entries and returns the previous descriptor map, which s no longer uses.
func (s *catalogState) resetMaps(capacity int) map[ids.ObjectAddress]Descriptor {
	previous := s.descriptors
	s.descriptors = make(map[ids.ObjectAddress]Descriptor, capacity)
	s.names = make(map[ids.NameKey]ids.ObjectAddress, capacity)
	return previous
}
