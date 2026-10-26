package analyze

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// ApplyDefaultOrigin installs a default's fold and routine origin on c.
func ApplyDefaultOrigin(o DefaultOrigin, c *exec.Context) {
	c.FoldOrigin = o.Fold
	c.RoutineRecipe, c.RoutineGraft = true, false
	c.RoutineChecks = nil
	c.RoutineReferences, c.RoutineConsumer = o.Definition.Dependencies, o.Consumer
}

// EvalDefault evaluates node under origin and restores c's origin fields.
func EvalDefault(c *exec.Context, node expr.Expr, origin DefaultOrigin) (sqltype.Value, error) {
	previousOrigin, recipe, graft := c.FoldOrigin, c.RoutineRecipe, c.RoutineGraft
	checks, refs, consumer := c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer
	ApplyDefaultOrigin(origin, c)
	defer func() {
		c.FoldOrigin, c.RoutineRecipe, c.RoutineGraft = previousOrigin, recipe, graft
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = checks, refs, consumer
	}()
	return node.Eval(c)
}
