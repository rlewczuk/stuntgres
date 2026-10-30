package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// A state keeps the claimed side of extension membership in one more
// persistent map: the address of every descriptor whose ExtensionMembership is
// set, mapped to that extension. ValidateExtensionMembership checks an
// extension's member list against these few entries instead of every
// descriptor. Writers set a descriptor's Extension only on new copies
// (WithExtensionMembership and the remap helpers), so SetDescriptor,
// RemoveDescriptor and Candidate.State keep the map equal to the descriptors'
// claims; copies share it like the descriptor map.

// updateExtensionMembers records that the entry at a changes from old to next.
// It is idempotent, so Candidate.State may apply a removal and a new
// descriptor at one address against the same base entry.
func (s *State) updateExtensionMembers(a ids.ObjectAddress, old, next Descriptor) {
	was, is := ExtensionMembership(old), ExtensionMembership(next)
	if is == (ids.ObjectAddress{}) {
		if was != (ids.ObjectAddress{}) {
			s.extensionMembers = s.extensionMembers.Delete(a)
		}
		return
	}
	if current, found := s.extensionMembers.Get(a); !found || current != is {
		s.extensionMembers = s.extensionMembers.Set(a, is)
	}
}

// extensionClaims iterates, in unspecified order, the addresses of the
// descriptors that claim an extension membership, with that extension.
func (r Read) extensionClaims() iter.Seq2[ids.ObjectAddress, ids.ObjectAddress] {
	if r.state == nil {
		return func(func(ids.ObjectAddress, ids.ObjectAddress) bool) {}
	}
	return r.state.extensionMembers.All()
}
