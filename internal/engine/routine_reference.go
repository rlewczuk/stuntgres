package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

// Only evaluator-local stack copies select graft ancestry. A user replacement
// cannot inherit the enclosing stored node's manifest or relation owner.
func selectExpressionRecipe(c *exec.Context, recipe *catalog.Relation) error {
	c.RoutineGraft = recipe != nil
	c.RoutineRecipe = recipe != nil
	c.RoutineReferences = nil
	c.RoutineChecks = nil
	c.RoutineConsumer = recipe
	if recipe == nil {
		return nil
	}
	auth, _, err := routineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if auth.Live() {
		current, ok := auth.Catalog.Relation(recipe.Address())
		if !ok || current.View != recipe.View || recipe.Owner.ID() == 0 || recipe.Owner != auth.Owner.Principals.Principal(recipe.Owner.ID()) {
			return ids.ErrCatalogReplaced
		}
	}
	c.RoutineChecks = recipe.View.Dependencies()
	return nil
}
