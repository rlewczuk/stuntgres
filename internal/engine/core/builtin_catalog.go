package core

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// Every engine seeds the same built-in catalog from compiled tables; only the
// current database descriptor depends on engine options. The install steps
// and the seed share are catalog's; newEngine keeps their order and the steps
// that bind.

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
		d := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: seed.ID})
		if err := e.BindDomainConstraintDefinitions(context.Background(), nil, e.bootstrapAuthorization(d.Owner), nil, d, seed.Constraints, sqltype.InputEnvironment{}); err != nil {
			return err
		}
	}
	for _, seed := range typedef.BuiltinDomainDefaultSeeds() {
		d := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: seed.ID})
		if err := e.BindDomainDefault(context.Background(), nil, e.bootstrapAuthorization(d.Owner), nil, d, seed.Expression, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err != nil {
			return err
		}
	}
	return nil
}

// bootstrapAuthorization is the published-catalog authority of user under
// which builtin seeds are bound.
func (e *Engine) bootstrapAuthorization(user authorization.PrincipalRef) AuthorizationView {
	view := e.Catalog.Current()
	return e.AuthorityView(access.View{
		Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.RoleStore.Published, Catalog: view,
		Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, view), State: authorization.RoleState{CurrentUser: user},
	})
}

// builtinSeed is the process's seed share: every engine seeds the same
// builtin catalog, so they share its procedure-name index, name map and
// validation result.
var builtinSeed = &catalog.SeedShare{}
