package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// OwnedAggregate carries the actual definition. Helpers are populated only by
// intagg's lookup-only extension aliases; ordinary aggregates use real SFUNC
// descriptors and leave Helpers empty.
type OwnedAggregate struct {
	Definition   AggregateDefinition
	Helpers      [2]RoutineIdentity
	BinaryRecipe *AggregateBinaryRecipe
}

// AggregateBinaryRecipe retains the admitted direct declared edges, not a
// dependency on their continued catalog presence. Both endpoints remain ordinary
// aggregate/support type dependencies. Published recipes are immutable.
type AggregateBinaryRecipe struct {
	Casts []Cast
}

// AggregateConversion uses only retained declaration evidence or fixed binary
// identity/domain rules. It never consults a later replacement pg_cast row.
func AggregateConversion(recipe *AggregateBinaryRecipe, source, target sqltype.Usage) (sqltype.CoercionPlan, error) {
	if aggregateBinaryRecipeAllows(recipe, source, target) {
		return sqltype.PlanDeclaredBinaryCoercion(source, target)
	}
	if !sqltype.BinaryCoercible(source.Type, target.Type) {
		return sqltype.CoercionPlan{}, fmt.Errorf("stuntgres: missing aggregate binary conversion")
	}
	return sqltype.PlanCoercion(source, target, sqltype.Implicit)
}

// AggregateCanCoerce is pure specialization feasibility for a retained
// declaration. Fixed coercions participate in common-type selection; binary-only
// support admission and physical conversion remain AggregateConversion's job.
func AggregateCanCoerce(recipe *AggregateBinaryRecipe, source, target sqltype.Usage, mode sqltype.CoercionContext) bool {
	return aggregateBinaryRecipeAllows(recipe, source, target) || CanCoerce(Read{}, source, target, mode) == nil
}

func aggregateBinaryRecipeAllows(recipe *AggregateBinaryRecipe, source, target sqltype.Usage) bool {
	if recipe == nil || !source.Valid() || !target.Valid() {
		return false
	}
	for _, edge := range recipe.Casts {
		if edge.Source == source.BaseUsage().Type.ID() && edge.Target == target.Type.ID() && edge.Method == "b" && edge.Context == "i" && edge.Function == (ids.ObjectAddress{}) {
			return true
		}
	}
	return false
}
