package catalog

import (
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// Seed descriptor templates.
//
// Every builtin seed installs its descriptors at the same addresses in the
// same order (see seed_names.go). The first seed that uses a SeedShare records
// that order as a seedDescriptors. A later seed of an empty state keeps its
// descriptors in a slice by recorded position instead of a Go map, and its
// bulk load ends by mapping the record's persistent map to them
// (util.PersistentMapValues), without hashing or partitioning again.
// Addresses outside the record go to the ordinary bulk-load map. A seed that
// leaves a recorded position empty builds its map from its descriptors as
// usual.

// seedDescriptors is the recorded install order of one seed. It is immutable
// once a SeedShare publishes it.
type seedDescriptors struct {
	slots util.PersistentMap[ids.ObjectAddress, int32] // install position by address
	order []ids.ObjectAddress                          // address by install position
}

// seedFill holds a seed's descriptors at their recorded positions.
type seedFill struct {
	shared *seedDescriptors
	values []Descriptor
	filled int // non-nil values
	cursor int // the position after the last install, where the next one usually goes
}

// slot returns a's recorded position.
func (f *seedFill) slot(a ids.ObjectAddress) (int, bool) {
	if f.cursor < len(f.shared.order) && f.shared.order[f.cursor] == a {
		return f.cursor, true
	}
	i, ok := f.shared.slots.Get(a)
	return int(i), ok
}

func (f *seedFill) find(a ids.ObjectAddress) (Descriptor, bool, bool) {
	i, recorded := f.slot(a)
	if !recorded {
		return nil, false, false
	}
	return f.values[i], f.values[i] != nil, true
}

func (f *seedFill) put(a ids.ObjectAddress, d Descriptor) bool {
	i, recorded := f.slot(a)
	if !recorded {
		return false
	}
	if f.values[i] == nil {
		f.filled++
	}
	f.values[i], f.cursor = d, i+1
	return true
}

func (f *seedFill) drop(a ids.ObjectAddress) bool {
	i, recorded := f.slot(a)
	if !recorded {
		return false
	}
	if f.values[i] != nil {
		f.values[i] = nil
		f.filled--
	}
	return true
}

func (f *seedFill) all(yield func(ids.ObjectAddress, Descriptor) bool) bool {
	for i, d := range f.values {
		if d != nil && !yield(f.shared.order[i], d) {
			return false
		}
	}
	return true
}

// complete reports whether every recorded position holds a descriptor.
func (f *seedFill) complete() bool { return f.filled == len(f.values) }

// bulkDescriptorMap builds the persistent descriptor map at the end of a bulk
// load.
func (s *State) bulkDescriptorMap() util.PersistentMap[ids.ObjectAddress, Descriptor] {
	f := s.bulk.fill
	if f == nil {
		return util.PersistentMapOf(s.bulk.descriptors)
	}
	if !f.complete() {
		return util.PersistentMapFrom(s.allDescriptors())
	}
	m := util.PersistentMapValues(f.shared.slots, func(_ ids.ObjectAddress, i int32) Descriptor { return f.values[i] })
	if len(s.bulk.descriptors) != 0 {
		m = m.SetAll(maps.All(s.bulk.descriptors))
	}
	return m
}

// recordSeedDescriptors builds the seedDescriptors of a recording seed that
// installed each of its descriptors once and removed none, or returns nil.
func recordSeedDescriptors(seed *seedBinds, descriptors map[ids.ObjectAddress]Descriptor) *seedDescriptors {
	if seed == nil || !seed.recording || seed.descriptorsRemoved || len(seed.descriptorOrder) != len(descriptors) {
		return nil
	}
	recorded := &seedDescriptors{order: seed.descriptorOrder}
	recorded.slots = util.PersistentMapFrom(func(yield func(ids.ObjectAddress, int32) bool) {
		for i, a := range recorded.order {
			if !yield(a, int32(i)) {
				return
			}
		}
	})
	if recorded.slots.Len() != len(recorded.order) {
		return nil
	}
	return recorded
}
