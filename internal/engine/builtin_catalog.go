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
)

// Every engine seeds the same built-in catalog from compiled tables; only the
// current database descriptor depends on engine options. Work whose result is
// a property of the seed is done once per process and shared by every engine.

// builtinCatalogCapacity is the number of descriptors and names a fresh engine
// seeds from the built-in tables, plus room for the few seeded outside them:
// namespaces, tablespaces, catalog sources and the current database.
func builtinCatalogCapacity(types int) int {
	return types + len(catalog.BuiltinCollations) + typedef.BuiltinConversionCount + len(routine.BuiltinRoutineDeclarations) +
		len(catalog.BuiltinConversions) + routine.BuiltinOperatorCount + 128
}

// installBuiltinProcedures installs the builtin routine descriptors.
func (e *Engine) installBuiltinProcedures() {
	procedures := routine.BuiltinProcedures(e.roles.domain.Principal(authorization.BootstrapPrincipalID))
	for i := range procedures {
		d := &procedures[i]
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
}

// installBuiltinConversionRoutines installs the encoding conversion routines.
func (e *Engine) installBuiltinConversionRoutines() {
	procedures := typedef.BuiltinConversionProcedures(e.roles.domain.Principal(authorization.BootstrapPrincipalID))
	for i := range procedures {
		d := &procedures[i]
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
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
		d := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: seed.ID})
		if err := e.bindDomainCheckRecipes(context.Background(), nil, e.bootstrapAuthorization(d.Owner), nil, d, seed.Constraints, sqltype.InputEnvironment{}); err != nil {
			return err
		}
	}
	for _, seed := range typedef.BuiltinDomainDefaultSeeds() {
		d := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: seed.ID})
		if err := e.bindDomainDefault(context.Background(), nil, e.bootstrapAuthorization(d.Owner), nil, d, seed.Expression, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err != nil {
			return err
		}
	}
	return nil
}

// bootstrapAuthorization is the published-catalog authority of user under
// which builtin seeds are bound.
func (e *Engine) bootstrapAuthorization(user authorization.PrincipalRef) AuthorizationView {
	view := e.catalog.read(e.catalog.state)
	return AuthorizationView{engine: e, View: access.View{
		Owner: e.catalog.owner(), PairedCatalog: view, RoleRecord: e.roles.published, Catalog: view,
		Roles: e.roles.read(e.catalog.owner(), view, e.roles.published), State: authorization.RoleState{CurrentUser: user},
	}}
}

// installBuiltinOperators installs the seeded operator descriptors and the
// shared operator-name index.
func (e *Engine) installBuiltinOperators() {
	e.catalog.state.SetOperatorNames(routine.SeededOperatorNames())
	operators := routine.BuiltinOperatorDescriptors(e.roles.domain.Principal(authorization.BootstrapPrincipalID))
	for i := range operators {
		d := &operators[i]
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
}

// builtinProcedureNames is the procedure-name index of the seeded procedures.
// Engines share it read-only: ProcedureIndexEdit copies its maps and buckets
// before the first change, as for routine.SeededOperatorNames.
var builtinProcedureNames struct {
	once  sync.Once
	index *catalog.ProcedureNameIndex
}

// seedProcedureNames installs the shared index, building it from this
// engine's seed when it is the first in the process.
func (e *Engine) seedProcedureNames() {
	builtinProcedureNames.once.Do(func() {
		builtinProcedureNames.index = catalog.RebuildProcedureNames(e.catalog.state.Descriptors())
	})
	e.catalog.state.SetProcedureNames(builtinProcedureNames.index)
}

// builtinCatalogValidation memoizes provider validation of the built-in seed.
// The seed is fixed by the binary, so the first engine's result is every
// engine's result.
var builtinCatalogValidation struct {
	once sync.Once
	err  error
}

// validateSeed validates the built-in seed once per process and the current
// database descriptor, which depends on engine options, on every engine.
func (e *Engine) validateSeed() error {
	builtinCatalogValidation.once.Do(func() {
		builtinCatalogValidation.err = e.validateCatalogDescriptors(e.catalog.state.Descriptors())
	})
	if builtinCatalogValidation.err != nil {
		return builtinCatalogValidation.err
	}
	database := e.catalog.state.Get(catalog.CurrentDatabaseAddress)
	return e.validateCatalogDescriptors(func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		yield(catalog.CurrentDatabaseAddress, database)
	})
}

// validateCatalogDescriptors checks each built-in descriptor with the
// provider registered for its class.
func (e *Engine) validateCatalogDescriptors(descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) error {
	view := e.catalog.read(e.catalog.state)
	for _, d := range descriptors {
		p := e.catalog.Provider(d.Address().ClassID)
		if p == nil {
			return fmt.Errorf("stuntgres: missing catalog provider for built-in %q (class %d)", d.Name(), d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return fmt.Errorf("stuntgres: invalid built-in catalog descriptor %q (class %d): %w", d.Name(), d.Address().ClassID, err)
		}
	}
	return nil
}
