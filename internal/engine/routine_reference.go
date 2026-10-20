package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Only evaluator-local stack copies select graft ancestry. A user replacement
// cannot inherit the enclosing stored node's manifest or relation owner.
func selectExpressionRecipe(c *EvalContext, recipe *catalog.Relation) error {
	c.routineGraft = recipe != nil
	c.routineRecipe = recipe != nil
	c.routineReferences = nil
	c.routineChecks = nil
	c.routineConsumer = recipe
	if recipe == nil {
		return nil
	}
	auth, _, err := routineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if auth.engine != nil {
		current, ok := auth.Catalog.Relation(recipe.Address())
		if !ok || current.View != recipe.View || recipe.Owner.ID() == 0 || recipe.Owner != auth.Owner.Principals.Principal(recipe.Owner.ID()) {
			return ids.ErrCatalogReplaced
		}
	}
	c.routineChecks = viewOf(recipe).dependencies
	return nil
}
