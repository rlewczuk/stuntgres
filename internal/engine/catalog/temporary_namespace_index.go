package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// A state keeps its temporary namespaces, the *Namespace descriptors with a
// session Temporary token, in one more persistent map, so session end finds a
// session's namespaces without visiting every descriptor. SetDescriptor,
// RemoveDescriptor and Candidate.State keep the map equal to the matching
// descriptor entries; copies share it like the descriptor map.

func temporaryNamespace(d Descriptor) (*Namespace, bool) {
	n, ok := d.(*Namespace)
	return n, ok && n.Temporary != (ids.SessionToken{})
}

// updateTemporaryNamespaces records that an entry changes from old to next.
func (s *State) updateTemporaryNamespaces(old, next Descriptor) {
	a, was := temporaryNamespace(old)
	b, is := temporaryNamespace(next)
	if !was && !is {
		return
	}
	if was && (!is || a.Address() != b.Address()) {
		s.temporaryNamespaces = s.temporaryNamespaces.Delete(a.Address())
	}
	if is {
		if current, found := s.temporaryNamespaces.Get(b.Address()); !found || current != b {
			s.temporaryNamespaces = s.temporaryNamespaces.Set(b.Address(), b)
		}
	}
}

// TemporaryNamespaces iterates, in unspecified order, the namespaces that
// belong to a session.
func (r Read) TemporaryNamespaces() iter.Seq2[ids.ObjectAddress, *Namespace] {
	if r.state == nil {
		return func(func(ids.ObjectAddress, *Namespace) bool) {}
	}
	return r.state.temporaryNamespaces.All()
}
