package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// A state tracks its unseeded descriptors: every descriptor entry that is not
// as a builtin seed installed it. The seed's bulk load installs seeded
// entries. Every other write adds the written entry to another persistent map
// and a removal deletes it; SetDescriptor, RemoveDescriptor and
// Candidate.State maintain it, and copies share it. Any other bulk load is a
// whole-catalog rewrite, so it marks every entry unseeded (unseededAll), as
// does a seed over a state that already held unseeded entries.
//
// Builtin seeds install the same content in every engine, and a seeded
// descriptor references only seeded objects, whose OIDs are at most the seed's
// OID ceiling (seedCeiling). So no seeded descriptor references an object with a
// higher OID, and a walk for the dependents of one may visit just the unseeded
// entries (Read.SeedMayReference).

// updateUnseeded records that the entry at a is now d, or removed for nil.
// Bulk loads are settled by endBulkUnseeded.
func (s *State) updateUnseeded(a ids.ObjectAddress, d Descriptor) {
	if s.bulk != nil || s.unseededAll {
		return
	}
	if d == nil {
		s.unseeded = s.unseeded.Delete(a)
	} else {
		s.unseeded = s.unseeded.Set(a, d)
	}
}

func (s *State) endBulkUnseeded() {
	if s.bulk.seed == nil || s.unseeded.Len() != 0 {
		s.unseeded, s.unseededAll = util.PersistentMap[ids.ObjectAddress, Descriptor]{}, true
	}
}

// allUnseeded reports a state whose every entry counts as unseeded, including
// one in the middle of a whole-catalog rewrite.
func (s *State) allUnseeded() bool {
	return s.unseededAll || s.bulk != nil && s.bulk.seed == nil
}

// unseededDescriptors iterates the unseeded entries present when it is called,
// each once, like allDescriptors.
func (s *State) unseededDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	if s.allUnseeded() {
		return s.allDescriptors()
	}
	return s.unseeded.All()
}

// UnseededDescriptors iterates the descriptors that are not as a builtin seed
// installed them, in unspecified order.
func (r Read) UnseededDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return r.state.unseededDescriptors()
}

// Unseeded reports whether the entry at a, if any, is not as a builtin seed
// installed it.
func (r Read) Unseeded(a ids.ObjectAddress) bool {
	if r.state.allUnseeded() {
		return true
	}
	_, ok := r.state.unseeded.Get(a)
	return ok
}

// SeedMayReference reports whether a seeded descriptor may reference a: only
// objects within the seed's OID ceiling can be. For a state never seeded every
// descriptor is unseeded, so the answer does not matter.
func (r Read) SeedMayReference(a ids.ObjectAddress) bool {
	return a.ObjectID <= r.state.seedCeiling
}
