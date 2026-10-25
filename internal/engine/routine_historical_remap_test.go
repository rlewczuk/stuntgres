package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/routine"
)

// replacementProcedurePolicyFixture supplies the real host policy for private
// address/type remap fixtures; it does not manufacture numeric principal links.
func replacementProcedurePolicyFixture(source, target *core.Engine) catalog.ProcedureRemapper {
	sourceRoles := source.RoleStore.PublishedRead(&source.Catalog.Owner, source.Catalog.Current())
	targetRoles := target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current())
	return catalog.NewProcedureRemapper(target.RoleStore.PrincipalRemap(&target.Catalog, sourceRoles, targetRoles, nil, routine.OperatorSeeded))
}
