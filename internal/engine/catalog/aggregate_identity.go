package catalog

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// OwnedAggregate carries the actual definition. Helpers are populated only by
// intagg's lookup-only extension aliases; ordinary aggregates use real selected
// transition/final descriptors and leave Helpers empty.
type OwnedAggregate struct {
	Definition   AggregateDefinition
	Helpers      [2]RoutineIdentity
	BinaryRecipe *AggregateBinaryRecipe
	Variadic     bool // Terminal VARIADIC ANY; paired ordered declarations share this slot.
	ArgumentNames []string // Canonical flattened declaration; nil when entirely unnamed.
}

// SameAggregateDefinition compares complete immutable declaration identity.
// Binary recipes remain pointer-owned evidence; argument names are canonical
// declaration values, independent of their backing slice.
func SameAggregateDefinition(a, b *OwnedAggregate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Definition == b.Definition && a.Helpers == b.Helpers &&
		a.BinaryRecipe == b.BinaryRecipe && a.Variadic == b.Variadic &&
		slices.Equal(a.ArgumentNames, b.ArgumentNames)
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
