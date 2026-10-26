package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// A state keeps the procedures that own aggregate helper identities in one
// more persistent map: exactly the *Procedure descriptors with both Aggregate
// and Implementation set (intagg's aggregate). Helper lookups visit these few
// entries instead of every descriptor. Writers set those fields only on new
// descriptor copies, so SetDescriptor, RemoveDescriptor and Candidate.State
// keep the map equal to the matching descriptor entries; copies share it like
// the descriptor map.

func helperOwner(d Descriptor) (*Procedure, bool) {
	p, ok := d.(*Procedure)
	return p, ok && p.Aggregate != nil && p.Implementation != nil
}

// updateHelperOwners records that an entry changes from old to next.
func (s *State) updateHelperOwners(old, next Descriptor) {
	a, was := helperOwner(old)
	b, is := helperOwner(next)
	if !was && !is {
		return
	}
	if was && (!is || a.Address() != b.Address()) {
		s.helperOwners = s.helperOwners.Delete(a.Address())
	}
	if is {
		if current, found := s.helperOwners.Get(b.Address()); !found || current != b {
			s.helperOwners = s.helperOwners.Set(b.Address(), b)
		}
	}
}

// HelperOwners iterates, in unspecified order, the procedures whose
// Aggregate.Helpers carry lookup-only routine identities.
func (r Read) HelperOwners() iter.Seq2[ids.ObjectAddress, *Procedure] {
	if r.state == nil {
		return func(func(ids.ObjectAddress, *Procedure) bool) {}
	}
	return r.state.helperOwners.All()
}
