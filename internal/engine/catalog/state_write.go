package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// Write access to an unpublished catalog state.
//
// These methods, the constructors and CloneState are the only code that
// changes the descriptor and name maps. Callers write only to states they are
// still building: the bootstrap seed, a clone, a stage candidate or a
// replacement before publication. A published state is never written again;
// the engine's publication swaps the whole state under its mutex.

// SetDescriptor stores d at a.
func (s *State) SetDescriptor(a ids.ObjectAddress, d Descriptor) {
	old, _ := s.findDescriptor(a)
	s.updateCastIndex(old, d)
	oldKey, hadDefault := descriptorDefaultKey(old)
	newKey, hasDefault := descriptorDefaultKey(d)
	if hadDefault && (!hasDefault || oldKey != newKey) {
		s.dropName(oldKey)
	}
	s.putDescriptor(a, d)
	if hasDefault {
		if bound, _ := s.findName(newKey); bound != a {
			s.putName(newKey, a)
		}
	}
}

// RemoveDescriptor deletes the descriptor at a.
func (s *State) RemoveDescriptor(a ids.ObjectAddress) {
	old, _ := s.findDescriptor(a)
	s.updateCastIndex(old, nil)
	if key, ok := descriptorDefaultKey(old); ok {
		s.dropName(key)
	}
	s.dropDescriptor(a)
}

// BindName binds k to a.
func (s *State) BindName(k ids.NameKey, a ids.ObjectAddress) { s.putName(k, a) }

// UnbindName removes the binding of k.
func (s *State) UnbindName(k ids.NameKey) { s.dropName(k) }

// ResetDescriptors gives s an empty descriptor map. Names stay as they are.
func (s *State) ResetDescriptors() {
	s.mustNotBulkLoad()
	s.descriptors = util.PersistentMap[ids.ObjectAddress, Descriptor]{}
	s.procedureNames = nil
	s.castPairs = nil
}

// ResetMaps gives s empty descriptor and name maps and iterates the previous
// descriptors, which s no longer uses. Writers that refill s use a bulk load.
func (s *State) ResetMaps() iter.Seq2[ids.ObjectAddress, Descriptor] {
	s.mustNotBulkLoad()
	previous := s.descriptors
	s.descriptors = util.PersistentMap[ids.ObjectAddress, Descriptor]{}
	s.names = util.PersistentMap[ids.NameKey, ids.ObjectAddress]{}
	s.procedureNames = nil
	s.castPairs = nil
	return previous.All()
}
