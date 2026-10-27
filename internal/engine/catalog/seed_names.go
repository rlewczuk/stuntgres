package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// Shared seed names.
//
// The builtin seed binds one name per seeded descriptor, through the seed
// helpers (bindSeedName, bindSeedKey), and the database name through a plain
// BindName. Every engine binds the same helper names in the same order: the
// seed depends only on the binary, never on engine options. The first seed
// that uses a SeedShare records its helper binds as a seedNames. A later seed
// over the same share skips each helper bind that matches the recorded one at
// its position, without computing the key, and ends with the shared map plus
// its own plain bindings.
//
// Reads during such a seed see exactly what an own seed would: a recorded
// name becomes visible once the seed reaches its bind. Any other helper bind,
// and any plain write or removal of a recorded key, first copies the visible
// recorded names into the bulk-load map ("materializes"); the seed then
// continues as an ordinary bulk load and builds its own map.

// seedNames is the recorded helper binds of one seed. It is immutable once a
// SeedShare publishes it.
type seedNames struct {
	names util.PersistentMap[ids.NameKey, ids.ObjectAddress] // every helper bind
	keys  []ids.NameKey                                      // key of each bind, in order
	order []ids.ObjectAddress                                // address of each bind, in order
	index map[ids.NameKey]int32                              // position of each key
}

// seedBinds tracks one seed's helper binds during its bulk load.
type seedBinds struct {
	shared    *seedNames          // non-nil while the helper binds follow it
	bound     int                 // helper binds matched so far
	recording bool                // this seed records its binds for its share
	order     []ids.ObjectAddress // recorded helper bind addresses
	diverged  bool                // a shared seed materialized
}

// bindSeedName binds d's descriptor name as a seed helper.
func (s *State) bindSeedName(d Descriptor) {
	if !s.skipSeedBind(d.Address()) {
		s.BindName(DescriptorNameKey(d), d.Address())
	}
}

// bindSeedKey binds a precomputed descriptor name key of a as a seed helper.
func (s *State) bindSeedKey(k ids.NameKey, a ids.ObjectAddress) {
	if !s.skipSeedBind(a) {
		s.BindName(k, a)
	}
}

// skipSeedBind reports whether the helper bind of a may be skipped because it
// matches the shared seed's next bind. Outside a seed it never skips.
func (s *State) skipSeedBind(a ids.ObjectAddress) bool {
	if s.bulk == nil || s.bulk.seed == nil {
		return false
	}
	seed := s.bulk.seed
	if shared := seed.shared; shared != nil {
		if seed.bound < len(shared.order) && shared.order[seed.bound] == a {
			seed.bound++
			return true
		}
		s.materializeSeedNames()
		return false
	}
	if seed.recording {
		seed.order = append(seed.order, a)
	}
	return false
}

// sharedSeedName looks k up among the shared binds this seed has reached.
func (b *stateBulk) sharedSeedName(k ids.NameKey) (ids.ObjectAddress, bool) {
	if b.seed == nil || b.seed.shared == nil {
		return ids.ObjectAddress{}, false
	}
	shared := b.seed.shared
	if i, ok := shared.index[k]; ok && int(i) < b.seed.bound {
		return shared.order[i], true
	}
	return ids.ObjectAddress{}, false
}

// sharesSeedKey reports whether k is a recorded shared bind, reached or not.
func (b *stateBulk) sharesSeedKey(k ids.NameKey) bool {
	if b.seed == nil || b.seed.shared == nil {
		return false
	}
	_, ok := b.seed.shared.index[k]
	return ok
}

// materializeSeedNames copies the reached shared binds into the bulk-load
// names and ends shared mode; the seed continues as an ordinary bulk load.
func (s *State) materializeSeedNames() {
	seed := s.bulk.seed
	shared := seed.shared
	if shared == nil {
		return
	}
	for i := range seed.bound {
		s.bulk.names[shared.keys[i]] = shared.order[i]
	}
	seed.shared, seed.diverged = nil, true
}

// recordSeedNames builds the seedNames of a recording seed from its final
// bulk-load names, without the database key. It returns nil unless every name
// is exactly one recorded helper bind.
func recordSeedNames(seed *seedBinds, names map[ids.NameKey]ids.ObjectAddress, database ids.NameKey) *seedNames {
	if seed == nil || !seed.recording || seed.diverged || len(names) != len(seed.order)+1 {
		return nil
	}
	byAddress := make(map[ids.ObjectAddress]ids.NameKey, len(seed.order))
	for k, a := range names {
		if k == database {
			continue
		}
		if _, duplicate := byAddress[a]; duplicate {
			return nil
		}
		byAddress[a] = k
	}
	recorded := &seedNames{keys: make([]ids.NameKey, len(seed.order)), order: seed.order, index: make(map[ids.NameKey]int32, len(seed.order))}
	for i, a := range seed.order {
		k, ok := byAddress[a]
		if _, again := recorded.index[k]; !ok || again {
			return nil
		}
		recorded.keys[i], recorded.index[k] = k, int32(i)
	}
	recorded.names = util.PersistentMapFrom(func(yield func(ids.NameKey, ids.ObjectAddress) bool) {
		for i, k := range recorded.keys {
			if !yield(k, recorded.order[i]) {
				return
			}
		}
	})
	return recorded
}
