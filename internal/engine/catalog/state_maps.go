package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// Storage of a state's descriptors and name bindings.
//
// A state keeps them in persistent maps (util.PersistentMap). Copying a state,
// building a candidate's state or writing to a copy shares every unchanged
// part with the original instead of copying the whole catalog, and a published
// state, which is never written again, stays safe for lock-free readers.
//
// A bulk load is the exception for writers that set most entries of a state:
// building the bootstrap seed and whole-catalog rewrites. Between
// BeginBulkLoad and EndBulkLoad the entries live in ordinary Go maps, so each
// write costs a map assignment; EndBulkLoad builds both persistent maps in one
// pass. A state in a bulk load is private to its writer and is never copied or
// published.

// stateBulk holds a state's entries during a bulk load. seed is set only for
// the builtin seed (see seed_names.go); names then holds just the bindings
// that are not reached shared seed names.
type stateBulk struct {
	descriptors map[ids.ObjectAddress]Descriptor
	names       map[ids.NameKey]ids.ObjectAddress
	relations   map[ids.ObjectAddress]*Relation
	seed        *seedBinds
	// fill, set only for a seed following a recorded install order, holds the
	// recorded addresses' descriptors; descriptors then holds just the others.
	fill *seedFill
}

// BeginBulkLoad moves s's entries into Go maps with room for capacity
// entries each, for a writer about to set most of them. Reads and writes see
// the same entries as before until EndBulkLoad.
func (s *State) BeginBulkLoad(capacity int) {
	s.beginBulkLoad(capacity, capacity)
}

func (s *State) beginBulkLoad(descriptors, names int) {
	if s.bulk != nil {
		panic("stuntgres: catalog bulk load already in progress")
	}
	b := &stateBulk{
		descriptors: make(map[ids.ObjectAddress]Descriptor, max(descriptors, s.descriptors.Len())),
		names:       make(map[ids.NameKey]ids.ObjectAddress, max(names, s.names.Len())),
		relations:   make(map[ids.ObjectAddress]*Relation, s.relations.Len()),
	}
	for a, d := range s.descriptors.All() {
		b.descriptors[a] = d
	}
	for k, a := range s.names.All() {
		b.names[k] = a
	}
	for a, r := range s.relations.All() {
		b.relations[a] = r
	}
	s.bulk = b
}

// EndBulkLoad builds s's persistent maps from the bulk-load entries.
func (s *State) EndBulkLoad() {
	if s.bulk == nil {
		panic("stuntgres: no catalog bulk load in progress")
	}
	s.descriptors = s.bulkDescriptorMap()
	s.names = util.PersistentMapOf(s.bulk.names)
	s.relations = util.PersistentMapOf(s.bulk.relations)
	s.endBulkUnseeded()
	s.bulk = nil
}

// EndBulkLoadWithNames ends a bulk load like EndBulkLoad but installs names
// instead of building a name map from the bulk-load bindings. The caller
// guarantees that names holds exactly those bindings; the engine seed uses it
// to share one built-in name map among engines.
func (s *State) EndBulkLoadWithNames(names util.PersistentMap[ids.NameKey, ids.ObjectAddress]) {
	if s.bulk == nil {
		panic("stuntgres: no catalog bulk load in progress")
	}
	s.descriptors = s.bulkDescriptorMap()
	s.names = names
	s.relations = util.PersistentMapOf(s.bulk.relations)
	s.endBulkUnseeded()
	s.bulk = nil
}

// mustNotBulkLoad guards operations that share or replace a state's maps.
func (s *State) mustNotBulkLoad() {
	if s.bulk != nil {
		panic("stuntgres: catalog state used during a bulk load")
	}
}

func (s *State) findDescriptor(a ids.ObjectAddress) (Descriptor, bool) {
	if s.bulk != nil {
		if f := s.bulk.fill; f != nil {
			if d, ok, recorded := f.find(a); recorded {
				return d, ok
			}
		}
		d, ok := s.bulk.descriptors[a]
		return d, ok
	}
	return s.descriptors.Get(a)
}

func (s *State) putDescriptor(a ids.ObjectAddress, d Descriptor) {
	if s.bulk != nil {
		if f := s.bulk.fill; f != nil && f.put(a, d) {
			return
		}
		if seed := s.bulk.seed; seed != nil && seed.recording {
			if _, exists := s.bulk.descriptors[a]; !exists {
				seed.descriptorOrder = append(seed.descriptorOrder, a)
			}
		}
		s.bulk.descriptors[a] = d
		return
	}
	s.descriptors = s.descriptors.Set(a, d)
}

func (s *State) dropDescriptor(a ids.ObjectAddress) {
	if s.bulk != nil {
		if f := s.bulk.fill; f != nil && f.drop(a) {
			return
		}
		if seed := s.bulk.seed; seed != nil {
			seed.descriptorsRemoved = true
		}
		delete(s.bulk.descriptors, a)
		return
	}
	s.descriptors = s.descriptors.Delete(a)
}

func (s *State) findName(k ids.NameKey) (ids.ObjectAddress, bool) {
	if s.bulk != nil {
		if a, ok := s.bulk.names[k]; ok {
			return a, true
		}
		return s.bulk.sharedSeedName(k)
	}
	return s.names.Get(k)
}

func (s *State) putName(k ids.NameKey, a ids.ObjectAddress) {
	if s.bulk != nil {
		if s.bulk.sharesSeedKey(k) {
			s.materializeSeedNames()
		}
		s.bulk.names[k] = a
		return
	}
	s.names = s.names.Set(k, a)
}

func (s *State) dropName(k ids.NameKey) {
	if s.bulk != nil {
		if s.bulk.sharesSeedKey(k) {
			s.materializeSeedNames()
		}
		delete(s.bulk.names, k)
		return
	}
	s.names = s.names.Delete(k)
}

// allDescriptors iterates the descriptors present when it is called. Outside
// a bulk load that is one persistent version, so the loop body may write to s
// and every original entry is still visited once. It returns one static
// closure, which lets callers range over it without allocating.
func (s *State) allDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	bulk, version := s.bulk, s.descriptors
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		if bulk != nil {
			if bulk.fill != nil && !bulk.fill.all(yield) {
				return
			}
			for a, d := range bulk.descriptors {
				if !yield(a, d) {
					return
				}
			}
			return
		}
		for a, d := range version.All() {
			if !yield(a, d) {
				return
			}
		}
	}
}

func (s *State) allNames() iter.Seq2[ids.NameKey, ids.ObjectAddress] {
	bulk, version := s.bulk, s.names
	return func(yield func(ids.NameKey, ids.ObjectAddress) bool) {
		if bulk != nil {
			for k, a := range bulk.names {
				if !yield(k, a) {
					return
				}
			}
			if seed := bulk.seed; seed != nil && seed.shared != nil {
				for i := range seed.bound {
					if !yield(seed.shared.keys[i], seed.shared.order[i]) {
						return
					}
				}
			}
			return
		}
		for k, a := range version.All() {
			if !yield(k, a) {
				return
			}
		}
	}
}

func (s *State) descriptorCount() int {
	if s.bulk != nil {
		if f := s.bulk.fill; f != nil {
			return f.filled + len(s.bulk.descriptors)
		}
		return len(s.bulk.descriptors)
	}
	return s.descriptors.Len()
}

func (s *State) nameCount() int {
	if s.bulk != nil {
		if seed := s.bulk.seed; seed != nil && seed.shared != nil {
			return len(s.bulk.names) + seed.bound
		}
		return len(s.bulk.names)
	}
	return s.names.Len()
}
