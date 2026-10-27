package catalog

import (
	"fmt"
	"iter"
	"sync"
	"sync/atomic"

	"stuntgres/internal/engine/ids"
)

// SeedShare memoizes the parts of a builtin seed fixed by the binary. The first
// seed that uses a share fills it; later seeds reuse it read-only, so every
// store seeded with one share holds the same immutable index and name map.
// The engine keeps one share per process. A share assumes that the seed's
// helper binds (seed_names.go) do not depend on engine options: only the
// database binding may differ between the stores seeded with it.
type SeedShare struct {
	// procedureNames is the procedure-name index of the seeded procedures.
	// ProcedureIndexEdit copies its maps and buckets before the first change,
	// as for routine.SeededOperatorNames.
	procedureNames struct {
		once  sync.Once
		index *ProcedureNameIndex
	}
	// names holds the first recording seed's helper binds; later seeds skip
	// matching binds and add their own bindings to the shared map.
	names atomic.Pointer[seedNames]
	// fallbacks counts seeds that left shared names after a divergent bind.
	fallbacks atomic.Int64
	// validation is the provider validation result of the seed without its
	// database descriptor, which depends on engine options.
	validation struct {
		once sync.Once
		err  error
	}
}

// ShareProcedureNamesLocked installs the shared procedure-name index, building
// it from this seed when it is the first.
func (s *Store) ShareProcedureNamesLocked(share *SeedShare) {
	share.procedureNames.once.Do(func() {
		share.procedureNames.index = RebuildProcedureNames(s.State.Descriptors())
	})
	s.State.SetProcedureNames(share.procedureNames.index)
}

// BeginSeedLocked starts the builtin seed of a fresh store: a bulk load with
// room for capacity entries. A seed of an empty state over a share with
// recorded names follows them (seed_names.go); otherwise it records its helper
// binds for the share. Until FinishSeedLocked, OID allocation skips the global
// identity walk (see allocatedOIDUsedLocked).
func (s *Store) BeginSeedLocked(share *SeedShare, capacity int) {
	shared := share.names.Load()
	if shared != nil && s.State.DescriptorCount() == 0 && s.State.NameCount() == 0 {
		s.State.beginBulkLoad(capacity, 4)
		s.State.bulk.seed = &seedBinds{shared: shared}
	} else {
		s.State.BeginBulkLoad(capacity)
		s.State.bulk.seed = &seedBinds{recording: shared == nil}
		if shared == nil {
			s.State.bulk.seed.order = make([]ids.ObjectAddress, 0, capacity)
		}
	}
	s.seeding = true
}

// FinishSeedLocked ends the seed's bulk load and records the seeded
// descriptors for Read.SeedIdentical. A seed that followed every shared helper
// bind ends with the shared names plus its own bindings (the database's); any
// other seed builds its own name map, and a recording seed publishes its
// helper binds to the share.
func (s *Store) FinishSeedLocked(share *SeedShare) {
	s.seeding = false
	state := s.State
	address := s.Owner.DatabaseAddress()
	database := DescriptorNameKey(state.Get(address))
	seed := state.bulk.seed
	bound, ok := state.LookupName(database)
	ok = ok && bound == address
	if shared := seed.shared; ok && shared != nil && seed.bound == len(shared.order) {
		names := shared.names
		for k, a := range state.bulk.names {
			names = names.Set(k, a)
		}
		state.EndBulkLoadWithNames(names)
	} else {
		if shared != nil {
			// A short seed: its reached names are its own.
			state.materializeSeedNames()
		}
		if seed.diverged {
			share.fallbacks.Add(1)
		}
		if recorded := recordSeedNames(seed, state.bulk.names, database); ok && recorded != nil {
			share.names.CompareAndSwap(nil, recorded)
		}
		state.EndBulkLoad()
	}
	// The database descriptor depends on engine options and is validated per
	// engine, so it is never seed-identical.
	s.Owner.seed = state.descriptors.Delete(address)
}

// SharesNames reports whether the share holds recorded seed names.
func (s *SeedShare) SharesNames() bool { return s.names.Load() != nil }

// Fallbacks counts seeds that left the shared names after a divergent bind.
func (s *SeedShare) Fallbacks() int64 { return s.fallbacks.Load() }

// ValidateSeed validates the seed once per share and the current database
// descriptor, which depends on engine options, on every call.
func (s *Store) ValidateSeed(share *SeedShare) error {
	share.validation.once.Do(func() {
		share.validation.err = s.ValidateDescriptors(s.State.Descriptors())
	})
	if share.validation.err != nil {
		return share.validation.err
	}
	address := s.Owner.DatabaseAddress()
	database := s.State.Get(address)
	return s.ValidateDescriptors(func(yield func(ids.ObjectAddress, Descriptor) bool) {
		yield(address, database)
	})
}

// ValidateDescriptors checks each descriptor with the provider registered
// for its class.
func (s *Store) ValidateDescriptors(descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) error {
	view := s.Current()
	for _, d := range descriptors {
		p := s.Provider(d.Address().ClassID)
		if p == nil {
			return fmt.Errorf("stuntgres: missing catalog provider for built-in %q (class %d)", d.Name(), d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return fmt.Errorf("stuntgres: invalid built-in catalog descriptor %q (class %d): %w", d.Name(), d.Address().ClassID, err)
		}
	}
	return nil
}
