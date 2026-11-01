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
	c.AcceptedDefault = o.Accepted
	if o.Accepted != nil || o.Occurrence.Kind != 0 {
		c.DefaultOccurrence.Kind, c.DefaultOccurrence.Row, c.DefaultOccurrence.Slot = o.Occurrence.Kind, o.Occurrence.Row, o.Occurrence.Slot
	}
}

// EvalDefault evaluates node under origin and restores c's origin fields.
func EvalDefault(c *exec.Context, node expr.Expr, origin DefaultOrigin) (sqltype.Value, error) {
	previousOrigin, recipe, graft := c.FoldOrigin, c.RoutineRecipe, c.RoutineGraft
	checks, refs, consumer := c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer
	accepted := c.AcceptedDefault
	occurrence := c.DefaultOccurrence
	ApplyDefaultOrigin(origin, c)
	defer func() {
		c.FoldOrigin, c.RoutineRecipe, c.RoutineGraft = previousOrigin, recipe, graft
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = checks, refs, consumer
		c.AcceptedDefault = accepted
		c.DefaultOccurrence = occurrence
	}()
	return node.Eval(c)
}

// PrepareDefault joins the consumed stored-column lane to source-bound typed
// preparation. Domain/generated defaults keep their independent ordinary path.
func PrepareDefault(c *exec.Context, node expr.Expr, origin DefaultOrigin, policy exec.DefaultPreparationPolicy) (expr.Expr, DefaultOrigin, bool, error) {
	ApplyDefaultOrigin(origin, c)
	if c.Host != nil && origin.Consumer != nil && origin.Consumer.ColumnMeta.Defaults[origin.Fold.Attribute].Expression != nil {
		prepared, artifact, changed, err := c.Host.PrepareAcceptedDefault(c, origin.Consumer, origin.Fold, policy)
		if artifact != nil {
			original, ref, attribute, definition := artifact.Source()
			origin.Consumer, origin.Definition, origin.Accepted = original, definition, artifact
			origin.Fold.Owner, origin.Fold.Attribute, origin.Fold.Definition = ref, attribute, definition.OID
		}
		origin.Occurrence = c.DefaultOccurrence
		return prepared, origin, changed, err
	}
	prepared, changed, err := exec.PreflightDefault(c, node, policy)
	return prepared, origin, changed, err
}
