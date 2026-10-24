package catalog

import (
	"fmt"
	"iter"
	"sync"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// SeedShare memoizes the parts of a builtin seed fixed by the binary. The first
// seed that uses a share fills it; later seeds reuse it read-only, so every
// store seeded with one share holds the same immutable index and name map.
// The engine keeps one share per process.
type SeedShare struct {
	// procedureNames is the procedure-name index of the seeded procedures.
	// ProcedureIndexEdit copies its maps and buckets before the first change,
	// as for routine.SeededOperatorNames.
	procedureNames struct {
		once  sync.Once
		index *ProcedureNameIndex
	}
	// names holds the seed's name bindings other than the current database's.
	// Seeds add their database binding to their own version of the map.
	names struct {
		once  sync.Once
		names util.PersistentMap[ids.NameKey, ids.ObjectAddress]
	}
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

// FinishSeedLocked ends the seed's bulk load. The name map is the shared seed
// names plus this store's database binding; a seed that bound a different
// number of names builds its own map instead.
func (s *Store) FinishSeedLocked(share *SeedShare) {
	state := s.State
	address := s.Owner.DatabaseAddress()
	database := DescriptorNameKey(state.Get(address))
	share.names.once.Do(func() {
		share.names.names = util.PersistentMapFrom(func(yield func(ids.NameKey, ids.ObjectAddress) bool) {
			for k, a := range state.Names() {
				if k != database && !yield(k, a) {
					return
				}
			}
		})
	})
	if bound, ok := state.LookupName(database); !ok || bound != address || state.NameCount() != share.names.names.Len()+1 {
		state.EndBulkLoad()
		return
	}
	state.EndBulkLoadWithNames(share.names.names.Set(database, address))
}

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
