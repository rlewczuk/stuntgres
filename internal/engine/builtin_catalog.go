package engine

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

// Every engine seeds the same built-in catalog from compiled tables; only the
// current database descriptor depends on engine options. Work whose result is
// a property of the seed is done once per process and shared by every engine.

// builtinCatalogCapacity is the number of descriptors and names a fresh engine
// seeds from the built-in tables, plus room for the few seeded outside them:
// namespaces, tablespaces, catalog sources and the current database.
func builtinCatalogCapacity(types int) int {
	return types + len(catalog.BuiltinCollations) + typedef.BuiltinConversionCount + len(routine.BuiltinRoutineDeclarations) +
		len(catalog.BuiltinConversions) + routine.BuiltinOperatorCount + catalog.BuiltinFamilyDescriptorCount + 128
}

// installBuiltinDomainChecks binds the builtin domain CHECK and DEFAULT seeds
// into their seeded types. It uses the same VALUE binder and dependency
// cooking as user domains, without a session, SQL execution or a separately
// allocated type.
func (e *Engine) installBuiltinDomainChecks() error {
	checks, err := typedef.BuiltinDomainCheckSeeds()
	if err != nil {
		return err
	}
	for _, seed := range checks {
		d := e.catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: seed.ID})
		if err := e.bindDomainConstraintDefinitions(context.Background(), nil, e.bootstrapAuthorization(d.Owner), nil, d, seed.Constraints, sqltype.InputEnvironment{}); err != nil {
			return err
		}
	}
	for _, seed := range typedef.BuiltinDomainDefaultSeeds() {
		d := e.catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: seed.ID})
		if err := e.bindDomainDefault(context.Background(), nil, e.bootstrapAuthorization(d.Owner), nil, d, seed.Expression, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err != nil {
			return err
		}
	}
	return nil
}

// bootstrapAuthorization is the published-catalog authority of user under
// which builtin seeds are bound.
func (e *Engine) bootstrapAuthorization(user authorization.PrincipalRef) AuthorizationView {
	view := e.catalog.Current()
	return e.authorityView(access.View{
		Owner: &e.catalog.Owner, PairedCatalog: view, RoleRecord: e.roles.published, Catalog: view,
		Roles: e.roles.read(&e.catalog.Owner, view, e.roles.published), State: authorization.RoleState{CurrentUser: user},
	})
}

// builtinSeed is the process's seed share: every engine seeds the same
// builtin catalog, so they share its procedure-name index, name map and
// validation result.
var builtinSeed = &seedShare{}

// seedShare memoizes the parts of a builtin seed fixed by the binary. The first
// seed in the process fills it; later seeds reuse it read-only.
type seedShare struct {
	// procedureNames is the procedure-name index of the seeded procedures.
	// ProcedureIndexEdit copies its maps and buckets before the first change,
	// as for routine.SeededOperatorNames.
	procedureNames struct {
		once  sync.Once
		index *catalog.ProcedureNameIndex
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

// shareProcedureNamesLocked installs the shared procedure-name index, building
// it from this seed when it is the first.
func shareProcedureNamesLocked(s *catalog.Store, share *seedShare) {
	share.procedureNames.once.Do(func() {
		share.procedureNames.index = catalog.RebuildProcedureNames(s.State.Descriptors())
	})
	s.State.SetProcedureNames(share.procedureNames.index)
}

// finishSeedLocked ends the seed's bulk load. The name map is the shared seed
// names plus this store's database binding; a seed that bound a different
// number of names builds its own map instead.
func finishSeedLocked(s *catalog.Store, share *seedShare) {
	state := s.State
	address := s.Owner.DatabaseAddress()
	database := catalog.DescriptorNameKey(state.Get(address))
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

// validateSeed validates the seed once per share and the current database
// descriptor, which depends on engine options, on every call.
func validateSeed(s *catalog.Store, share *seedShare) error {
	share.validation.once.Do(func() {
		share.validation.err = validateCatalogDescriptors(s, s.State.Descriptors())
	})
	if share.validation.err != nil {
		return share.validation.err
	}
	address := s.Owner.DatabaseAddress()
	database := s.State.Get(address)
	return validateCatalogDescriptors(s, func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		yield(address, database)
	})
}

// validateCatalogDescriptors checks each built-in descriptor with the
// provider registered for its class.
func validateCatalogDescriptors(s *catalog.Store, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) error {
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
