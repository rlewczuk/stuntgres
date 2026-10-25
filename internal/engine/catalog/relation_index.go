package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// A state keeps its relations in a second persistent map: exactly the
// descriptor entries whose value is a *Relation. Walks that only look for
// relations (inheritance children, a table's indexes) visit those few entries
// instead of every built-in routine, operator and type.
//
// Membership depends only on the stored value's type, which an in-place edit
// of a descriptor cannot change. SetDescriptor, RemoveDescriptor and
// Candidate.State keep the map equal to the relation entries of the
// descriptor map; copies share it like the descriptor map. During a bulk load
// the relations live in a Go map beside the bulk descriptors.

// updateRelations records that the entry at a changes from old to next.
func (s *State) updateRelations(a ids.ObjectAddress, old, next Descriptor) {
	r, is := next.(*Relation)
	if _, was := old.(*Relation); !is && !was {
		return
	}
	if s.bulk != nil {
		if is {
			s.bulk.relations[a] = r
		} else {
			delete(s.bulk.relations, a)
		}
		return
	}
	if is {
		s.relations = s.relations.Set(a, r)
	} else {
		s.relations = s.relations.Delete(a)
	}
}

// Relations iterates every *Relation descriptor in unspecified order. Like
// Descriptors it visits the entries present when it is called, each once,
// even when the loop body writes to s (outside a bulk load), and it returns
// one closure that callers range over without allocating.
func (s *State) Relations() iter.Seq2[ids.ObjectAddress, *Relation] {
	bulk, version := s.bulk, s.relations
	return func(yield func(ids.ObjectAddress, *Relation) bool) {
		if bulk != nil {
			for a, r := range bulk.relations {
				if !yield(a, r) {
					return
				}
			}
			return
		}
		for a, r := range version.All() {
			if !yield(a, r) {
				return
			}
		}
	}
}
