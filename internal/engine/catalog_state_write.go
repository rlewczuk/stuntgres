package engine

import (
	"iter"
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

// SetDescriptor stores d at a.
func (s *catalogState) SetDescriptor(a ids.ObjectAddress, d Descriptor) { s.descriptors[a] = d }

// RemoveDescriptor deletes the descriptor at a.
func (s *catalogState) RemoveDescriptor(a ids.ObjectAddress) { delete(s.descriptors, a) }

// BindName binds k to a.
func (s *catalogState) BindName(k ids.NameKey, a ids.ObjectAddress) { s.names[k] = a }

// UnbindName removes the binding of k.
func (s *catalogState) UnbindName(k ids.NameKey) { delete(s.names, k) }

// CloneDescriptors gives s its own copy of its descriptor map, so that s can
// be written without changing the state it was copied from. Names stay
// shared.
func (s *catalogState) CloneDescriptors() { s.descriptors = maps.Clone(s.descriptors) }

// CloneNames gives s its own copy of its name map. Descriptors stay shared.
func (s *catalogState) CloneNames() { s.names = maps.Clone(s.names) }

// ResetDescriptors gives s an empty descriptor map with room for capacity
// entries. Names stay shared.
func (s *catalogState) ResetDescriptors(capacity int) {
	s.descriptors = make(map[ids.ObjectAddress]Descriptor, capacity)
}

// ResetMaps gives s empty descriptor and name maps with room for capacity
// entries and iterates the previous descriptors, which s no longer uses.
func (s *catalogState) ResetMaps(capacity int) iter.Seq2[ids.ObjectAddress, Descriptor] {
	previous := s.descriptors
	s.descriptors = make(map[ids.ObjectAddress]Descriptor, capacity)
	s.names = make(map[ids.NameKey]ids.ObjectAddress, capacity)
	return maps.All(previous)
}
